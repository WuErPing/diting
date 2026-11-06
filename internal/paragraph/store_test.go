package paragraph

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func testParagraph(id string, start time.Time, segments ...string) Paragraph {
	return Paragraph{
		ID:         id,
		StartedAt:  start,
		EndedAt:    start.Add(time.Minute),
		Text:       "text-" + id,
		SegmentIDs: segments,
		Source:     SourceLLM,
		UpdatedAt:  start,
	}
}

func paragraphIDs(paragraphs []Paragraph) []string {
	ids := make([]string, len(paragraphs))
	for i, paragraph := range paragraphs {
		ids[i] = paragraph.ID
	}
	return ids
}

func TestStoreOpenMissingFile(t *testing.T) {
	root := t.TempDir()
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(store.All()) != 0 {
		t.Fatalf("paragraphs = %v", store.All())
	}
	if _, err := os.Stat(filepath.Join(root, FileName)); !os.IsNotExist(err) {
		t.Fatalf("paragraph file should not exist yet: %v", err)
	}
}

func TestStoreSaveManyUpsertsAndSorts(t *testing.T) {
	store := openTestStore(t)
	later := testParagraph("b", testBase.Add(time.Minute), "b")
	earlier := testParagraph("a", testBase, "a")
	if err := store.SaveMany([]Paragraph{later, earlier}); err != nil {
		t.Fatal(err)
	}
	if ids := paragraphIDs(store.All()); len(ids) != 2 || ids[0] != "a" || ids[1] != "b" {
		t.Fatalf("ids = %v", ids)
	}

	updated := testParagraph("a", testBase, "a")
	updated.Text = "revised"
	if err := store.SaveMany([]Paragraph{updated}); err != nil {
		t.Fatal(err)
	}
	all := store.All()
	if len(all) != 2 {
		t.Fatalf("duplicate upsert: %d", len(all))
	}
	if all[0].ID != "a" || all[0].Text != "revised" {
		t.Fatalf("upsert result = %+v", all[0])
	}
}

func TestStoreSaveManyReplacesOverlappingSegments(t *testing.T) {
	store := openTestStore(t)
	if err := store.SaveMany([]Paragraph{
		testParagraph("a", testBase, "a1", "a2"),
		testParagraph("c", testBase.Add(5*time.Minute), "c1"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveMany([]Paragraph{
		testParagraph("a2", testBase.Add(30*time.Second), "a2", "a3"),
	}); err != nil {
		t.Fatal(err)
	}
	all := store.All()
	if ids := paragraphIDs(all); len(ids) != 2 || ids[0] != "a2" || ids[1] != "c" {
		t.Fatalf("ids = %v", ids)
	}
}

func TestStoreOpenSkipsBadLines(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, FileName)
	content := strings.Join([]string{
		`{"id":"a","startedAt":"2026-11-07T09:00:00Z","segmentIds":["s1"],"source":"llm"}`,
		``,
		`not json`,
		`{"id":"b","startedAt":"2026-11-07T09:01:00Z","segmentIds":["s2"],"source":"single"}`,
		`{"id":"c","startedAt":"2026-11-07T09:02:00Z","segmentIds":["s1"],"source":"llm"}`,
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if ids := paragraphIDs(store.All()); len(ids) != 2 || ids[0] != "a" || ids[1] != "b" {
		t.Fatalf("ids = %v (overlap with s1 should drop c)", ids)
	}
}

func TestStoreAtomicRewriteLeavesNoTempFiles(t *testing.T) {
	root := t.TempDir()
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveMany([]Paragraph{testParagraph("a", testBase, "s1")}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != FileName {
			t.Fatalf("unexpected file %q", entry.Name())
		}
	}
}

func TestStoreCoverageAndReset(t *testing.T) {
	root := t.TempDir()
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	first := testParagraph("a", testBase, "a1", "a2")
	second := testParagraph("b", testBase.Add(time.Minute), "b1")
	second.Source = SourceFallback
	if err := store.SaveMany([]Paragraph{first, second}); err != nil {
		t.Fatal(err)
	}
	coverage := store.Coverage()
	want := map[string]string{"a1": SourceLLM, "a2": SourceLLM, "b1": SourceFallback}
	for id, source := range want {
		if coverage[id] != source {
			t.Fatalf("coverage[%s] = %q, want %q", id, coverage[id], source)
		}
	}

	if err := store.Reset(); err != nil {
		t.Fatal(err)
	}
	if len(store.All()) != 0 || len(store.Coverage()) != 0 {
		t.Fatalf("store not empty after reset: %v", store.All())
	}
	data, err := os.ReadFile(filepath.Join(root, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 {
		t.Fatalf("file not truncated: %q", data)
	}
}

func TestStoreReplaceDropsEmptyAndOverlapping(t *testing.T) {
	store := openTestStore(t)
	empty := testParagraph("", testBase, "s1")
	noSegments := testParagraph("b", testBase.Add(time.Minute))
	first := testParagraph("a", testBase, "x")
	overlap := testParagraph("c", testBase.Add(2*time.Minute), "x")
	last := testParagraph("d", testBase.Add(3*time.Minute), "y")

	if err := store.Replace([]Paragraph{empty, noSegments, first, overlap, last}); err != nil {
		t.Fatal(err)
	}
	if ids := paragraphIDs(store.All()); len(ids) != 2 || ids[0] != "a" || ids[1] != "d" {
		t.Fatalf("ids = %v", ids)
	}
}
