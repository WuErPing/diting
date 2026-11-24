package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const minutesDir = "minutes"

// Minutes is one day's generated meeting minutes, persisted so it can be
// reopened later without calling the model again.
type Minutes struct {
	Date        string    `json:"date"`
	Segments    int       `json:"segments"`
	Markdown    string    `json:"markdown"`
	GeneratedAt time.Time `json:"generatedAt"`
}

func validMinutesDate(date string) error {
	parsed, err := time.ParseInLocation("2006-01-02", date, time.Local)
	if err != nil || parsed.Format("2006-01-02") != date {
		return fmt.Errorf("invalid date %q", date)
	}
	return nil
}

func (s *Store) minutesPath(date string) string {
	return filepath.Join(s.root, minutesDir, date+".json")
}

// SaveMinutes atomically writes one day's minutes under data/minutes.
func (s *Store) SaveMinutes(date string, segments int, markdown string) (Minutes, error) {
	if err := validMinutesDate(date); err != nil {
		return Minutes{}, err
	}
	if strings.TrimSpace(markdown) == "" {
		return Minutes{}, fmt.Errorf("minutes markdown is empty")
	}
	record := Minutes{Date: date, Segments: segments, Markdown: markdown, GeneratedAt: time.Now()}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return Minutes{}, fmt.Errorf("encode minutes: %w", err)
	}
	data = append(data, '\n')

	dir := filepath.Join(s.root, minutesDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Minutes{}, fmt.Errorf("create minutes directory: %w", err)
	}
	file, err := os.CreateTemp(dir, ".minutes-*.json")
	if err != nil {
		return Minutes{}, fmt.Errorf("create minutes temp file: %w", err)
	}
	tmp := file.Name()
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(tmp)
	}
	if _, err := file.Write(data); err != nil {
		cleanup()
		return Minutes{}, fmt.Errorf("write minutes: %w", err)
	}
	if err := file.Sync(); err != nil {
		cleanup()
		return Minutes{}, fmt.Errorf("sync minutes: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tmp)
		return Minutes{}, fmt.Errorf("close minutes: %w", err)
	}
	if err := os.Rename(tmp, s.minutesPath(date)); err != nil {
		_ = os.Remove(tmp)
		return Minutes{}, fmt.Errorf("replace minutes: %w", err)
	}
	return record, nil
}

// DayMinutes reads one day's saved minutes; os.ErrNotExist when none exist.
func (s *Store) DayMinutes(date string) (Minutes, error) {
	if err := validMinutesDate(date); err != nil {
		return Minutes{}, err
	}
	data, err := os.ReadFile(s.minutesPath(date))
	if err != nil {
		return Minutes{}, err
	}
	var record Minutes
	if err := json.Unmarshal(data, &record); err != nil {
		return Minutes{}, fmt.Errorf("parse minutes %s: %w", date, err)
	}
	return record, nil
}

// MinutesDates lists the dates that have saved minutes, ascending.
func (s *Store) MinutesDates() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(s.root, minutesDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read minutes directory: %w", err)
	}
	var dates []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		date := strings.TrimSuffix(name, ".json")
		if validMinutesDate(date) == nil {
			dates = append(dates, date)
		}
	}
	sort.Strings(dates)
	return dates, nil
}

// DeleteMinutes removes one day's saved minutes; missing files are ignored.
func (s *Store) DeleteMinutes(date string) error {
	if err := validMinutesDate(date); err != nil {
		return err
	}
	if err := os.Remove(s.minutesPath(date)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("delete minutes %s: %w", date, err)
	}
	return nil
}
