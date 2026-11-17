package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	transcriptFile   = "transcripts.jsonl"
	transcriptBackup = "transcripts.jsonl.bak"
	lockFileName     = ".lock"
	historyLimit     = 500
	audioDir         = "audio"
)

type Segment struct {
	ID         string    `json:"id"`
	StartedAt  time.Time `json:"startedAt"`
	EndedAt    time.Time `json:"endedAt"`
	DurationMS int64     `json:"durationMs"`
	Text       string    `json:"text"`
	Speaker    string    `json:"speaker,omitempty"`
	AudioPath  string    `json:"audioPath"`
}

type Store struct {
	root string
	lock *os.File
	mu   sync.RWMutex
	log  []Segment

	statsCache *Stats
	daysCache  []DayStat
}

func Open(root string) (*Store, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	lock, err := os.OpenFile(filepath.Join(root, lockFileName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open data lock: %w", err)
	}
	if err := acquireLock(lock); err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("data directory %s is already in use by another diting process: %w", root, err)
	}
	s := &Store{root: root, lock: lock}
	if err := s.load(); err != nil {
		_ = releaseLock(lock)
		_ = lock.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lock == nil {
		return nil
	}
	unlockErr := releaseLock(s.lock)
	closeErr := s.lock.Close()
	s.lock = nil
	return errors.Join(unlockErr, closeErr)
}

func (s *Store) Root() string {
	return s.root
}

func (s *Store) Save(segment Segment, samples []float32) (Segment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if segment.ID == "" {
		segment.ID = time.Now().Format("20060102-150405.000000000")
	}
	segment.DurationMS = segment.EndedAt.Sub(segment.StartedAt).Milliseconds()
	day := segment.StartedAt.Format("20060102")
	dayDir := filepath.Join(s.root, audioDir, day)
	wavPath := filepath.Join(dayDir, segment.ID+audioExtWAV)
	if err := os.MkdirAll(dayDir, 0o700); err != nil {
		return Segment{}, fmt.Errorf("create audio directory: %w", err)
	}
	if err := writeWAVFile(wavPath, samples); err != nil {
		return Segment{}, err
	}

	audioPath := filepath.ToSlash(filepath.Join(audioDir, day, segment.ID+audioExtWAV))
	finalPath := wavPath
	if ext := audioExt(); ext != audioExtWAV {
		compressedPath := filepath.Join(dayDir, segment.ID+ext)
		if err := encodeAudio(wavPath, compressedPath); err != nil {
			warnAudioFallback(err)
			_ = os.Remove(compressedPath)
		} else if err := os.Chmod(compressedPath, 0o600); err != nil {
			// afconvert creates 0644; fall back rather than keep a readable file.
			warnAudioFallback(err)
			_ = os.Remove(compressedPath)
		} else {
			_ = os.Remove(wavPath)
			audioPath = filepath.ToSlash(filepath.Join(audioDir, day, segment.ID+ext))
			finalPath = compressedPath
		}
	}
	segment.AudioPath = audioPath

	logPath := filepath.Join(s.root, transcriptFile)
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		_ = os.Remove(finalPath)
		return Segment{}, fmt.Errorf("open transcript log: %w", err)
	}
	writeErr := json.NewEncoder(file).Encode(segment)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(finalPath)
		return Segment{}, errors.Join(writeErr, closeErr)
	}

	s.log = append(s.log, segment)
	s.log = trimHistory(s.log)
	s.statsCache = nil
	s.daysCache = nil
	return segment, nil
}

func (s *Store) All() ([]Segment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return readTranscripts(filepath.Join(s.root, transcriptFile))
}

func (s *Store) Rewrite(segments []Segment) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rewriteLocked(segments)
}

func (s *Store) rewriteLocked(segments []Segment) error {
	logPath := filepath.Join(s.root, transcriptFile)
	if err := backupFile(logPath, filepath.Join(s.root, transcriptBackup)); err != nil {
		return err
	}

	file, err := os.CreateTemp(s.root, ".transcripts-*.jsonl")
	if err != nil {
		return fmt.Errorf("create transcript temp file: %w", err)
	}
	tmp := file.Name()
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(tmp)
	}

	writer := bufio.NewWriter(file)
	encoder := json.NewEncoder(writer)
	for _, segment := range segments {
		if err := encoder.Encode(segment); err != nil {
			cleanup()
			return fmt.Errorf("encode transcript: %w", err)
		}
	}
	if err := writer.Flush(); err != nil {
		cleanup()
		return fmt.Errorf("flush transcript: %w", err)
	}
	if err := file.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("sync transcript: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close transcript: %w", err)
	}
	if err := os.Rename(tmp, logPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace transcript: %w", err)
	}

	s.log = trimHistory(segments)
	s.statsCache = nil
	s.daysCache = nil
	return nil
}

func (s *Store) List(limit int, before time.Time) []Segment {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	result := make([]Segment, 0, limit)
	for i := len(s.log) - 1; i >= 0 && len(result) < limit; i-- {
		segment := s.log[i]
		if !before.IsZero() && !segment.StartedAt.Before(before) {
			continue
		}
		result = append(result, segment)
	}
	return result
}

// Stats aggregates the full transcript log and audio directory on disk, not
// just the in-memory history window.
type Stats struct {
	AudioFiles      int   `json:"audioFiles"`
	AudioBytes      int64 `json:"audioBytes"`
	Segments        int   `json:"segments"`
	TranscriptChars int   `json:"transcriptChars"`
}

func (s *Store) Stats() (Stats, error) {
	s.mu.RLock()
	if s.statsCache != nil {
		stats := *s.statsCache
		s.mu.RUnlock()
		return stats, nil
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.statsCache != nil {
		return *s.statsCache, nil
	}
	stats, err := s.computeStatsLocked()
	if err != nil {
		return Stats{}, err
	}
	s.statsCache = &stats
	return stats, nil
}

func (s *Store) computeStatsLocked() (Stats, error) {
	segments, err := readTranscripts(filepath.Join(s.root, transcriptFile))
	if err != nil {
		return Stats{}, err
	}
	stats := Stats{Segments: len(segments)}
	for _, segment := range segments {
		stats.TranscriptChars += utf8.RuneCountInString(strings.TrimSpace(segment.Text))
	}

	err = filepath.WalkDir(filepath.Join(s.root, audioDir), func(_ string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, fs.ErrNotExist) {
				return nil
			}
			return walkErr
		}
		if entry.IsDir() || !isAudioFile(entry.Name()) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		stats.AudioFiles++
		stats.AudioBytes += info.Size()
		return nil
	})
	if err != nil {
		return Stats{}, err
	}
	return stats, nil
}

// DayStat aggregates one local calendar day across transcripts and audio files.
type DayStat struct {
	Date       string `json:"date"`
	Segments   int    `json:"segments"`
	Chars      int    `json:"chars"`
	AudioFiles int    `json:"audioFiles"`
	AudioBytes int64  `json:"audioBytes"`
}

func (s *Store) Days() ([]DayStat, error) {
	s.mu.RLock()
	if s.daysCache != nil {
		days := append([]DayStat(nil), s.daysCache...)
		s.mu.RUnlock()
		return days, nil
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.daysCache != nil {
		return append([]DayStat(nil), s.daysCache...), nil
	}
	days, err := s.computeDaysLocked()
	if err != nil {
		return nil, err
	}
	s.daysCache = days
	return append([]DayStat(nil), days...), nil
}

func (s *Store) computeDaysLocked() ([]DayStat, error) {
	segments, err := readTranscripts(filepath.Join(s.root, transcriptFile))
	if err != nil {
		return nil, err
	}
	byDate := make(map[string]*DayStat)
	for _, segment := range segments {
		date := segment.StartedAt.Local().Format("2006-01-02")
		day := byDate[date]
		if day == nil {
			day = &DayStat{Date: date}
			byDate[date] = day
		}
		day.Segments++
		day.Chars += utf8.RuneCountInString(strings.TrimSpace(segment.Text))
	}

	err = filepath.WalkDir(filepath.Join(s.root, audioDir), func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, fs.ErrNotExist) {
				return nil
			}
			return walkErr
		}
		if entry.IsDir() || !isAudioFile(entry.Name()) {
			return nil
		}
		date, ok := audioDay(path)
		if !ok {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		day := byDate[date]
		if day == nil {
			day = &DayStat{Date: date}
			byDate[date] = day
		}
		day.AudioFiles++
		day.AudioBytes += info.Size()
		return nil
	})
	if err != nil {
		return nil, err
	}

	days := make([]DayStat, 0, len(byDate))
	for _, day := range byDate {
		days = append(days, *day)
	}
	sort.Slice(days, func(i, j int) bool { return days[i].Date < days[j].Date })
	return days, nil
}

// DayDelete reports what a DeleteDay call removed.
type DayDelete struct {
	Date       string   `json:"date"`
	Segments   int      `json:"segments"`
	AudioFiles int      `json:"audioFiles"`
	AudioBytes int64    `json:"audioBytes"`
	TrashPath  string   `json:"trashPath,omitempty"`
	Snapshot   string   `json:"snapshot,omitempty"`
	DryRun     bool     `json:"dryRun,omitempty"`
	SegmentIDs []string `json:"-"`
}

// DeleteDay removes one local day's transcripts and moves its audio directory
// to the Trash. When dryRun is true it only reports the impact without
// modifying any files. A real deletion writes a snapshot of the removed
// transcripts under data/backups so the text can be restored later.
func (s *Store) DeleteDay(date string, dryRun bool) (DayDelete, error) {
	parsed, err := time.ParseInLocation("2006-01-02", date, time.Local)
	if err != nil || parsed.Format("2006-01-02") != date {
		return DayDelete{}, fmt.Errorf("invalid date %q", date)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	segments, err := readTranscripts(filepath.Join(s.root, transcriptFile))
	if err != nil {
		return DayDelete{}, err
	}
	keep := make([]Segment, 0, len(segments))
	removed := make([]Segment, 0)
	result := DayDelete{Date: date, DryRun: dryRun}
	for _, segment := range segments {
		if segment.StartedAt.Local().Format("2006-01-02") == date {
			result.Segments++
			result.SegmentIDs = append(result.SegmentIDs, segment.ID)
			removed = append(removed, segment)
			continue
		}
		keep = append(keep, segment)
	}
	dayDir := filepath.Join(s.root, audioDir, parsed.Format("20060102"))
	if _, err := os.Stat(dayDir); err == nil {
		result.AudioFiles, result.AudioBytes, err = audioDirStats(dayDir)
		if err != nil {
			return result, err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return result, err
	}
	if dryRun {
		return result, nil
	}

	if len(removed) > 0 {
		snapshotDir, err := s.writeDaySnapshotLocked(parsed, removed)
		if err != nil {
			return DayDelete{}, fmt.Errorf("write deletion snapshot: %w", err)
		}
		result.Snapshot = snapshotDir
	}
	if err := s.rewriteLocked(keep); err != nil {
		return DayDelete{}, err
	}

	if _, err := os.Stat(dayDir); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return result, nil
		}
		return result, err
	}
	destination, err := moveDirToTrash(dayDir, "diting-"+parsed.Format("20060102")+"-"+time.Now().Format("150405"))
	result.TrashPath = destination
	if err != nil {
		return result, fmt.Errorf("move audio to trash: %w", err)
	}
	return result, nil
}

// RestoreResult reports how many segments were restored from a snapshot.
type RestoreResult struct {
	Date     string `json:"date"`
	Restored int    `json:"restored"`
	Skipped  int    `json:"skipped"`
	Snapshot string `json:"snapshot"`
}

// RestoreDay merges the latest snapshot for date back into transcripts.jsonl.
// Segments whose IDs already exist are skipped so the operation is idempotent.
// Audio files are not moved automatically; if the audio directory was moved to
// Trash it must be dragged back manually before playback is possible.
func (s *Store) RestoreDay(date string) (RestoreResult, error) {
	parsed, err := time.ParseInLocation("2006-01-02", date, time.Local)
	if err != nil || parsed.Format("2006-01-02") != date {
		return RestoreResult{}, fmt.Errorf("invalid date %q", date)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	snapshotDir, err := s.latestSnapshotLocked(parsed)
	if err != nil {
		return RestoreResult{}, err
	}
	snapshotSegments, err := readTranscripts(filepath.Join(snapshotDir, transcriptFile))
	if err != nil {
		return RestoreResult{}, fmt.Errorf("read snapshot: %w", err)
	}
	current, err := readTranscripts(filepath.Join(s.root, transcriptFile))
	if err != nil {
		return RestoreResult{}, err
	}
	existing := make(map[string]struct{}, len(current))
	for _, segment := range current {
		existing[segment.ID] = struct{}{}
	}
	result := RestoreResult{Date: date, Snapshot: snapshotDir}
	merged := append([]Segment(nil), current...)
	for _, segment := range snapshotSegments {
		if _, ok := existing[segment.ID]; ok {
			result.Skipped++
			continue
		}
		merged = append(merged, segment)
		result.Restored++
	}
	if result.Restored == 0 {
		return result, nil
	}
	sort.SliceStable(merged, func(i, j int) bool {
		return merged[i].StartedAt.Before(merged[j].StartedAt)
	})
	if err := s.rewriteLocked(merged); err != nil {
		return RestoreResult{}, err
	}
	return result, nil
}

func (s *Store) latestSnapshotLocked(day time.Time) (string, error) {
	backupRoot := filepath.Join(s.root, "backups")
	entries, err := os.ReadDir(backupRoot)
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("no snapshots found for %s", day.Format("2006-01-02"))
	}
	if err != nil {
		return "", fmt.Errorf("read backups: %w", err)
	}
	prefix := day.Format("20060102") + "-"
	var candidates []string
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), prefix) {
			candidates = append(candidates, entry.Name())
		}
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("no snapshots found for %s", day.Format("2006-01-02"))
	}
	sort.Strings(candidates)
	return filepath.Join(backupRoot, candidates[len(candidates)-1]), nil
}

func (s *Store) writeDaySnapshotLocked(day time.Time, segments []Segment) (string, error) {
	backupRoot := filepath.Join(s.root, "backups")
	snapshotDir := filepath.Join(backupRoot, day.Format("20060102")+"-"+time.Now().Format("150405"))
	if err := os.MkdirAll(snapshotDir, 0o700); err != nil {
		return "", fmt.Errorf("create snapshot directory: %w", err)
	}
	path := filepath.Join(snapshotDir, transcriptFile)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return "", fmt.Errorf("create snapshot transcript: %w", err)
	}
	writer := bufio.NewWriter(file)
	encoder := json.NewEncoder(writer)
	for _, segment := range segments {
		if err := encoder.Encode(segment); err != nil {
			_ = file.Close()
			return "", fmt.Errorf("encode snapshot segment: %w", err)
		}
	}
	if err := writer.Flush(); err != nil {
		_ = file.Close()
		return "", fmt.Errorf("flush snapshot: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return "", fmt.Errorf("sync snapshot: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close snapshot: %w", err)
	}
	return snapshotDir, nil
}

func audioDay(path string) (string, bool) {
	if parsed, err := time.ParseInLocation("20060102", filepath.Base(filepath.Dir(path)), time.Local); err == nil {
		return parsed.Format("2006-01-02"), true
	}
	name := filepath.Base(path)
	if len(name) >= 8 {
		if parsed, err := time.ParseInLocation("20060102", name[:8], time.Local); err == nil {
			return parsed.Format("2006-01-02"), true
		}
	}
	return "", false
}

func audioDirStats(dir string) (int, int64, error) {
	files := 0
	var bytes int64
	err := filepath.WalkDir(dir, func(_ string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !isAudioFile(entry.Name()) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		files++
		bytes += info.Size()
		return nil
	})
	return files, bytes, err
}

func (s *Store) Audio(id string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := len(s.log) - 1; i >= 0; i-- {
		if s.log[i].ID == id {
			path := filepath.Join(s.root, filepath.FromSlash(s.log[i].AudioPath))
			return path, nil
		}
	}
	segments, err := readTranscripts(filepath.Join(s.root, transcriptFile))
	if err != nil {
		return "", err
	}
	for i := len(segments) - 1; i >= 0; i-- {
		if segments[i].ID == id {
			return filepath.Join(s.root, filepath.FromSlash(segments[i].AudioPath)), nil
		}
	}
	return "", os.ErrNotExist
}

func (s *Store) load() error {
	segments, err := readTranscripts(filepath.Join(s.root, transcriptFile))
	if err != nil {
		return fmt.Errorf("read transcript history: %w", err)
	}
	s.log = trimHistory(segments)
	return nil
}

func readTranscripts(path string) ([]Segment, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open transcript history: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	var segments []Segment
	for scanner.Scan() {
		var segment Segment
		if json.Unmarshal(scanner.Bytes(), &segment) == nil && segment.ID != "" {
			segments = append(segments, segment)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read transcript history: %w", err)
	}
	return segments, nil
}

func trimHistory(segments []Segment) []Segment {
	if len(segments) > historyLimit {
		segments = segments[len(segments)-historyLimit:]
	}
	return append([]Segment(nil), segments...)
}

func backupFile(source, destination string) error {
	data, err := os.ReadFile(source)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read transcript backup source: %w", err)
	}
	if err := os.WriteFile(destination, data, 0o600); err != nil {
		return fmt.Errorf("write transcript backup: %w", err)
	}
	return nil
}
