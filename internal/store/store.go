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
	transcriptFile = "transcripts.jsonl"
	historyLimit   = 500
)

type Segment struct {
	ID         string    `json:"id"`
	StartedAt  time.Time `json:"startedAt"`
	EndedAt    time.Time `json:"endedAt"`
	DurationMS int64     `json:"durationMs"`
	Text       string    `json:"text"`
	AudioPath  string    `json:"audioPath"`
}

type Store struct {
	root string
	mu   sync.RWMutex
	log  []Segment
}

func Open(root string) (*Store, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	s := &Store{root: root}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
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
	if len(s.log) > historyLimit {
		s.log = append([]Segment(nil), s.log[len(s.log)-historyLimit:]...)
	}
	return segment, nil
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
	file, err := os.Open(filepath.Join(s.root, transcriptFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open transcript history: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		var segment Segment
		if json.Unmarshal(scanner.Bytes(), &segment) == nil && segment.ID != "" {
			s.log = append(s.log, segment)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read transcript history: %w", err)
	}
	if len(s.log) > historyLimit {
		s.log = s.log[len(s.log)-historyLimit:]
	}
	return nil
}
