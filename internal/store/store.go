package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	transcriptFile   = "transcripts.jsonl"
	transcriptBackup = "transcripts.jsonl.bak"
	lockFileName     = ".lock"
	historyLimit     = 500
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
	segment.AudioPath = filepath.ToSlash(filepath.Join(
		"audio", segment.StartedAt.Format("20060102"), segment.ID+".wav",
	))
	wavPath := filepath.Join(s.root, filepath.FromSlash(segment.AudioPath))
	if err := os.MkdirAll(filepath.Dir(wavPath), 0o700); err != nil {
		return Segment{}, fmt.Errorf("create audio directory: %w", err)
	}
	if err := writeWAVFile(wavPath, samples); err != nil {
		return Segment{}, err
	}

	logPath := filepath.Join(s.root, transcriptFile)
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		_ = os.Remove(wavPath)
		return Segment{}, fmt.Errorf("open transcript log: %w", err)
	}
	writeErr := json.NewEncoder(file).Encode(segment)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(wavPath)
		return Segment{}, errors.Join(writeErr, closeErr)
	}

	s.log = append(s.log, segment)
	s.log = trimHistory(s.log)
	return segment, nil
}

func (s *Store) All() ([]Segment, error) {
	return readTranscripts(filepath.Join(s.root, transcriptFile))
}

func (s *Store) Rewrite(segments []Segment) error {
	s.mu.Lock()
	defer s.mu.Unlock()

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

func (s *Store) Audio(id string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := len(s.log) - 1; i >= 0; i-- {
		if s.log[i].ID == id {
			path := filepath.Join(s.root, filepath.FromSlash(s.log[i].AudioPath))
			return path, nil
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
