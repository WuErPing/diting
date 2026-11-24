package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func openMinutesStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestMinutesRoundTrip(t *testing.T) {
	s := openMinutesStore(t)

	if _, err := s.DayMinutes("2026-11-07"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing minutes error = %v", err)
	}
	dates, err := s.MinutesDates()
	if err != nil || len(dates) != 0 {
		t.Fatalf("dates = %v, err = %v", dates, err)
	}

	saved, err := s.SaveMinutes("2026-11-07", 3, "# 2026-11-07 会议纪要\n")
	if err != nil {
		t.Fatal(err)
	}
	if saved.Date != "2026-11-07" || saved.Segments != 3 || saved.GeneratedAt.IsZero() {
		t.Fatalf("saved = %+v", saved)
	}
	if _, err := s.SaveMinutes("2026-10-01", 1, "# 2026-10-01 会议纪要\n"); err != nil {
		t.Fatal(err)
	}

	loaded, err := s.DayMinutes("2026-11-07")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Markdown != saved.Markdown || loaded.Segments != 3 || !loaded.GeneratedAt.Equal(saved.GeneratedAt) {
		t.Fatalf("loaded = %+v, saved = %+v", loaded, saved)
	}

	dates, err = s.MinutesDates()
	if err != nil {
		t.Fatal(err)
	}
	if len(dates) != 2 || dates[0] != "2026-10-01" || dates[1] != "2026-11-07" {
		t.Fatalf("dates = %v", dates)
	}

	updated, err := s.SaveMinutes("2026-11-07", 5, "# 更新后的纪要\n")
	if err != nil {
		t.Fatal(err)
	}
	loaded, err = s.DayMinutes("2026-11-07")
	if err != nil || loaded.Markdown != updated.Markdown || loaded.Segments != 5 {
		t.Fatalf("loaded = %+v, err = %v", loaded, err)
	}

	if err := s.DeleteMinutes("2026-11-07"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteMinutes("2026-11-07"); err != nil {
		t.Fatalf("delete of missing minutes: %v", err)
	}
	if _, err := s.DayMinutes("2026-11-07"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted minutes error = %v", err)
	}
	dates, err = s.MinutesDates()
	if err != nil || len(dates) != 1 || dates[0] != "2026-10-01" {
		t.Fatalf("dates = %v, err = %v", dates, err)
	}
}

func TestMinutesRejectInvalidInput(t *testing.T) {
	s := openMinutesStore(t)
	for _, date := range []string{"", "2026-02-30", "2026-1-07", "../escape", "2026-11-07/.."} {
		if _, err := s.SaveMinutes(date, 1, "# 纪要"); err == nil {
			t.Fatalf("SaveMinutes(%q) accepted invalid date", date)
		}
		if _, err := s.DayMinutes(date); err == nil {
			t.Fatalf("DayMinutes(%q) accepted invalid date", date)
		}
		if err := s.DeleteMinutes(date); err == nil {
			t.Fatalf("DeleteMinutes(%q) accepted invalid date", date)
		}
	}
	if _, err := s.SaveMinutes("2026-11-07", 1, "  "); err == nil {
		t.Fatal("empty markdown accepted")
	}
	if _, err := os.Stat(filepath.Join(s.Root(), minutesDir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid input created minutes directory: %v", err)
	}
}

func TestMinutesDatesIgnoreForeignFiles(t *testing.T) {
	s := openMinutesStore(t)
	if _, err := s.SaveMinutes("2026-11-07", 1, "# 纪要"); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(s.Root(), minutesDir)
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".minutes-tmp.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "2026-02-30.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "2026-11-08.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	dates, err := s.MinutesDates()
	if err != nil || len(dates) != 1 || dates[0] != "2026-11-07" {
		t.Fatalf("dates = %v, err = %v", dates, err)
	}
}

func TestDayMinutesRejectsCorruptJSON(t *testing.T) {
	s := openMinutesStore(t)
	if _, err := s.SaveMinutes("2026-11-07", 1, "# 纪要"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.Root(), minutesDir, "2026-11-07.json")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DayMinutes("2026-11-07"); err == nil {
		t.Fatal("corrupt minutes JSON accepted")
	}
}

func TestSaveMinutesRenameFailureRemovesTempFile(t *testing.T) {
	s := openMinutesStore(t)
	dir := filepath.Join(s.Root(), minutesDir)
	if err := os.MkdirAll(filepath.Join(dir, "2026-11-07.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveMinutes("2026-11-07", 1, "# 纪要"); err == nil {
		t.Fatal("expected rename failure")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "2026-11-07.json" {
		t.Fatalf("temporary files left after rename failure: %v", entries)
	}
}
