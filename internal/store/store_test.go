package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStats(t *testing.T) {
	root := t.TempDir()
	segmentStore, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer segmentStore.Close()

	empty, err := segmentStore.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if empty != (Stats{}) {
		t.Fatalf("empty stats = %+v", empty)
	}

	base := time.Date(2026, 11, 6, 14, 0, 0, 0, time.Local)
	for i, text := range []string{"你好", " world "} {
		started := base.Add(time.Duration(i) * time.Minute)
		if _, err := segmentStore.Save(Segment{
			ID:        started.Format("20060102-150405.000000000"),
			StartedAt: started,
			EndedAt:   started.Add(2 * time.Second),
			Text:      text,
		}, []float32{0.1, -0.2, 0.3}); err != nil {
			t.Fatal(err)
		}
	}

	stats, err := segmentStore.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.AudioFiles != 2 || stats.Segments != 2 || stats.TranscriptChars != 7 {
		t.Fatalf("stats = %+v", stats)
	}
	if stats.AudioBytes <= 0 {
		t.Fatalf("audio bytes = %d", stats.AudioBytes)
	}
}

func TestAllAndRewrite(t *testing.T) {
	root := t.TempDir()
	segmentStore, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer segmentStore.Close()

	base := time.Date(2026, 11, 6, 14, 0, 0, 0, time.Local)
	for i := 0; i < 3; i++ {
		started := base.Add(time.Duration(i) * time.Minute)
		if _, err := segmentStore.Save(Segment{
			ID:        started.Format("20060102-150405.000000000"),
			StartedAt: started,
			EndedAt:   started.Add(2 * time.Second),
			Text:      "旧文本",
		}, []float32{0.1, -0.2}); err != nil {
			t.Fatal(err)
		}
	}

	segments, err := segmentStore.All()
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) != 3 {
		t.Fatalf("all = %d, want 3", len(segments))
	}

	for i := range segments {
		segments[i].Text = "新文本"
		segments[i].Speaker = "说话人1"
	}
	if err := segmentStore.Rewrite(segments); err != nil {
		t.Fatal(err)
	}

	rewritten, err := readTranscripts(filepath.Join(root, transcriptFile))
	if err != nil {
		t.Fatal(err)
	}
	if len(rewritten) != 3 {
		t.Fatalf("rewritten = %d, want 3", len(rewritten))
	}
	for i, segment := range rewritten {
		if segment.Text != "新文本" || segment.Speaker != "说话人1" {
			t.Fatalf("segment %d = %+v", i, segment)
		}
		if !segment.StartedAt.Equal(segments[i].StartedAt) {
			t.Fatalf("startedAt changed: %v != %v", segment.StartedAt, segments[i].StartedAt)
		}
		if segment.DurationMS != 2000 {
			t.Fatalf("durationMs = %d, want 2000", segment.DurationMS)
		}
		if segment.AudioPath != segments[i].AudioPath {
			t.Fatalf("audioPath changed: %q != %q", segment.AudioPath, segments[i].AudioPath)
		}
	}

	backup, err := readTranscripts(filepath.Join(root, transcriptBackup))
	if err != nil {
		t.Fatal(err)
	}
	if len(backup) != 3 || backup[0].Text != "旧文本" {
		t.Fatalf("backup = %+v", backup)
	}

	temps, err := filepath.Glob(filepath.Join(root, ".transcripts-*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(temps) != 0 {
		t.Fatalf("temp files left behind: %v", temps)
	}
}

func TestOpenLockContention(t *testing.T) {
	root := t.TempDir()
	first, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := Open(root); err == nil {
		t.Fatal("expected second Open to fail while the first is held")
	}

	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Open(root)
	if err != nil {
		t.Fatalf("Open after release: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestAllSkipsMalformedLines(t *testing.T) {
	root := t.TempDir()
	content := `{"id":"a","startedAt":"2026-11-06T14:00:00+08:00","text":"a"}
not json
{"id":"b","startedAt":"2026-11-06T14:01:00+08:00","text":"b"}
`
	if err := os.WriteFile(filepath.Join(root, transcriptFile), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	segmentStore, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer segmentStore.Close()

	segments, err := segmentStore.All()
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) != 2 {
		t.Fatalf("all = %d, want 2", len(segments))
	}
}

func TestRewriteWithoutExistingLog(t *testing.T) {
	root := t.TempDir()
	segmentStore, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer segmentStore.Close()

	segments := []Segment{{
		ID:         "20261106-140000.000000000",
		StartedAt:  time.Date(2026, 11, 6, 14, 0, 0, 0, time.Local),
		EndedAt:    time.Date(2026, 11, 6, 14, 0, 2, 0, time.Local),
		DurationMS: 2000,
		Text:       "你好",
		Speaker:    "说话人1",
		AudioPath:  "audio/20261106/20261106-140000.000000000.wav",
	}}
	if err := segmentStore.Rewrite(segments); err != nil {
		t.Fatal(err)
	}

	rewritten, err := readTranscripts(filepath.Join(root, transcriptFile))
	if err != nil {
		t.Fatal(err)
	}
	if len(rewritten) != 1 || rewritten[0].Speaker != "说话人1" {
		t.Fatalf("rewritten = %+v", rewritten)
	}
	if _, err := os.Stat(filepath.Join(root, transcriptBackup)); !os.IsNotExist(err) {
		t.Fatalf("backup should not exist for a missing log, err = %v", err)
	}
}
