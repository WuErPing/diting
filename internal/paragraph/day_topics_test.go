package paragraph

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"diting/internal/store"
)

func TestWorkerClassifyDayUsesOnlySelectedDayAndCurrentModel(t *testing.T) {
	start := time.Date(2026, 11, 23, 22, 5, 0, 0, time.Local)
	previous := testSegment("previous", "说话人1", "前一天", start.AddDate(0, 0, -1), start.AddDate(0, 0, -1).Add(time.Second))
	first := testSegment("first", "说话人1", "英伟达股价上涨。", start, start.Add(time.Second))
	second := testSegment("second", "说话人1", "继续讨论 CEO 回归。", start.Add(2*time.Second), start.Add(3*time.Second))
	next := testSegment("next", "说话人1", "后一天", start.AddDate(0, 0, 1), start.AddDate(0, 0, 1).Add(time.Second))
	grouper := &fakeGrouper{fn: func(_ int, _ context.Context, texts []string) ([]Group, error) {
		groups := make([]Group, len(texts))
		for i := range texts {
			groups[i] = Group{Start: i + 1, End: i + 1, Category: "英伟达回归", Title: "讨论侧面", Summary: "保留讨论内容。"}
		}
		return groups, nil
	}}
	paragraphStore := openTestStore(t)
	if err := paragraphStore.SaveMany([]Paragraph{testParagraph("previous", previous.StartedAt, "previous")}); err != nil {
		t.Fatal(err)
	}
	worker := NewWorker(WorkerOptions{
		Store: paragraphStore, Segments: fakeSegments{previous, first, second, next}, Grouper: grouper,
		Model: "initial-model", Clock: newFakeClock(start), Logf: quietLogf,
	})
	worker.SetModel("chosen-model")

	result, err := worker.ClassifyDay(context.Background(), "2026-11-23")
	if err != nil {
		t.Fatal(err)
	}
	if result.Date != "2026-11-23" || result.Segments != 2 || result.Runs != 1 || result.LLM != 1 || result.Paragraphs != 2 {
		t.Fatalf("result = %+v", result)
	}
	calls := grouper.snapshot()
	if len(calls) != 1 || len(calls[0]) != 2 || strings.Contains(strings.Join(calls[0], ""), "一天") {
		t.Fatalf("model inputs = %+v", calls)
	}
	for i, segment := range []store.Segment{first, second} {
		if !strings.Contains(calls[0][i], segment.Text) {
			t.Fatalf("model input %d = %q, want segment text %q", i, calls[0][i], segment.Text)
		}
	}
	all := paragraphStore.All()
	if len(all) != 3 || all[0].ID != "previous" {
		t.Fatalf("stored paragraphs = %+v", all)
	}
	for _, item := range all[1:] {
		if item.Model != "chosen-model" || item.Category != "英伟达回归" {
			t.Fatalf("paragraph = %+v", item)
		}
	}
}

func TestWorkerClassifyDayReplacesOnlySelectedDay(t *testing.T) {
	start := time.Date(2026, 11, 23, 22, 5, 0, 0, time.Local)
	previous := testSegment("previous", "说话人1", "前一天", start.AddDate(0, 0, -1), start.AddDate(0, 0, -1).Add(time.Second))
	current := testSegment("current", "说话人1", "保留当天内容。", start, start.Add(time.Second))
	grouper := &fakeGrouper{fn: func(_ int, _ context.Context, texts []string) ([]Group, error) {
		return []Group{{Start: 1, End: 1, Category: "当天主题", Title: "保留", Summary: "当天摘要。"}}, nil
	}}
	paragraphStore := openTestStore(t)
	if err := paragraphStore.SaveMany([]Paragraph{
		testParagraph("previous", previous.StartedAt, "previous"),
		testParagraph("stale", start.Add(-time.Hour), "deleted"),
	}); err != nil {
		t.Fatal(err)
	}
	worker := NewWorker(WorkerOptions{
		Store: paragraphStore, Segments: fakeSegments{previous, current}, Grouper: grouper,
		Model: "chosen-model", Clock: newFakeClock(start), Logf: quietLogf,
	})

	if _, err := worker.ClassifyDay(context.Background(), "2026-11-23"); err != nil {
		t.Fatal(err)
	}
	all := paragraphStore.All()
	if len(all) != 2 || all[0].ID != "previous" || all[0].Text != "text-previous" || all[1].ID != "current" {
		t.Fatalf("stored paragraphs = %+v", all)
	}
}

func TestWorkerClassifyDayWriteFailureLeavesStoreAndEventsUnchanged(t *testing.T) {
	start := time.Date(2026, 11, 23, 22, 5, 0, 0, time.Local)
	current := testSegment("current", "说话人1", "新内容。", start, start.Add(time.Second))
	paragraphStore := openTestStore(t)
	if err := paragraphStore.SaveMany([]Paragraph{testParagraph("existing", start, "current")}); err != nil {
		t.Fatal(err)
	}
	before := paragraphStore.All()
	grouper := &fakeGrouper{fn: func(_ int, _ context.Context, texts []string) ([]Group, error) {
		return []Group{{Start: 1, End: 1, Category: "新主题"}}, nil
	}}
	worker := NewWorker(WorkerOptions{
		Store: paragraphStore, Segments: fakeSegments{current}, Grouper: grouper,
		Model: "chosen-model", Clock: newFakeClock(start), Logf: quietLogf,
	})
	events := 0
	worker.SetOnParagraph(func(Paragraph) { events++ })
	if err := os.RemoveAll(paragraphStore.root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paragraphStore.root, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := worker.ClassifyDay(context.Background(), "2026-11-23"); err == nil {
		t.Fatal("expected write failure")
	}
	if after := paragraphStore.All(); !reflect.DeepEqual(after, before) {
		t.Fatalf("stored paragraphs changed after failure: before = %+v, after = %+v", before, after)
	}
	if events != 0 {
		t.Fatalf("events = %d, want 0", events)
	}
}

func TestWorkerClassifyDayRequiresTranscriptsAndGrouper(t *testing.T) {
	worker := NewWorker(WorkerOptions{Store: openTestStore(t), Segments: fakeSegments{}, Logf: quietLogf})
	if _, err := worker.ClassifyDay(context.Background(), "2026-11-23"); !errors.Is(err, ErrDayTopicsUnavailable) {
		t.Fatalf("unavailable error = %v", err)
	}
	worker = NewWorker(WorkerOptions{
		Store: openTestStore(t), Segments: fakeSegments{}, Grouper: &fakeGrouper{}, Logf: quietLogf,
	})
	if _, err := worker.ClassifyDay(context.Background(), "2026-11-23"); !errors.Is(err, ErrDayNotFound) {
		t.Fatalf("not found error = %v", err)
	}
}
