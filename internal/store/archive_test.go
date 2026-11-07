package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveCompressesToM4A(t *testing.T) {
	t.Setenv("DITING_AUDIO_FORMAT", "m4a")
	original := encodeAudio
	encodeAudio = func(_, output string) error {
		// afconvert creates world-readable files; Save must tighten the mode.
		return os.WriteFile(output, []byte("compressed"), 0o644)
	}
	defer func() { encodeAudio = original }()

	root := t.TempDir()
	segmentStore, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer segmentStore.Close()

	started := time.Date(2026, 11, 7, 10, 0, 0, 0, time.Local)
	saved, err := segmentStore.Save(Segment{
		ID:        "20261107-100000.000000000",
		StartedAt: started,
		EndedAt:   started.Add(2 * time.Second),
		Text:      "你好",
	}, []float32{0.1, -0.1})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Ext(saved.AudioPath) != audioExtM4A {
		t.Fatalf("audioPath = %q, want .m4a", saved.AudioPath)
	}
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(saved.AudioPath)))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("audio mode = %o, want 600", info.Mode().Perm())
	}
	if wav := filepath.Join(root, audioDir, "20261107", "20261107-100000.000000000.wav"); !os.IsNotExist(mustStat(t, wav)) {
		t.Fatalf("temporary WAV still exists: %s", wav)
	}
}

func TestSaveFallsBackToWAV(t *testing.T) {
	t.Setenv("DITING_AUDIO_FORMAT", "m4a")
	original := encodeAudio
	encodeAudio = func(_, _ string) error { return errors.New("encoder unavailable") }
	defer func() { encodeAudio = original }()

	root := t.TempDir()
	segmentStore, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer segmentStore.Close()

	started := time.Date(2026, 11, 7, 10, 0, 0, 0, time.Local)
	saved, err := segmentStore.Save(Segment{
		ID:        "20261107-100001.000000000",
		StartedAt: started,
		EndedAt:   started.Add(time.Second),
		Text:      "回退",
	}, []float32{0.2})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Ext(saved.AudioPath) != audioExtWAV {
		t.Fatalf("audioPath = %q, want .wav", saved.AudioPath)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(saved.AudioPath))); err != nil {
		t.Fatal(err)
	}
}

func TestDaysAggregatesTranscriptsAndOrphans(t *testing.T) {
	t.Setenv("DITING_AUDIO_FORMAT", "wav")
	root := t.TempDir()
	segmentStore, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer segmentStore.Close()

	base := time.Date(2026, 11, 6, 14, 0, 0, 0, time.Local)
	for i := 0; i < 2; i++ {
		started := base.Add(time.Duration(i) * time.Minute)
		if _, err := segmentStore.Save(Segment{
			ID:        started.Format("20060102-150405.000000000"),
			StartedAt: started,
			EndedAt:   started.Add(time.Second),
			Text:      "你好",
		}, []float32{0.1}); err != nil {
			t.Fatal(err)
		}
	}
	nextDay := base.Add(24 * time.Hour)
	if _, err := segmentStore.Save(Segment{
		ID:        nextDay.Format("20060102-150405.000000000"),
		StartedAt: nextDay,
		EndedAt:   nextDay.Add(time.Second),
		Text:      "第二天",
	}, []float32{0.1}); err != nil {
		t.Fatal(err)
	}

	orphanDir := filepath.Join(root, audioDir, "20261105")
	if err := os.MkdirAll(orphanDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orphanDir, "orphan.wav"), []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}

	days, err := segmentStore.Days()
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 3 {
		t.Fatalf("days = %+v", days)
	}
	if days[0].Date != "2026-11-05" || days[0].Segments != 0 || days[0].AudioFiles != 1 || days[0].AudioBytes != 3 {
		t.Fatalf("orphan day = %+v", days[0])
	}
	if days[1].Date != "2026-11-06" || days[1].Segments != 2 || days[1].Chars != 4 || days[1].AudioFiles != 2 {
		t.Fatalf("first day = %+v", days[1])
	}
	if days[2].Date != "2026-11-07" || days[2].Segments != 1 {
		t.Fatalf("second day = %+v", days[2])
	}
}

func TestDeleteDayMovesAudioToTrash(t *testing.T) {
	t.Setenv("DITING_AUDIO_FORMAT", "wav")
	trash := t.TempDir()
	t.Setenv("DITING_TRASH_DIR", trash)

	root := t.TempDir()
	segmentStore, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer segmentStore.Close()

	base := time.Date(2026, 11, 6, 14, 0, 0, 0, time.Local)
	for _, started := range []time.Time{base, base.Add(time.Hour), base.Add(24 * time.Hour)} {
		if _, err := segmentStore.Save(Segment{
			ID:        started.Format("20060102-150405.000000000"),
			StartedAt: started,
			EndedAt:   started.Add(time.Second),
			Text:      "文本",
		}, []float32{0.1}); err != nil {
			t.Fatal(err)
		}
	}

	result, err := segmentStore.DeleteDay("2026-11-06")
	if err != nil {
		t.Fatal(err)
	}
	if result.Segments != 2 || len(result.SegmentIDs) != 2 || result.AudioFiles != 2 || result.AudioBytes <= 0 {
		t.Fatalf("delete result = %+v", result)
	}
	if result.TrashPath == "" {
		t.Fatal("missing trash path")
	}
	if _, err := os.Stat(result.TrashPath); err != nil {
		t.Fatalf("trash destination: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, audioDir, "20261106")); !os.IsNotExist(err) {
		t.Fatalf("audio directory still exists, err = %v", err)
	}

	segments, err := segmentStore.All()
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) != 1 || segments[0].StartedAt.Day() != 7 {
		t.Fatalf("remaining = %+v", segments)
	}

	backup, err := readTranscripts(filepath.Join(root, transcriptBackup))
	if err != nil {
		t.Fatal(err)
	}
	if len(backup) != 3 {
		t.Fatalf("backup = %d, want 3", len(backup))
	}

	if _, err := segmentStore.DeleteDay("2026-13-01"); err == nil {
		t.Fatal("expected invalid date error")
	}
}

func TestAudioFindsSegmentsOutsideHistoryWindow(t *testing.T) {
	root := t.TempDir()
	segmentStore, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer segmentStore.Close()

	segments := make([]Segment, 0, historyLimit+1)
	base := time.Date(2026, 11, 1, 0, 0, 0, 0, time.Local)
	for i := 0; i <= historyLimit; i++ {
		segments = append(segments, Segment{
			ID:        base.Add(time.Duration(i) * time.Second).Format("20060102-150405.000000000"),
			StartedAt: base.Add(time.Duration(i) * time.Second),
			EndedAt:   base.Add(time.Duration(i)*time.Second + time.Second),
			AudioPath: "audio/20261101/" + base.Add(time.Duration(i)*time.Second).Format("20060102-150405.000000000") + ".wav",
		})
	}
	if err := segmentStore.Rewrite(segments); err != nil {
		t.Fatal(err)
	}
	path, err := segmentStore.Audio(segments[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(root, filepath.FromSlash(segments[0].AudioPath)) {
		t.Fatalf("path = %q", path)
	}
}

func mustStat(t *testing.T, path string) error {
	t.Helper()
	_, err := os.Stat(path)
	return err
}
