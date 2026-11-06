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
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

type fakeTimer struct {
	clock   *fakeClock
	ch      chan time.Time
	at      time.Time
	stopped bool
}

func newFakeClock(now time.Time) *fakeClock {
	return &fakeClock{now: now}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) NewTimer(d time.Duration) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	timer := &fakeTimer{clock: c, ch: make(chan time.Time, 1), at: c.now.Add(d)}
	if d <= 0 {
		timer.fire(c.now)
		timer.stopped = true
		return timer
	}
	c.timers = append(c.timers, timer)
	return timer
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	remaining := c.timers[:0]
	for _, timer := range c.timers {
		if timer.stopped {
			continue
		}
		if !timer.at.After(c.now) {
			timer.fire(c.now)
			timer.stopped = true
			continue
		}
		remaining = append(remaining, timer)
	}
	c.timers = remaining
}

func (t *fakeTimer) fire(now time.Time) {
	select {
	case t.ch <- now:
	default:
	}
}

func (t *fakeTimer) C() <-chan time.Time {
	return t.ch
}

func (t *fakeTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	wasActive := !t.stopped
	t.stopped = true
	return wasActive
}

func (t *fakeTimer) Reset(d time.Duration) bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	wasActive := !t.stopped
	t.stopped = false
	t.at = t.clock.now.Add(d)
	if d <= 0 {
		t.fire(t.clock.now)
		t.stopped = true
		return wasActive
	}
	t.clock.timers = append(t.clock.timers, t)
	return wasActive
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
		Store:       paragraphStore,
		Segments:    segments,
		Grouper:     grouper,
		Model:       "test-model",
		Clock:       clock,
		IdleTimeout: 60 * time.Second,
		Logf:        quietLogf,
	})
}

func TestWorkerMergesAfterIdleTimeout(t *testing.T) {
	clock := newFakeClock(testBase)
	paragraphStore := openTestStore(t)
	grouper := &fakeGrouper{}
	worker := newTestWorker(t, clock, paragraphStore, nil, grouper)
	events := make(chan Paragraph, 4)
	worker.SetOnParagraph(func(p Paragraph) { events <- p })
	worker.Start(context.Background())
	defer worker.Close()

	worker.Notify(testSegment("a", "说话人1", "你好", testBase, testBase.Add(time.Second)))
	worker.Notify(testSegment("b", "说话人1", "世界", testBase.Add(2*time.Second), testBase.Add(3*time.Second)))

	clock.Advance(59 * time.Second)
	time.Sleep(50 * time.Millisecond)
	if got := paragraphStore.All(); len(got) != 0 {
		t.Fatalf("merged before idle timeout: %v", got)
	}
	clock.Advance(2 * time.Second)
	time.Sleep(50 * time.Millisecond)
	if got := paragraphStore.All(); len(got) != 0 {
		t.Fatalf("merged before 60s idle: %v", got)
	}

	clock.Advance(2 * time.Second)
	waitFor(t, "merged paragraph", func() bool { return len(paragraphStore.All()) == 1 })

	paragraph := paragraphStore.All()[0]
	if paragraph.Text != "你好世界" || paragraph.Source != SourceLLM || paragraph.Model != "test-model" {
		t.Fatalf("paragraph = %+v", paragraph)
	}
	if grouper.count() != 1 {
		t.Fatalf("model calls = %d", grouper.count())
	}
	select {
	case event := <-events:
		if event.ID != paragraph.ID {
			t.Fatalf("event = %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("paragraph event not fired")
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
	worker.Notify(testSegment("b", "说话人2", "二", testBase.Add(2*time.Second), testBase.Add(3*time.Second)))

	waitFor(t, "first paragraph", func() bool { return len(paragraphStore.All()) == 1 })
	paragraph := paragraphStore.All()[0]
	if paragraph.ID != "a" || paragraph.Source != SourceSingle || paragraph.Text != "一" {
		t.Fatalf("paragraph = %+v", paragraph)
	}
	if grouper.count() != 0 {
		t.Fatalf("single segment should not call model")
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
			opts.IdleTimeout = 60 * time.Second
			opts.Logf = quietLogf
			worker := NewWorker(opts)
			worker.Start(context.Background())
			defer worker.Close()

			worker.Notify(testSegment("a", "说话人1", "你好", testBase, testBase.Add(time.Second)))
			worker.Notify(testSegment("b", "说话人1", "世界", testBase.Add(2*time.Second), testBase.Add(3*time.Second)))
			worker.Notify(testSegment("c", "说话人1", "再见", testBase.Add(4*time.Second), testBase.Add(5*time.Second)))

			waitFor(t, "capped paragraph", func() bool { return len(paragraphStore.All()) == 1 })
			paragraph := paragraphStore.All()[0]
			if paragraph.Text != "你好世界" || !reflect.DeepEqual(paragraph.SegmentIDs, []string{"a", "b"}) {
				t.Fatalf("paragraph = %+v", paragraph)
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
	grouper := &fakeGrouper{fn: func(int, context.Context, []string) ([]Group, error) {
		return nil, errors.New("model down")
	}}
	worker := newTestWorker(t, clock, paragraphStore, nil, grouper)
	worker.Start(context.Background())
	defer worker.Close()

	worker.Notify(testSegment("a1", "说话人1", "一", testBase, testBase.Add(time.Second)))
	worker.Notify(testSegment("a2", "说话人1", "二", testBase.Add(2*time.Second), testBase.Add(3*time.Second)))
	worker.Notify(testSegment("b1", "说话人2", "三", testBase.Add(4*time.Second), testBase.Add(5*time.Second)))
	worker.Notify(testSegment("b2", "说话人2", "四", testBase.Add(6*time.Second), testBase.Add(7*time.Second)))
	worker.Notify(testSegment("c1", "说话人3", "五", testBase.Add(8*time.Second), testBase.Add(9*time.Second)))

	waitFor(t, "fallback paragraphs", func() bool { return len(paragraphStore.All()) == 2 })
	for _, paragraph := range paragraphStore.All() {
		if paragraph.Source != SourceFallback {
			t.Fatalf("source = %q", paragraph.Source)
		}
	}
	if grouper.count() != 1 {
		t.Fatalf("model should be skipped while cooling down, calls = %d", grouper.count())
	}

	clock.Advance(2*time.Minute + time.Second)
	waitFor(t, "idle paragraph", func() bool { return len(paragraphStore.All()) == 3 })

	worker.Notify(testSegment("d1", "说话人4", "六", clock.Now().Add(time.Second), clock.Now().Add(2*time.Second)))
	worker.Notify(testSegment("d2", "说话人4", "七", clock.Now().Add(3*time.Second), clock.Now().Add(4*time.Second)))
	worker.Notify(testSegment("e1", "说话人5", "八", clock.Now().Add(5*time.Second), clock.Now().Add(6*time.Second)))

	waitFor(t, "post-cooldown paragraph", func() bool { return grouper.count() == 2 })
	waitFor(t, "post-cooldown paragraphs", func() bool { return len(paragraphStore.All()) == 4 })
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
	worker.Notify(testSegment("a2", "说话人1", "二", testBase.Add(2*time.Second), testBase.Add(3*time.Second)))
	worker.Notify(testSegment("b1", "说话人2", "三", testBase.Add(4*time.Second), testBase.Add(5*time.Second)))
	worker.Notify(testSegment("b2", "说话人2", "四", testBase.Add(6*time.Second), testBase.Add(7*time.Second)))
	worker.Notify(testSegment("c1", "说话人3", "五", testBase.Add(8*time.Second), testBase.Add(9*time.Second)))

	waitFor(t, "second model call", func() bool { return grouper.count() == 2 })
	waitFor(t, "both paragraphs", func() bool { return len(paragraphStore.All()) == 2 })
	all := paragraphStore.All()
	if all[0].Source != SourceFallback || all[1].Source != SourceLLM {
		t.Fatalf("sources = %q / %q", all[0].Source, all[1].Source)
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

func TestWorkerBackfillSeedsRecentTail(t *testing.T) {
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

	waitFor(t, "backfilled run", func() bool { return len(paragraphStore.All()) == 1 })

	s4 := testSegment("s4", "说话人1", "四", s3.EndedAt.Add(2*time.Second), s3.EndedAt.Add(3*time.Second))
	worker.Notify(s4)
	clock.Advance(61 * time.Second)

	waitFor(t, "tail merged", func() bool { return len(paragraphStore.All()) == 2 })
	all := paragraphStore.All()
	if !reflect.DeepEqual(all[1].SegmentIDs, []string{"s3", "s4"}) || all[1].Text != "三四" {
		t.Fatalf("tail paragraph = %+v", all[1])
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

	clock := newFakeClock(b2.EndedAt.Add(5 * time.Minute))
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
	worker := newTestWorker(t, clock, paragraphStore, fakeSegments{a1, a2, b1, b2}, grouper)
	worker.Start(context.Background())
	defer worker.Close()

	<-firstStarted
	now := clock.Now()
	worker.Notify(testSegment("l1", "说话人3", "实时一", now, now.Add(time.Second)))
	worker.Notify(testSegment("l2", "说话人3", "实时二", now.Add(2*time.Second), now.Add(3*time.Second)))
	worker.Notify(testSegment("l3", "说话人4", "实时三", now.Add(4*time.Second), now.Add(5*time.Second)))
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
	worker.Notify(testSegment("b1", "说话人2", "三", testBase.Add(4*time.Second), testBase.Add(5*time.Second)))
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
	if got := paragraphStore.All(); len(got) != 0 {
		t.Fatalf("paragraphs after cancel = %v", got)
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
	three := testSegment("b1", "说话人1", "三", base2, base2.Add(time.Second))
	base3 := testBase.Add(10 * time.Minute)
	four := testSegment("c1", "说话人2", "四", base3, base3.Add(time.Second))
	five := testSegment("c2", "说话人2", "五", base3.Add(2*time.Second), base3.Add(3*time.Second))

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
	base2 := testBase.Add(5 * time.Minute)
	three := testSegment("b1", "说话人1", "三", base2, base2.Add(time.Second))
	four := testSegment("b2", "说话人1", "四", base2.Add(2*time.Second), base2.Add(3*time.Second))

	grouper := &fakeGrouper{fn: func(int, context.Context, []string) ([]Group, error) {
		return nil, errors.New("connection refused")
	}}
	paragraphStore := openTestStore(t)
	stats, err := Rebuild(context.Background(), paragraphStore, []store.Segment{one, two, three, four},
		grouper, "m", RunOptions{}, newFakeClock(base2), nil)
	if err != nil {
		t.Fatal(err)
	}
	if grouper.count() != 1 {
		t.Fatalf("model calls = %d, want 1", grouper.count())
	}
	if stats.Fallback != 2 || stats.LLM != 0 {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestRebuildRetriesAfterInvalidGrouping(t *testing.T) {
	one := testSegment("a1", "说话人1", "一", testBase, testBase.Add(time.Second))
	two := testSegment("a2", "说话人1", "二", testBase.Add(2*time.Second), testBase.Add(3*time.Second))
	base2 := testBase.Add(5 * time.Minute)
	three := testSegment("b1", "说话人1", "三", base2, base2.Add(time.Second))
	four := testSegment("b2", "说话人1", "四", base2.Add(2*time.Second), base2.Add(3*time.Second))

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
	if stats.Fallback != 1 || stats.LLM != 1 {
		t.Fatalf("stats = %+v", stats)
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
