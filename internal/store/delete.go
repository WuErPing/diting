package store

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

type SegmentDelete struct {
	ID        string `json:"id"`
	TrashPath string `json:"trashPath,omitempty"`
}

func (s *Store) DeleteSegment(id string) (SegmentDelete, error) {
	result := SegmentDelete{ID: id}
	s.mu.Lock()
	defer s.mu.Unlock()

	segments, err := readTranscripts(filepath.Join(s.root, transcriptFile))
	if err != nil {
		return result, err
	}
	index := -1
	for i := range segments {
		if segments[i].ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		return result, os.ErrNotExist
	}
	removed := segments[index]
	keep := make([]Segment, 0, len(segments)-1)
	keep = append(keep, segments[:index]...)
	keep = append(keep, segments[index+1:]...)
	if err := s.rewriteLocked(keep); err != nil {
		return result, err
	}
	if removed.AudioPath == "" {
		return result, nil
	}

	path := filepath.Join(s.root, filepath.FromSlash(removed.AudioPath))
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return result, nil
	} else if err != nil {
		return result, err
	}
	result.TrashPath, err = moveFileToTrash(path, removed.ID+filepath.Ext(path))
	if err != nil {
		return result, err
	}
	return result, nil
}

// DeleteSegments removes every matching segment in one transcript rewrite.
// Unknown IDs are skipped; audio files are moved to Trash individually.
func (s *Store) DeleteSegments(ids []string) ([]SegmentDelete, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	segments, err := readTranscripts(filepath.Join(s.root, transcriptFile))
	if err != nil {
		return nil, err
	}
	wanted := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		wanted[id] = struct{}{}
	}
	keep := make([]Segment, 0, len(segments))
	var removed []Segment
	for _, segment := range segments {
		if _, ok := wanted[segment.ID]; ok {
			removed = append(removed, segment)
			continue
		}
		keep = append(keep, segment)
	}
	if len(removed) == 0 {
		return nil, nil
	}
	if err := s.rewriteLocked(keep); err != nil {
		return nil, err
	}
	results := make([]SegmentDelete, 0, len(removed))
	for _, segment := range removed {
		result := SegmentDelete{ID: segment.ID}
		if segment.AudioPath != "" {
			path := filepath.Join(s.root, filepath.FromSlash(segment.AudioPath))
			if _, err := os.Stat(path); err == nil {
				result.TrashPath, err = moveFileToTrash(path, segment.ID+filepath.Ext(path))
				if err != nil {
					return results, err
				}
			} else if !errors.Is(err, fs.ErrNotExist) {
				return results, err
			}
		}
		results = append(results, result)
	}
	return results, nil
}
