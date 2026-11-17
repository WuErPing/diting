package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDeleteSegmentMovesOnlySelectedAudioToTrash(t *testing.T) {
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
	for i, text := range []string{"删除我", "保留我"} {
		started := base.Add(time.Duration(i) * time.Minute)
		if _, err := segmentStore.Save(Segment{
			ID: started.Format("20060102-150405.000000000"), StartedAt: started,
			EndedAt: started.Add(time.Second), Text: text,
		}, []float32{0.1}); err != nil {
			t.Fatal(err)
		}
	}
	deletedID := base.Format("20060102-150405.000000000")
	result, err := segmentStore.DeleteSegment(deletedID)
	if err != nil {
		t.Fatal(err)
	}
	if result.ID != deletedID || result.TrashPath == "" {
		t.Fatalf("result = %+v", result)
	}
	if _, err := os.Stat(result.TrashPath); err != nil {
		t.Fatalf("trash audio: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, audioDir, "20261106", deletedID+".wav")); !os.IsNotExist(err) {
		t.Fatalf("deleted audio still exists: %v", err)
	}
	segments, err := segmentStore.All()
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) != 1 || segments[0].Text != "保留我" {
		t.Fatalf("remaining = %+v", segments)
	}
	if _, err := segmentStore.DeleteSegment(deletedID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("second delete error = %v", err)
	}
}

func TestDeleteSegmentsRemovesBatchAndTrashesAudio(t *testing.T) {
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
	var ids []string
	for i, text := range []string{"删除一", "保留", "删除二"} {
		started := base.Add(time.Duration(i) * time.Minute)
		id := started.Format("20060102-150405.000000000")
		ids = append(ids, id)
		if _, err := segmentStore.Save(Segment{
			ID: id, StartedAt: started, EndedAt: started.Add(time.Second), Text: text,
		}, []float32{0.1}); err != nil {
			t.Fatal(err)
		}
	}

	results, err := segmentStore.DeleteSegments([]string{ids[0], ids[2], ids[0], "20990101-000000.000000000"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %+v, want 2 entries", results)
	}
	for i, result := range results {
		wantID := []string{ids[0], ids[2]}[i]
		if result.ID != wantID || result.TrashPath == "" {
			t.Fatalf("result = %+v, want ID %s with trash path", result, wantID)
		}
		if _, err := os.Stat(result.TrashPath); err != nil {
			t.Fatalf("trash audio: %v", err)
		}
		if _, err := os.Stat(filepath.Join(root, audioDir, "20261106", wantID+".wav")); !os.IsNotExist(err) {
			t.Fatalf("deleted audio still exists: %v", err)
		}
	}
	segments, err := segmentStore.All()
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) != 1 || segments[0].Text != "保留" {
		t.Fatalf("remaining = %+v", segments)
	}

	results, err = segmentStore.DeleteSegments([]string{ids[0]})
	if err != nil || len(results) != 0 {
		t.Fatalf("repeat delete = %+v, %v; want empty result", results, err)
	}
}
