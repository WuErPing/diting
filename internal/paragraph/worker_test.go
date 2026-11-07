package paragraph

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"diting/internal/store"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(now time.Time) *fakeClock {
	return &fakeClock{now: now}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type fakeGrouper struct {
	mu    sync.Mutex
	calls [][]string
	fn    func(call int, ctx context.Context, texts []string) ([]Group, error)
}

func (g *fakeGrouper) Group(ctx context.Context, texts []string) ([]Group, error) {
	g.mu.Lock()
	g.calls = append(g.calls, append([]string(nil), texts...))
	call := len(g.calls)
	fn := g.fn
	g.mu.Unlock()
	if fn != nil {
		return fn(call, ctx, texts)
	}
	return []Group{{Start: 1, End: len(texts)}}, nil
}

func (g *fakeGrouper) count() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.calls)
}

func (g *fakeGrouper) snapshot() [][]string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([][]string(nil), g.calls...)
}

type fakeSegments []store.Segment

func (s fakeSegments) All() ([]store.Segment, error) {
	return append([]store.Segment(nil), s...), nil
}

func quietLogf(string, ...any) {}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

func newTestWorker(t *testing.T, clock *fakeClock, paragraphStore *Store, segments SegmentSource, grouper Grouper) *Worker {
	t.Helper()
	return NewWorker(WorkerOptions{
		Store:    paragraphStore,
		Segments: segments,
		Grouper:  grouper,
		Model:    "test-model",
		Clock:    clock,
		Logf:     quietLogf,
	})
}

func TestWorkerMergesOnEverySegment(t *testing.T) {
	clock := newFakeClock(testBase)
	paragraphStore := openTestStore(t)
	grouper := &fakeGrouper{}
	worker := newTestWorker(t, clock, paragraphStore, nil, grouper)
	events := make(chan Paragraph, 8)
	worker.SetOnParagraph(func(p Paragraph) { events <- p })
	worker.Start(context.Background())
	defer worker.Close()

	worker.Notify(testSegment("a", "说话人1", "你好", testBase, testBase.Add(time.Second)))
	waitFor(t, "first paragraph", func() bool { return len(paragraphStore.All()) == 1 })
	first := paragraphStore.All()[0]
	if first.ID != "a" || first.Source != SourceSingle {
		t.Fatalf("first paragraph = %+v", first)
	}

	worker.Notify(testSegment("b", "说话人1", "世界", testBase.Add(2*time.Second), testBase.Add(3*time.Second)))
	waitFor(t, "merged paragraph", func() bool {
		all := paragraphStore.All()
		return len(all) == 1 && all[0].Source == SourceLLM && len(all[0].SegmentIDs) == 2
	})

	paragraph := paragraphStore.All()[0]
	if paragraph.Text != "你好世界" || paragraph.Model != "test-model" {
		t.Fatalf("paragraph = %+v", paragraph)
	}
	if !reflect.DeepEqual(paragraph.SegmentIDs, []string{"a", "b"}) {
		t.Fatalf("segment ids = %v", paragraph.SegmentIDs)
	}
	if grouper.count() != 1 {
		t.Fatalf("model calls = %d", grouper.count())
	}
	select {
	case <-events:
	case <-time.After(time.Second):
		t.Fatal("paragraph event not fired")
	}
}

func TestWorkerMergesLongPausesWithoutSegment(t *testing.T) {
	clock := newFakeClock(testBase)
	paragraphStore := openTestStore(t)
	grouper := &fakeGrouper{}
	worker := newTestWorker(t, clock, paragraphStore, nil, grouper)
	worker.Start(context.Background())
	defer worker.Close()

	worker.Notify(testSegment("a", "说话人1", "一", testBase, testBase.Add(time.Second)))
	waitFor(t, "first paragraph", func() bool { return len(paragraphStore.All()) == 1 })

	later := testBase.Add(time.Hour)
	worker.Notify(testSegment("b", "说话人1", "二", later, later.Add(time.Second)))
	waitFor(t, "merged across pause", func() bool {
		all := paragraphStore.All()
		return len(all) == 1 && len(all[0].SegmentIDs) == 2
	})

	// No timer should fire on its own: only the new segment triggers merging.
	if grouper.count() != 1 {
		t.Fatalf("model calls = %d", grouper.count())
	}
}

func TestWorkerMergesOnSpeakerChange(t *testing.T) {
	clock := newFakeClock(testBase)
	paragraphStore := openTestStore(t)
	grouper := &fakeGrouper{}
	worker := newTestWorker(t, clock, paragraphStore, nil, grouper)
	worker.Start(context.Background())
	defer worker.Close()

	worker.Notify(testSegment("a", "说话人1", "一", testBase, testBase.Add(time.Second)))
	waitFor(t, "first paragraph", func() bool { return len(paragraphStore.All()) == 1 })
	worker.Notify(testSegment("b", "说话人2", "二", testBase.Add(2*time.Second), testBase.Add(3*time.Second)))

	waitFor(t, "second paragraph", func() bool { return len(paragraphStore.All()) == 2 })
	all := paragraphStore.All()
	if all[0].ID != "a" || all[0].Source != SourceSingle || all[0].Text != "一" {
		t.Fatalf("first paragraph = %+v", all[0])
	}
	if all[1].ID != "b" || all[1].Source != SourceSingle || all[1].Text != "二" {
		t.Fatalf("second paragraph = %+v", all[1])
	}
	if grouper.count() != 0 {
		t.Fatalf("single segments should not call model")
	}
}

func TestWorkerCapsTriggerMerge(t *testing.T) {
	cases := []struct {
		name string
		opts WorkerOptions
	}{
		{"segment cap", WorkerOptions{MaxSegments: 2}},
		{"char cap", WorkerOptions{MaxChars: 4}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clock := newFakeClock(testBase)
			paragraphStore := openTestStore(t)
			grouper := &fakeGrouper{}
			opts := tc.opts
			opts.Store = paragraphStore
			opts.Grouper = grouper
			opts.Clock = clock
			opts.Logf = quietLogf
			worker := NewWorker(opts)
			worker.Start(context.Background())
			defer worker.Close()

			worker.Notify(testSegment("a", "说话人1", "你好", testBase, testBase.Add(time.Second)))
			waitFor(t, "first paragraph", func() bool { return len(paragraphStore.All()) == 1 })
			worker.Notify(testSegment("b", "说话人1", "世界", testBase.Add(2*time.Second), testBase.Add(3*time.Second)))
			waitFor(t, "capped merge", func() bool {
				all := paragraphStore.All()
				return len(all) == 1 && len(all[0].SegmentIDs) == 2
			})

			worker.Notify(testSegment("c", "说话人1", "再见", testBase.Add(4*time.Second), testBase.Add(5*time.Second)))
			waitFor(t, "closed by cap", func() bool { return len(paragraphStore.All()) == 2 })
			all := paragraphStore.All()
			if all[0].Text != "你好世界" || !reflect.DeepEqual(all[0].SegmentIDs, []string{"a", "b"}) {
				t.Fatalf("first paragraph = %+v", all[0])
			}
			if all[1].ID != "c" || all[1].Source != SourceSingle {
				t.Fatalf("second paragraph = %+v", all[1])
			}
			if grouper.count() != 1 {
				t.Fatalf("model calls = %d", grouper.count())
			}
		})
	}
}

func TestWorkerFallsBackAndCoolsDown(t *testing.T) {
	clock := newFakeClock(testBase)
	paragraphStore := openTestStore(t)
	grouper := &fakeGrouper{fn: func(call int, _ context.Context, texts []string) ([]Group, error) {
		if call == 1 {
			return nil, errors.New("model down")
		}
		return []Group{{Start: 1, End: len(texts)}}, nil
	}}
	worker := newTestWorker(t, clock, paragraphStore, nil, grouper)
	worker.Start(context.Background())
	defer worker.Close()

	worker.Notify(testSegment("a1", "说话人1", "一", testBase, testBase.Add(time.Second)))
	waitFor(t, "single a1", func() bool { return len(paragraphStore.All()) == 1 })
	worker.Notify(testSegment("a2", "说话人1", "二", testBase.Add(2*time.Second), testBase.Add(3*time.Second)))
	waitFor(t, "a fallback", func() bool {
		all := paragraphStore.All()
		return len(all) == 1 && all[0].Source == SourceFallback && len(all[0].SegmentIDs) == 2
	})

	worker.Notify(testSegment("b1", "说话人2", "三", testBase.Add(4*time.Second), testBase.Add(5*time.Second)))
	waitFor(t, "single b1", func() bool { return len(paragraphStore.All()) == 2 })
	worker.Notify(testSegment("b2", "说话人2", "四", testBase.Add(6*time.Second), testBase.Add(7*time.Second)))
	waitFor(t, "b fallback", func() bool {
		all := paragraphStore.All()
		return len(all) == 2 && all[1].Source == SourceFallback && len(all[1].SegmentIDs) == 2
	})

	if grouper.count() != 1 {
		t.Fatalf("model should be skipped while cooling down, calls = %d", grouper.count())
	}

	clock.Advance(2*time.Minute + time.Second)
	now := clock.Now()
	worker.Notify(testSegment("d1", "说话人3", "五", now, now.Add(time.Second)))
	waitFor(t, "retried b run", func() bool {
		all := paragraphStore.All()
		return len(all) == 3 && all[1].Source == SourceLLM
	})
	worker.Notify(testSegment("d2", "说话人3", "六", now.Add(2*time.Second), now.Add(3*time.Second)))

	waitFor(t, "post-cooldown llm", func() bool {
		all := paragraphStore.All()
		return len(all) == 3 && all[2].Source == SourceLLM && len(all[2].SegmentIDs) == 2
	})
	if grouper.count() != 3 {
		t.Fatalf("model calls = %d", grouper.count())
	}
}

func TestWorkerInvalidGroupingDoesNotCoolDown(t *testing.T) {
	clock := newFakeClock(testBase)
	paragraphStore := openTestStore(t)
	grouper := &fakeGrouper{fn: func(call int, _ context.Context, texts []string) ([]Group, error) {
		if call == 1 {
			return nil, fmt.Errorf("%w: bad offsets", ErrInvalidGrouping)
		}
		return []Group{{Start: 1, End: len(texts)}}, nil
	}}
	worker := newTestWorker(t, clock, paragraphStore, nil, grouper)
	worker.Start(context.Background())
	defer worker.Close()

	worker.Notify(testSegment("a1", "说话人1", "一", testBase, testBase.Add(time.Second)))
	waitFor(t, "single a1", func() bool { return len(paragraphStore.All()) == 1 })
	worker.Notify(testSegment("a2", "说话人1", "二", testBase.Add(2*time.Second), testBase.Add(3*time.Second)))
	waitFor(t, "a fallback", func() bool {
		all := paragraphStore.All()
		return len(all) == 1 && all[0].Source == SourceFallback
	})

	worker.Notify(testSegment("b1", "说话人2", "三", testBase.Add(4*time.Second), testBase.Add(5*time.Second)))
	waitFor(t, "fallback retried without cooldown", func() bool {
		all := paragraphStore.All()
		return len(all) == 2 && all[0].Source == SourceLLM && grouper.count() == 2
	})
	worker.Notify(testSegment("b2", "说话人2", "四", testBase.Add(6*time.Second), testBase.Add(7*time.Second)))

	waitFor(t, "second llm paragraph", func() bool {
		all := paragraphStore.All()
		return len(all) == 2 && all[1].Source == SourceLLM
	})
	if grouper.count() != 3 {
		t.Fatalf("model calls = %d", grouper.count())
	}
}

func TestWorkerRepairsGappedGrouping(t *testing.T) {
	clock := newFakeClock(testBase)
	paragraphStore := openTestStore(t)
	grouper := &fakeGrouper{fn: func(_ int, _ context.Context, texts []string) ([]Group, error) {
		if len(texts) == 3 {
			return []Group{{Start: 1, End: 1}, {Start: 3, End: 3}}, nil
		}
		return []Group{{Start: 1, End: len(texts)}}, nil
	}}
	worker := newTestWorker(t, clock, paragraphStore, nil, grouper)
	worker.Start(context.Background())
	defer worker.Close()

	worker.Notify(testSegment("a", "说话人1", "一", testBase, testBase.Add(time.Second)))
	waitFor(t, "single a", func() bool { return len(paragraphStore.All()) == 1 })
	worker.Notify(testSegment("b", "说话人1", "二", testBase.Add(2*time.Second), testBase.Add(3*time.Second)))
	worker.Notify(testSegment("c", "说话人1", "三", testBase.Add(4*time.Second), testBase.Add(5*time.Second)))

	waitFor(t, "repaired paragraphs", func() bool {
		all := paragraphStore.All()
		return len(all) == 2 && all[0].Source == SourceLLM && all[1].Source == SourceLLM
	})
	all := paragraphStore.All()
	if !reflect.DeepEqual(all[0].SegmentIDs, []string{"a"}) {
		t.Fatalf("first paragraph = %+v", all[0])
	}
	if !reflect.DeepEqual(all[1].SegmentIDs, []string{"b", "c"}) || all[1].Text != "二三" {
		t.Fatalf("second paragraph = %+v", all[1])
	}
}

func TestWorkerBackfillSkipsCoveredAndRetriesFallback(t *testing.T) {
	s1 := testSegment("s1", "说话人1", "一", testBase, testBase.Add(time.Second))
	s2 := testSegment("s2", "说话人1", "二", testBase.Add(2*time.Second), testBase.Add(3*time.Second))
	base2 := testBase.Add(5 * time.Minute)
	s3 := testSegment("s3", "说话人2", "三", base2, base2.Add(time.Second))
	s4 := testSegment("s4", "说话人2", "四", base2.Add(2*time.Second), base2.Add(3*time.Second))

	paragraphStore := openTestStore(t)
	if err := paragraphStore.SaveMany([]Paragraph{
		testParagraph("s1", s1.StartedAt, "s1", "s2"),
		func() Paragraph {
			p := testParagraph("s3", s3.StartedAt, "s3", "s4")
			p.Source = SourceFallback
			return p
		}(),
	}); err != nil {
		t.Fatal(err)
	}

	clock := newFakeClock(s4.EndedAt.Add(5 * time.Minute))
	grouper := &fakeGrouper{}
	worker := newTestWorker(t, clock, paragraphStore, fakeSegments{s1, s2, s3, s4}, grouper)
	worker.Start(context.Background())
	defer worker.Close()

	waitFor(t, "retried fallback", func() bool {
		all := paragraphStore.All()
		return len(all) == 2 && all[1].Source == SourceLLM
	})
	if grouper.count() != 1 {
		t.Fatalf("model calls = %d", grouper.count())
	}
	calls := grouper.snapshot()
	if !reflect.DeepEqual(calls[0], []string{"三", "四"}) {
		t.Fatalf("model saw %v", calls[0])
	}
	all := paragraphStore.All()
	if all[0].ID != "s1" || all[0].Source != SourceLLM {
		t.Fatalf("covered paragraph was rewritten: %+v", all[0])
	}
}

func TestWorkerBackfillSeedsOpenTail(t *testing.T) {
	s1 := testSegment("s1", "说话人1", "一", testBase, testBase.Add(time.Second))
	s2 := testSegment("s2", "说话人1", "二", testBase.Add(2*time.Second), testBase.Add(3*time.Second))
	base2 := testBase.Add(10 * time.Minute)
	s3 := testSegment("s3", "说话人1", "三", base2, base2.Add(time.Second))

	paragraphStore := openTestStore(t)
	clock := newFakeClock(s3.EndedAt.Add(10 * time.Second))
	grouper := &fakeGrouper{}
	worker := newTestWorker(t, clock, paragraphStore, fakeSegments{s1, s2, s3}, grouper)
	worker.Start(context.Background())
	defer worker.Close()

	waitFor(t, "backfilled tail", func() bool {
		all := paragraphStore.All()
		return len(all) == 1 && len(all[0].SegmentIDs) == 3
	})

	s4 := testSegment("s4", "说话人1", "四", s3.EndedAt.Add(2*time.Second), s3.EndedAt.Add(3*time.Second))
	worker.Notify(s4)

	waitFor(t, "tail extended", func() bool {
		all := paragraphStore.All()
		return len(all) == 1 && len(all[0].SegmentIDs) == 4
	})
	all := paragraphStore.All()
	if !reflect.DeepEqual(all[0].SegmentIDs, []string{"s1", "s2", "s3", "s4"}) || all[0].Text != "一二三四" {
		t.Fatalf("tail paragraph = %+v", all[0])
	}
	if grouper.count() != 2 {
		t.Fatalf("model calls = %d", grouper.count())
	}
}

func TestWorkerLiveRunsBeforeBackfill(t *testing.T) {
	a1 := testSegment("a1", "说话人1", "旧一", testBase, testBase.Add(time.Second))
	a2 := testSegment("a2", "说话人1", "旧二", testBase.Add(2*time.Second), testBase.Add(3*time.Second))
	base2 := testBase.Add(5 * time.Minute)
	b1 := testSegment("b1", "说话人2", "旧三", base2, base2.Add(time.Second))
	b2 := testSegment("b2", "说话人2", "旧四", base2.Add(2*time.Second), base2.Add(3*time.Second))
	base3 := testBase.Add(10 * time.Minute)
	c1 := testSegment("c1", "说话人3", "旧五", base3, base3.Add(time.Second))
	c2 := testSegment("c2", "说话人3", "旧六", base3.Add(2*time.Second), base3.Add(3*time.Second))

	clock := newFakeClock(base3.Add(5 * time.Minute))
	firstStarted := make(chan struct{})
	release := make(chan struct{})
	grouper := &fakeGrouper{fn: func(call int, ctx context.Context, texts []string) ([]Group, error) {
		if call == 1 {
			close(firstStarted)
			<-release
		}
		return []Group{{Start: 1, End: len(texts)}}, nil
	}}

	paragraphStore := openTestStore(t)
	if err := paragraphStore.SaveMany([]Paragraph{testParagraph("c1", c1.StartedAt, "c1", "c2")}); err != nil {
		t.Fatal(err)
	}
	worker := newTestWorker(t, clock, paragraphStore, fakeSegments{a1, a2, b1, b2, c1, c2}, grouper)
	worker.Start(context.Background())
	defer worker.Close()

	<-firstStarted
	now := clock.Now()
	worker.Notify(testSegment("l1", "说话人4", "实时一", now, now.Add(time.Second)))
	worker.Notify(testSegment("l2", "说话人4", "实时二", now.Add(2*time.Second), now.Add(3*time.Second)))
	close(release)

	waitFor(t, "processed live run", func() bool { return grouper.count() == 3 })
	calls := grouper.snapshot()
	if !reflect.DeepEqual(calls[0], []string{"旧一", "旧二"}) {
		t.Fatalf("first call = %v", calls[0])
	}
	if !reflect.DeepEqual(calls[1], []string{"实时一", "实时二"}) {
		t.Fatalf("live run was not preferred: %v", calls[1])
	}
	if !reflect.DeepEqual(calls[2], []string{"旧三", "旧四"}) {
		t.Fatalf("third call = %v", calls[2])
	}
}

func TestWorkerCloseCancelsInFlightRequest(t *testing.T) {
	called := make(chan struct{})
	grouper := &fakeGrouper{fn: func(_ int, ctx context.Context, _ []string) ([]Group, error) {
		close(called)
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	paragraphStore := openTestStore(t)
	worker := newTestWorker(t, newFakeClock(testBase), paragraphStore, nil, grouper)
	worker.Start(context.Background())

	worker.Notify(testSegment("a1", "说话人1", "一", testBase, testBase.Add(time.Second)))
	worker.Notify(testSegment("a2", "说话人1", "二", testBase.Add(2*time.Second), testBase.Add(3*time.Second)))
	<-called

	start := time.Now()
	if err := worker.Close(); err != nil {
		t.Fatalf("close error: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("close took %s", elapsed)
	}
	if err := worker.Close(); err != nil {
		t.Fatalf("second close error: %v", err)
	}
}

func TestWorkerNotifyBeforeStartOrAfterClose(t *testing.T) {
	worker := newTestWorker(t, newFakeClock(testBase), openTestStore(t), nil, &fakeGrouper{})
	worker.Notify(testSegment("a", "说话人1", "一", testBase, testBase.Add(time.Second)))
	worker.mu.Lock()
	queued := len(worker.inbox)
	worker.mu.Unlock()
	if queued != 0 {
		t.Fatalf("notify before start queued %d", queued)
	}
	if err := worker.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRebuild(t *testing.T) {
	one := testSegment("a1", "说话人1", "一", testBase, testBase.Add(time.Second))
	two := testSegment("a2", "说话人1", "二", testBase.Add(2*time.Second), testBase.Add(3*time.Second))
	base2 := testBase.Add(5 * time.Minute)
	three := testSegment("b1", "说话人2", "三", base2, base2.Add(time.Second))
	base3 := testBase.Add(10 * time.Minute)
	four := testSegment("c1", "说话人3", "四", base3, base3.Add(time.Second))
	five := testSegment("c2", "说话人3", "五", base3.Add(2*time.Second), base3.Add(3*time.Second))

	grouper := &fakeGrouper{fn: func(_ int, _ context.Context, texts []string) ([]Group, error) {
		groups := make([]Group, len(texts))
		for i := range texts {
			groups[i] = Group{Start: i + 1, End: i + 1}
		}
		return groups, nil
	}}
	paragraphStore := openTestStore(t)
	if err := paragraphStore.SaveMany([]Paragraph{testParagraph("stale", testBase.Add(-time.Hour), "old")}); err != nil {
		t.Fatal(err)
	}

	var progress [][2]int
	stats, err := Rebuild(context.Background(), paragraphStore,
		[]store.Segment{one, two, three, four, five}, grouper, "m", RunOptions{},
		newFakeClock(base3.Add(time.Hour)), func(done, total int) {
			progress = append(progress, [2]int{done, total})
		})
	if err != nil {
		t.Fatal(err)
	}
	want := RebuildStats{Runs: 3, LLM: 2, Single: 1, Fallback: 0, Paragraphs: 5}
	if stats != want {
		t.Fatalf("stats = %+v, want %+v", stats, want)
	}
	if len(progress) != 3 || progress[0] != [2]int{1, 3} || progress[2] != [2]int{3, 3} {
		t.Fatalf("progress = %v", progress)
	}
	all := paragraphStore.All()
	if len(all) != 5 {
		t.Fatalf("paragraphs = %d", len(all))
	}
	for _, paragraph := range all {
		if paragraph.ID == "stale" {
			t.Fatal("stale paragraph survived rebuild")
		}
	}
	if grouper.count() != 2 {
		t.Fatalf("model calls = %d", grouper.count())
	}
}

func TestRebuildStopsCallingAfterTransportFailure(t *testing.T) {
	one := testSegment("a1", "说话人1", "一", testBase, testBase.Add(time.Second))
	two := testSegment("a2", "说话人1", "二", testBase.Add(2*time.Second), testBase.Add(3*time.Second))
	three := testSegment("a3", "说话人1", "三", testBase.Add(4*time.Second), testBase.Add(5*time.Second))
	four := testSegment("a4", "说话人1", "四", testBase.Add(6*time.Second), testBase.Add(7*time.Second))

	grouper := &fakeGrouper{fn: func(int, context.Context, []string) ([]Group, error) {
		return nil, errors.New("connection refused")
	}}
	paragraphStore := openTestStore(t)
	stats, err := Rebuild(context.Background(), paragraphStore, []store.Segment{one, two, three, four},
		grouper, "m", RunOptions{}, newFakeClock(testBase), nil)
	if err != nil {
		t.Fatal(err)
	}
	if grouper.count() != 1 {
		t.Fatalf("model calls = %d, want 1", grouper.count())
	}
	want := RebuildStats{Runs: 1, LLM: 0, Single: 0, Fallback: 1, Paragraphs: 1}
	if stats != want {
		t.Fatalf("stats = %+v, want %+v", stats, want)
	}
}

func TestRebuildRetriesAfterInvalidGrouping(t *testing.T) {
	one := testSegment("a1", "说话人1", "一", testBase, testBase.Add(time.Second))
	two := testSegment("a2", "说话人1", "二", testBase.Add(2*time.Second), testBase.Add(3*time.Second))
	base2 := testBase.Add(5 * time.Minute)
	three := testSegment("b1", "说话人2", "三", base2, base2.Add(time.Second))
	four := testSegment("b2", "说话人2", "四", base2.Add(2*time.Second), base2.Add(3*time.Second))

	grouper := &fakeGrouper{fn: func(call int, _ context.Context, texts []string) ([]Group, error) {
		if call == 1 {
			return nil, fmt.Errorf("%w: bad offsets", ErrInvalidGrouping)
		}
		return []Group{{Start: 1, End: len(texts)}}, nil
	}}
	paragraphStore := openTestStore(t)
	stats, err := Rebuild(context.Background(), paragraphStore, []store.Segment{one, two, three, four},
		grouper, "m", RunOptions{}, newFakeClock(base2), nil)
	if err != nil {
		t.Fatal(err)
	}
	if grouper.count() != 2 {
		t.Fatalf("model calls = %d, want 2", grouper.count())
	}
	want := RebuildStats{Runs: 2, LLM: 1, Single: 0, Fallback: 1, Paragraphs: 2}
	if stats != want {
		t.Fatalf("stats = %+v, want %+v", stats, want)
	}
}

func TestRebuildWithoutGrouperFallsBack(t *testing.T) {
	one := testSegment("a1", "说话人1", "一", testBase, testBase.Add(time.Second))
	two := testSegment("a2", "说话人1", "二", testBase.Add(2*time.Second), testBase.Add(3*time.Second))
	paragraphStore := openTestStore(t)

	stats, err := Rebuild(context.Background(), paragraphStore, []store.Segment{one, two}, nil, "", RunOptions{}, newFakeClock(testBase), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := RebuildStats{Runs: 1, LLM: 0, Single: 0, Fallback: 1, Paragraphs: 1}
	if stats != want {
		t.Fatalf("stats = %+v, want %+v", stats, want)
	}
	all := paragraphStore.All()
	if len(all) != 1 || all[0].Source != SourceFallback || all[0].Text != "一二" {
		t.Fatalf("paragraphs = %+v", all)
	}
}

type mutableSegments struct {
	mu   sync.Mutex
	list []store.Segment
}

func (m *mutableSegments) All() ([]store.Segment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]store.Segment(nil), m.list...), nil
}

func (m *mutableSegments) Replace(list []store.Segment) {
	m.mu.Lock()
	m.list = append([]store.Segment(nil), list...)
	m.mu.Unlock()
}

func TestWorkerReloadDropsDeletedSegments(t *testing.T) {
	clock := newFakeClock(testBase)
	paragraphStore := openTestStore(t)
	source := &mutableSegments{list: []store.Segment{
		testSegment("a", "说话人1", "你好", testBase, testBase.Add(time.Second)),
		testSegment("b", "说话人1", "世界", testBase.Add(2*time.Second), testBase.Add(3*time.Second)),
	}}
	grouper := &fakeGrouper{}
	worker := newTestWorker(t, clock, paragraphStore, source, grouper)
	worker.Start(context.Background())
	defer worker.Close()

	waitFor(t, "initial merged paragraph", func() bool {
		all := paragraphStore.All()
		return len(all) == 1 && len(all[0].SegmentIDs) == 2
	})

	source.Replace([]store.Segment{
		testSegment("b", "说话人1", "世界", testBase.Add(2*time.Second), testBase.Add(3*time.Second)),
	})
	if _, err := paragraphStore.RemoveSegments(map[string]struct{}{"a": {}}); err != nil {
		t.Fatal(err)
	}
	worker.Reload()

	waitFor(t, "paragraph rebuilt from surviving segment", func() bool {
		all := paragraphStore.All()
		if len(all) != 1 {
			return false
		}
		if len(all[0].SegmentIDs) != 1 || all[0].SegmentIDs[0] != "b" {
			return false
		}
		return all[0].Source == SourceSingle
	})
}
