package paragraph

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

const FileName = "paragraphs.jsonl"

type Store struct {
	root string
	mu   sync.RWMutex
	list []Paragraph
}

func Open(root string) (*Store, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	store := &Store{root: root}
	list, err := readParagraphs(filepath.Join(root, FileName))
	if err != nil {
		return nil, err
	}
	store.list = normalize(list)
	return store, nil
}

func (s *Store) All() []Paragraph {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Paragraph(nil), s.list...)
}

// SaveMany upserts paragraphs by ID and removes any paragraph that shares a
// segment with an incoming one, so a re-run always supersedes older splits.
func (s *Store) SaveMany(paragraphs []Paragraph) error {
	if len(paragraphs) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	incomingIDs := make(map[string]struct{}, len(paragraphs))
	incomingSegments := make(map[string]struct{})
	for _, paragraph := range paragraphs {
		incomingIDs[paragraph.ID] = struct{}{}
		for _, id := range paragraph.SegmentIDs {
			incomingSegments[id] = struct{}{}
		}
	}

	next := make([]Paragraph, 0, len(s.list)+len(paragraphs))
	for _, existing := range s.list {
		if _, ok := incomingIDs[existing.ID]; ok {
			continue
		}
		overlap := false
		for _, id := range existing.SegmentIDs {
			if _, ok := incomingSegments[id]; ok {
				overlap = true
				break
			}
		}
		if overlap {
			continue
		}
		next = append(next, existing)
	}
	next = append(next, paragraphs...)
	next = normalize(next)
	if err := s.rewriteLocked(next); err != nil {
		return err
	}
	s.list = next
	return nil
}

func (s *Store) Replace(paragraphs []Paragraph) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := normalize(append([]Paragraph(nil), paragraphs...))
	if err := s.rewriteLocked(next); err != nil {
		return err
	}
	s.list = next
	return nil
}

func (s *Store) Reset() error {
	return s.Replace(nil)
}

// RemoveSegments drops every paragraph intersecting the deleted segment IDs.
// A paragraph that spans a deleted day is discarded whole and will be rebuilt
// from the surviving segments by the worker's backfill.
func (s *Store) RemoveSegments(ids map[string]struct{}) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	next := make([]Paragraph, 0, len(s.list))
	removed := 0
	for _, paragraph := range s.list {
		drop := false
		for _, id := range paragraph.SegmentIDs {
			if _, ok := ids[id]; ok {
				drop = true
				break
			}
		}
		if drop {
			removed++
			continue
		}
		next = append(next, paragraph)
	}
	if removed == 0 {
		return 0, nil
	}
	if err := s.rewriteLocked(next); err != nil {
		return 0, err
	}
	s.list = next
	return removed, nil
}

func (s *Store) Coverage() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	coverage := make(map[string]string)
	for _, paragraph := range s.list {
		for _, id := range paragraph.SegmentIDs {
			coverage[id] = paragraph.Source
		}
	}
	return coverage
}

func (s *Store) rewriteLocked(paragraphs []Paragraph) error {
	path := filepath.Join(s.root, FileName)
	file, err := os.CreateTemp(s.root, ".paragraphs-*.jsonl")
	if err != nil {
		return fmt.Errorf("create paragraph temp file: %w", err)
	}
	tmp := file.Name()
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(tmp)
	}

	writer := bufio.NewWriter(file)
	encoder := json.NewEncoder(writer)
	for _, paragraph := range paragraphs {
		if err := encoder.Encode(paragraph); err != nil {
			cleanup()
			return fmt.Errorf("encode paragraph: %w", err)
		}
	}
	if err := writer.Flush(); err != nil {
		cleanup()
		return fmt.Errorf("flush paragraph: %w", err)
	}
	if err := file.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("sync paragraph: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close paragraph: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace paragraph: %w", err)
	}
	return nil
}

// normalize sorts by start time and drops duplicate or overlapping entries.
func normalize(paragraphs []Paragraph) []Paragraph {
	sort.SliceStable(paragraphs, func(i, j int) bool {
		if paragraphs[i].StartedAt.Equal(paragraphs[j].StartedAt) {
			return paragraphs[i].ID < paragraphs[j].ID
		}
		return paragraphs[i].StartedAt.Before(paragraphs[j].StartedAt)
	})
	owner := make(map[string]struct{})
	result := make([]Paragraph, 0, len(paragraphs))
	for _, paragraph := range paragraphs {
		if paragraph.ID == "" || len(paragraph.SegmentIDs) == 0 {
			continue
		}
		overlap := false
		for _, id := range paragraph.SegmentIDs {
			if _, ok := owner[id]; ok {
				overlap = true
				break
			}
		}
		if overlap {
			continue
		}
		for _, id := range paragraph.SegmentIDs {
			owner[id] = struct{}{}
		}
		result = append(result, paragraph)
	}
	return result
}

func readParagraphs(path string) ([]Paragraph, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open paragraph log: %w", err)
	}
	defer file.Close()

	var result []Paragraph
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var paragraph Paragraph
		if err := json.Unmarshal(line, &paragraph); err != nil {
			continue
		}
		result = append(result, paragraph)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read paragraph log: %w", err)
	}
	return result, nil
}
