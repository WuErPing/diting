package paragraph

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
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
	if first.ID != "a" || first.Source != SourceLLM {
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
	if grouper.count() != 2 {
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
	if grouper.count() != 2 {
		t.Fatalf("model calls = %d", grouper.count())
	}
}

func TestWorkerBuildsTopicAcrossSpeakerChange(t *testing.T) {
	clock := newFakeClock(testBase)
	paragraphStore := openTestStore(t)
	grouper := &fakeGrouper{fn: func(_ int, _ context.Context, texts []string) ([]Group, error) {
		return []Group{{Start: 1, End: len(texts), Title: "预算审批", Summary: "确定预算和审批时间。"}}, nil
	}}
	worker := newTestWorker(t, clock, paragraphStore, nil, grouper)
	worker.Start(context.Background())
	defer worker.Close()

	worker.Notify(testSegment("a", "说话人1", "预算十万。", testBase, testBase.Add(time.Second)))
	waitFor(t, "first paragraph", func() bool { return len(paragraphStore.All()) == 1 })
	worker.Notify(testSegment("b", "说话人2", "周五前审批。", testBase.Add(2*time.Second), testBase.Add(3*time.Second)))

	waitFor(t, "multi-speaker topic", func() bool {
		all := paragraphStore.All()
		return len(all) == 1 && all[0].Source == SourceLLM && len(all[0].SegmentIDs) == 2
	})
	topic := paragraphStore.All()[0]
	if topic.Title != "预算审批" || topic.Speaker != "" || topic.Text != "预算十万。周五前审批。" {
		t.Fatalf("topic = %+v", topic)
	}
	calls := grouper.snapshot()
	last := calls[len(calls)-1]
	if !reflect.DeepEqual(last, []string{"说话人1：预算十万。", "说话人2：周五前审批。"}) {
		t.Fatalf("model input = %v", last)
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
			if all[1].ID != "c" || all[1].Source != SourceLLM {
				t.Fatalf("second paragraph = %+v", all[1])
			}
			if grouper.count() != 3 {
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
	worker := NewWorker(WorkerOptions{
		Store: paragraphStore, Grouper: grouper, Model: "test-model", MaxSegments: 2, Clock: clock, Logf: quietLogf,
	})
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
	if grouper.count() != 4 {
		t.Fatalf("model calls = %d", grouper.count())
	}
}

func TestWorkerInvalidGroupingDoesNotCoolDown(t *testing.T) {
	clock := newFakeClock(testBase)
	paragraphStore := openTestStore(t)
	grouper := &fakeGrouper{fn: func(call int, _ context.Context, texts []string) ([]Group, error) {
		if call == 2 {
			return nil, fmt.Errorf("%w: bad offsets", ErrInvalidGrouping)
		}
		return []Group{{Start: 1, End: len(texts)}}, nil
	}}
	worker := NewWorker(WorkerOptions{
		Store: paragraphStore, Grouper: grouper, Model: "test-model", MaxSegments: 2, Clock: clock, Logf: quietLogf,
	})
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
		return len(all) == 2 && all[0].Source == SourceLLM && grouper.count() >= 3
	})
	worker.Notify(testSegment("b2", "说话人2", "四", testBase.Add(6*time.Second), testBase.Add(7*time.Second)))

	waitFor(t, "second llm paragraph", func() bool {
		all := paragraphStore.All()
		return len(all) == 2 && all[1].Source == SourceLLM
	})
	if grouper.count() != 4 {
		t.Fatalf("model calls = %d", grouper.count())
	}
}

func TestWorkerLogsModelForGrouping(t *testing.T) {
	tests := []struct {
		name          string
		llmCalls      int
		invalidGroups bool
		wantLog       string
	}{
		{name: "progress", llmCalls: 19, wantLog: "Paragraph merge progress: model=test-model calls=20"},
		{
			name: "failure", invalidGroups: true,
			wantLog: "Paragraph splitting failed; falling back to concatenation: model=test-model segments=2 chars=4 elapsed=0s err=invalid model grouping: paragraph 2 starts at 2, overlapping the covered range",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			grouper := &fakeGrouper{fn: func(_ int, _ context.Context, texts []string) ([]Group, error) {
				if tc.invalidGroups {
					return []Group{{Start: 1, End: 2}, {Start: 2, End: 2}}, nil
				}
				return []Group{{Start: 1, End: len(texts)}}, nil
			}}
			var logs []string
			worker := NewWorker(WorkerOptions{
				Store: openTestStore(t), Grouper: grouper, Model: "test-model", Clock: newFakeClock(testBase),
				Logf: func(format string, args ...any) { logs = append(logs, fmt.Sprintf(format, args...)) },
			})
			worker.llmCalls = tc.llmCalls
			run := []store.Segment{testSegment("a", "说话人1", "你好", testBase, testBase.Add(time.Second))}
			if tc.invalidGroups {
				run = append(run, testSegment("b", "说话人1", "世界", testBase.Add(2*time.Second), testBase.Add(3*time.Second)))
			}

			worker.process(context.Background(), job{run: run})

			if got := strings.Join(logs, "\n"); !strings.Contains(got, tc.wantLog) {
				t.Fatalf("logs = %q, want substring %q", got, tc.wantLog)
			}
		})
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
	worker := NewWorker(WorkerOptions{
		Store: paragraphStore, Segments: fakeSegments{s1, s2, s3, s4}, Grouper: grouper,
		Model: "test-model", MaxSegments: 2, Clock: clock, Logf: quietLogf,
	})
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
	if !reflect.DeepEqual(calls[0], []string{"说话人2：三", "说话人2：四"}) {
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
	worker := NewWorker(WorkerOptions{
		Store: paragraphStore, Segments: fakeSegments{a1, a2, b1, b2, c1, c2}, Grouper: grouper,
		Model: "test-model", MaxSegments: 2, Clock: clock, Logf: quietLogf,
	})
	worker.Start(context.Background())
	defer worker.Close()

	<-firstStarted
	now := clock.Now()
	worker.Notify(testSegment("l1", "说话人4", "实时一", now, now.Add(time.Second)))
	worker.Notify(testSegment("l2", "说话人4", "实时二", now.Add(2*time.Second), now.Add(3*time.Second)))
	close(release)

	waitFor(t, "processed live run", func() bool { return grouper.count() == 3 })
	calls := grouper.snapshot()
	if !reflect.DeepEqual(calls[0], []string{"说话人1：旧一", "说话人1：旧二"}) {
		t.Fatalf("first call = %v", calls[0])
	}
	if !reflect.DeepEqual(calls[1], []string{"说话人4：实时一", "说话人4：实时二"}) {
		t.Fatalf("live run was not preferred: %v", calls[1])
	}
	if !reflect.DeepEqual(calls[2], []string{"说话人2：旧三", "说话人2：旧四"}) {
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

func TestWorkerSplitRecent(t *testing.T) {
	one := testSegment("a1", "说话人1", "一", testBase, testBase.Add(time.Second))
	two := testSegment("a2", "说话人1", "二", testBase.Add(2*time.Second), testBase.Add(3*time.Second))
	base2 := testBase.Add(5 * time.Minute)
	three := testSegment("b1", "说话人2", "三", base2, base2.Add(time.Second))
	four := testSegment("b2", "说话人2", "四", base2.Add(2*time.Second), base2.Add(3*time.Second))
	segments := []store.Segment{one, two, three, four}
	paragraphStore := openTestStore(t)
	if err := paragraphStore.SaveMany([]Paragraph{
		{ID: "a1", StartedAt: one.StartedAt, EndedAt: two.EndedAt, Text: "旧段落", SegmentIDs: []string{"a1", "a2"}, Source: SourceFallback},
		{ID: "b1", StartedAt: three.StartedAt, EndedAt: four.EndedAt, Text: "三四", SegmentIDs: []string{"b1", "b2"}, Source: SourceFallback},
	}); err != nil {
		t.Fatal(err)
	}
	grouper := &fakeGrouper{fn: func(_ int, _ context.Context, texts []string) ([]Group, error) {
		groups := make([]Group, len(texts))
		for i := range texts {
			groups[i] = Group{Start: i + 1, End: i + 1}
		}
		return groups, nil
	}}
	worker := NewWorker(WorkerOptions{
		Store: paragraphStore, Segments: fakeSegments(segments), Grouper: grouper,
		Model: "test-model", MaxSegments: 2, Clock: newFakeClock(testBase), Logf: quietLogf,
	})
	events := make(chan Paragraph, 4)
	worker.SetOnParagraph(func(p Paragraph) { events <- p })

	stats, err := worker.SplitRecent(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	want := RebuildStats{Runs: 1, LLM: 1, Paragraphs: 2}
	if stats != want {
		t.Fatalf("stats = %+v, want %+v", stats, want)
	}
	all := paragraphStore.All()
	if len(all) != 3 || all[0].Text != "旧段落" || all[1].Text != "三" || all[2].Text != "四" {
		t.Fatalf("paragraphs = %+v", all)
	}
	for _, paragraph := range all[1:] {
		if paragraph.Source != SourceLLM {
			t.Fatalf("source = %q, want %q", paragraph.Source, SourceLLM)
		}
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
}

func TestWorkerSplitRecentUnavailable(t *testing.T) {
	worker := newTestWorker(t, newFakeClock(testBase), openTestStore(t), fakeSegments{}, nil)
	if _, err := worker.SplitRecent(context.Background(), 500); !errors.Is(err, ErrSplitUnavailable) {
		t.Fatalf("error = %v, want ErrSplitUnavailable", err)
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
		[]store.Segment{one, two, three, four, five}, grouper, nil, "m", RunOptions{MaxSegments: 2},
		newFakeClock(base3.Add(time.Hour)), nil, func(done, total int) {
			progress = append(progress, [2]int{done, total})
		})
	if err != nil {
		t.Fatal(err)
	}
	want := RebuildStats{Runs: 3, LLM: 3, Single: 0, Fallback: 0, Paragraphs: 5}
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
	if grouper.count() != 3 {
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
		grouper, nil, "m", RunOptions{}, newFakeClock(testBase), nil, nil)
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
		grouper, nil, "m", RunOptions{MaxSegments: 2}, newFakeClock(base2), nil, nil)
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

	stats, err := Rebuild(context.Background(), paragraphStore, []store.Segment{one, two}, nil, nil, "", RunOptions{}, newFakeClock(testBase), nil, nil)
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
		return all[0].Source == SourceLLM
	})
}

type fakeCorrector struct {
	mu    sync.Mutex
	calls [][]string
	fn    func(ctx context.Context, texts []string) ([]string, error)
}

func (c *fakeCorrector) Correct(ctx context.Context, texts []string) ([]string, error) {
	c.mu.Lock()
	c.calls = append(c.calls, append([]string(nil), texts...))
	fn := c.fn
	c.mu.Unlock()
	if fn != nil {
		return fn(ctx, texts)
	}
	return append([]string(nil), texts...), nil
}

func (c *fakeCorrector) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.calls)
}

func TestWorkerCorrectsParagraphText(t *testing.T) {
	clock := newFakeClock(testBase)
	paragraphStore := openTestStore(t)
	corrector := &fakeCorrector{fn: func(_ context.Context, texts []string) ([]string, error) {
		return []string{"摘要总结。"}, nil
	}}
	worker := NewWorker(WorkerOptions{
		Store:       paragraphStore,
		Corrector:   corrector,
		Model:       "test-model",
		MaxSegments: 1,
		Clock:       clock,
		Logf:        quietLogf,
	})
	worker.Start(context.Background())
	defer worker.Close()

	worker.Notify(testSegment("a", "说话人1", "在要总结。", testBase, testBase.Add(time.Second)))
	waitFor(t, "open paragraph", func() bool {
		all := paragraphStore.All()
		return len(all) == 1 && all[0].Text == "在要总结。"
	})
	if corrector.count() != 0 {
		t.Fatalf("open run must not trigger correction, calls = %d", corrector.count())
	}

	// Reaching the run cap closes the previous run; only then is correction applied.
	worker.Notify(testSegment("b", "说话人2", "另一句。", testBase.Add(2*time.Second), testBase.Add(3*time.Second)))
	waitFor(t, "corrected paragraph", func() bool {
		for _, paragraph := range paragraphStore.All() {
			if paragraph.ID == "a" && paragraph.Text == "摘要总结。" {
				return true
			}
		}
		return false
	})
	for _, paragraph := range paragraphStore.All() {
		if paragraph.ID == "a" && (paragraph.Source != SourceSingle || paragraph.SegmentIDs[0] != "a") {
			t.Fatalf("paragraph = %+v", paragraph)
		}
	}
}

func TestWorkerSavesOriginalWhenCorrectionFails(t *testing.T) {
	clock := newFakeClock(testBase)
	paragraphStore := openTestStore(t)
	corrector := &fakeCorrector{fn: func(context.Context, []string) ([]string, error) {
		return nil, errors.New("connection refused")
	}}
	worker := NewWorker(WorkerOptions{
		Store:       paragraphStore,
		Corrector:   corrector,
		Model:       "test-model",
		MaxSegments: 1,
		Clock:       clock,
		Logf:        quietLogf,
	})
	worker.Start(context.Background())
	defer worker.Close()

	worker.Notify(testSegment("a", "说话人1", "在要总结。", testBase, testBase.Add(time.Second)))
	waitFor(t, "open paragraph", func() bool { return len(paragraphStore.All()) == 1 })
	worker.Notify(testSegment("b", "说话人2", "另一句。", testBase.Add(2*time.Second), testBase.Add(3*time.Second)))
	waitFor(t, "uncorrected paragraph after failed correction", func() bool {
		all := paragraphStore.All()
		if len(all) != 2 {
			return false
		}
		for _, paragraph := range all {
			if paragraph.ID == "a" && paragraph.Text == "在要总结。" {
				return true
			}
		}
		return false
	})
}

func TestRebuildAppliesCorrections(t *testing.T) {
	one := testSegment("a1", "说话人1", "在要", testBase, testBase.Add(time.Second))
	two := testSegment("a2", "说话人1", "总结。", testBase.Add(2*time.Second), testBase.Add(3*time.Second))
	grouper := &fakeGrouper{}
	corrector := &fakeCorrector{fn: func(_ context.Context, texts []string) ([]string, error) {
		return []string{"摘要总结。"}, nil
	}}
	paragraphStore := openTestStore(t)

	stats, err := Rebuild(context.Background(), paragraphStore, []store.Segment{one, two}, grouper, corrector,
		"m", RunOptions{}, newFakeClock(testBase), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats != (RebuildStats{Runs: 1, LLM: 1, Single: 0, Fallback: 0, Paragraphs: 1}) {
		t.Fatalf("stats = %+v", stats)
	}
	all := paragraphStore.All()
	if len(all) != 1 || all[0].Text != "摘要总结。" {
		t.Fatalf("paragraphs = %v", all)
	}
}

func compareSegments() (a1, a2, b1, b2 store.Segment) {
	a1 = testSegment("a1", "说话人1", "在要", testBase, testBase.Add(time.Second))
	a2 = testSegment("a2", "说话人1", "总结。", testBase.Add(2*time.Second), testBase.Add(3*time.Second))
	base2 := testBase.Add(5 * time.Minute)
	b1 = testSegment("b1", "说话人2", "三", base2, base2.Add(time.Second))
	b2 = testSegment("b2", "说话人2", "四", base2.Add(2*time.Second), base2.Add(3*time.Second))
	return
}

func seedCompareStore(t *testing.T, a1, a2, b1, b2 store.Segment) *Store {
	t.Helper()
	paragraphStore := openTestStore(t)
	err := paragraphStore.SaveMany([]Paragraph{
		{ID: "a1", StartedAt: a1.StartedAt, EndedAt: a2.EndedAt, Speaker: "说话人1", Text: "摘要总结。", SegmentIDs: []string{"a1", "a2"}, Source: SourceLLM},
		{ID: "b1", StartedAt: b1.StartedAt, EndedAt: b2.EndedAt, Speaker: "说话人2", Text: "三四", SegmentIDs: []string{"b1", "b2"}, Source: SourceLLM},
	})
	if err != nil {
		t.Fatal(err)
	}
	return paragraphStore
}

func TestCompareRecentSelectsOnlyRecentRunsAndDoesNotWrite(t *testing.T) {
	a1, a2, b1, b2 := compareSegments()
	paragraphStore := seedCompareStore(t, a1, a2, b1, b2)
	before := paragraphStore.All()

	grouper := &fakeGrouper{}
	corrector := &fakeCorrector{fn: func(_ context.Context, texts []string) ([]string, error) {
		out := make([]string, len(texts))
		for i, text := range texts {
			out[i] = strings.Replace(text, "四", "思", 1)
		}
		return out, nil
	}}

	// The last two segments (b1, b2) form run B, so only run B is compared.
	comparisons, err := CompareRecent(context.Background(), paragraphStore,
		[]store.Segment{a1, a2, b1, b2}, grouper, corrector, "trial-model", RunOptions{MaxSegments: 2},
		newFakeClock(b2.EndedAt), nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(comparisons) != 1 {
		t.Fatalf("comparisons = %d, want 1", len(comparisons))
	}
	c := comparisons[0]
	if c.Speaker != "说话人2" || c.Raw != "三四" || c.Before != "三四" || c.After != "三思" {
		t.Fatalf("comparison = %+v", c)
	}
	if !reflect.DeepEqual(paragraphStore.All(), before) {
		t.Fatal("CompareRecent must not modify the store")
	}
}

func TestCompareRecentClampsNAndSelectsAllRuns(t *testing.T) {
	a1, a2, b1, b2 := compareSegments()
	paragraphStore := seedCompareStore(t, a1, a2, b1, b2)

	comparisons, err := CompareRecent(context.Background(), paragraphStore,
		[]store.Segment{a1, a2, b1, b2}, &fakeGrouper{}, nil, "trial-model", RunOptions{MaxSegments: 2},
		newFakeClock(b2.EndedAt), nil, 99)
	if err != nil {
		t.Fatal(err)
	}
	if len(comparisons) != 2 {
		t.Fatalf("comparisons = %d, want 2", len(comparisons))
	}
	if comparisons[0].Before != "摘要总结。" || comparisons[0].After != "在要总结。" {
		t.Fatalf("first comparison = %+v", comparisons[0])
	}
}

func TestWorkerCorrectionToggle(t *testing.T) {
	clock := newFakeClock(testBase)
	paragraphStore := openTestStore(t)
	corrector := &fakeCorrector{fn: func(_ context.Context, texts []string) ([]string, error) {
		out := make([]string, len(texts))
		for i, text := range texts {
			out[i] = strings.Replace(text, "在要", "摘要", 1)
		}
		return out, nil
	}}
	worker := NewWorker(WorkerOptions{
		Store:       paragraphStore,
		Corrector:   corrector,
		Model:       "test-model",
		MaxSegments: 1,
		Clock:       clock,
		Logf:        quietLogf,
	})
	if !worker.correctionEnabled() {
		t.Fatal("correction should default on when a corrector is present")
	}
	worker.Start(context.Background())
	defer worker.Close()

	textOf := func(id string) string {
		for _, paragraph := range paragraphStore.All() {
			if paragraph.ID == id {
				return paragraph.Text
			}
		}
		return ""
	}

	// Correction on: closing run [a] applies the correction.
	worker.Notify(testSegment("a", "说话人1", "在要一。", testBase, testBase.Add(time.Second)))
	waitFor(t, "open a", func() bool { return len(paragraphStore.All()) == 1 })
	worker.Notify(testSegment("b", "说话人2", "在要二。", testBase.Add(2*time.Second), testBase.Add(3*time.Second)))
	waitFor(t, "corrected a", func() bool { return textOf("a") == "摘要一。" && corrector.count() == 1 })

	// Pause correction: closing run [b] must keep its original text.
	worker.SetCorrectionEnabled(false)
	worker.Notify(testSegment("c", "说话人3", "在要三。", testBase.Add(4*time.Second), testBase.Add(5*time.Second)))
	waitFor(t, "uncorrected b", func() bool { return textOf("b") == "在要二。" && len(paragraphStore.All()) == 3 })
	if corrector.count() != 1 {
		t.Fatalf("paused correction still called the corrector: %d", corrector.count())
	}

	// Resume correction: closing run [c] applies it again.
	worker.SetCorrectionEnabled(true)
	worker.Notify(testSegment("d", "说话人4", "在要四。", testBase.Add(6*time.Second), testBase.Add(7*time.Second)))
	waitFor(t, "corrected c", func() bool { return textOf("c") == "摘要三。" && corrector.count() == 2 })
}

func TestWorkerSetModelStampsParagraphs(t *testing.T) {
	clock := newFakeClock(testBase)
	paragraphStore := openTestStore(t)
	grouper := &fakeGrouper{}
	worker := NewWorker(WorkerOptions{
		Store:   paragraphStore,
		Grouper: grouper,
		Model:   "old-model",
		Clock:   clock,
		Logf:    quietLogf,
	})
	worker.Start(context.Background())
	defer worker.Close()

	worker.Notify(testSegment("a", "说话人1", "一", testBase, testBase.Add(time.Second)))
	waitFor(t, "old model stamp", func() bool {
		all := paragraphStore.All()
		return len(all) == 1 && all[0].Model == "old-model"
	})

	worker.SetModel("new-model")
	worker.Notify(testSegment("b", "说话人1", "二", testBase.Add(2*time.Second), testBase.Add(3*time.Second)))
	waitFor(t, "new model stamp", func() bool {
		all := paragraphStore.All()
		return len(all) == 1 && len(all[0].SegmentIDs) == 2 && all[0].Model == "new-model"
	})
}

func TestWorkerCorrectRecentManually(t *testing.T) {
	clock := newFakeClock(testBase)
	segments := []store.Segment{
		testSegment("a", "说话人1", "在要甲。", testBase, testBase.Add(time.Second)),
		testSegment("b", "说话人1", "在要乙。", testBase.Add(2*time.Second), testBase.Add(3*time.Second)),
		testSegment("c", "说话人1", "在要丙。", testBase.Add(4*time.Second), testBase.Add(5*time.Second)),
	}
	paragraphStore := openTestStore(t)
	stored := make([]Paragraph, len(segments))
	for i, segment := range segments {
		stored[i] = Paragraph{
			ID: segment.ID, StartedAt: segment.StartedAt, EndedAt: segment.EndedAt,
			Speaker: segment.Speaker, Text: segment.Text, SegmentIDs: []string{segment.ID}, Source: SourceLLM,
		}
	}
	if err := paragraphStore.SaveMany(stored); err != nil {
		t.Fatal(err)
	}
	corrector := &fakeCorrector{fn: func(_ context.Context, texts []string) ([]string, error) {
		if !reflect.DeepEqual(texts, []string{"在要乙。", "在要丙。"}) {
			t.Fatalf("correction input = %v", texts)
		}
		return []string{"摘要乙。", "在要丙。"}, nil
	}}
	worker := NewWorker(WorkerOptions{
		Store: paragraphStore, Segments: fakeSegments(segments), Corrector: corrector,
		Model: "manual-model", Clock: clock, Logf: quietLogf,
	})
	worker.SetCorrectionEnabled(false)
	events := make(chan Paragraph, 1)
	worker.SetOnParagraph(func(p Paragraph) { events <- p })
	worker.Start(context.Background())
	defer worker.Close()

	result, err := worker.CorrectRecent(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if result.Paragraphs != 2 || result.Changed != 1 {
		t.Fatalf("result = %+v", result)
	}
	all := paragraphStore.All()
	if all[0].Text != "在要甲。" || all[1].Text != "摘要乙。" || all[2].Text != "在要丙。" {
		t.Fatalf("paragraphs = %+v", all)
	}
	if all[1].Model != "manual-model" || !all[1].UpdatedAt.Equal(testBase) {
		t.Fatalf("corrected metadata = %+v", all[1])
	}
	select {
	case event := <-events:
		if event.ID != "b" || event.Text != "摘要乙。" {
			t.Fatalf("event = %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("manual correction event not fired")
	}
}

func TestWorkerRetranscribeParagraphUpdatesOnlyTarget(t *testing.T) {
	segments := []store.Segment{
		{ID: "a", Text: "旧一"},
		{ID: "b", Text: "旧二"},
		{ID: "c", Text: "其他"},
	}
	paragraphStore := openTestStore(t)
	if err := paragraphStore.SaveMany([]Paragraph{
		{ID: "a", Text: "旧一旧二", SegmentIDs: []string{"a", "b"}},
		{ID: "c", Text: "其他", SegmentIDs: []string{"c"}},
	}); err != nil {
		t.Fatal(err)
	}
	worker := NewWorker(WorkerOptions{
		Store:    paragraphStore,
		Segments: fakeSegments(segments),
		Logf:     quietLogf,
		Retranscribe: func(_ context.Context, selected []store.Segment) ([]store.Segment, int, error) {
			if len(selected) != 2 || selected[0].ID != "a" || selected[1].ID != "b" {
				t.Fatalf("selected = %+v", selected)
			}
			selected[0].Text = "新一"
			selected[1].Text = "新二"
			return selected, 2, nil
		},
	})
	result, err := worker.RetranscribeParagraph(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	if result.Paragraphs != 1 || result.Segments != 2 || result.Changed != 2 {
		t.Fatalf("result = %+v", result)
	}
	all := paragraphStore.All()
	if len(all) != 2 || all[0].Text != "新一新二" || all[1].Text != "其他" {
		t.Fatalf("paragraphs = %+v", all)
	}
}

func TestWorkerRetranscribeParagraphRefreshesDerivedTextWithoutRawChanges(t *testing.T) {
	paragraphStore := openTestStore(t)
	if err := paragraphStore.SaveMany([]Paragraph{{ID: "a", Text: "语义修正后的文本", SegmentIDs: []string{"a"}}}); err != nil {
		t.Fatal(err)
	}
	worker := NewWorker(WorkerOptions{
		Store: paragraphStore,
		Logf:  quietLogf,
		Retranscribe: func(_ context.Context, selected []store.Segment) ([]store.Segment, int, error) {
			selected[0].Text = "最新原始识别"
			return selected, 0, nil
		},
	})
	result, err := worker.RetranscribeParagraph(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	if result.Changed != 0 || paragraphStore.All()[0].Text != "最新原始识别" {
		t.Fatalf("result = %+v, paragraph = %+v", result, paragraphStore.All()[0])
	}
}

func TestWorkerRetranscribeParagraphUnavailable(t *testing.T) {
	worker := NewWorker(WorkerOptions{Store: openTestStore(t), Logf: quietLogf})
	if _, err := worker.RetranscribeParagraph(context.Background(), "a"); !errors.Is(err, ErrASRUnavailable) {
		t.Fatalf("error = %v", err)
	}
}

type fakeSummarizer struct {
	summary string
	input   string
}

func (f *fakeSummarizer) Summarize(_ context.Context, text string) (string, error) {
	f.input = text
	return f.summary, nil
}

func TestWorkerSummarizeParagraphOnlyTarget(t *testing.T) {
	paragraphStore := openTestStore(t)
	if err := paragraphStore.SaveMany([]Paragraph{
		{ID: "a", Text: "讨论发布安排", Summary: "旧主题简介", SegmentIDs: []string{"a"}},
		{ID: "b", Text: "其他段落", SegmentIDs: []string{"b"}},
	}); err != nil {
		t.Fatal(err)
	}
	summarizer := &fakeSummarizer{summary: "确定了发布安排。"}
	worker := NewWorker(WorkerOptions{Store: paragraphStore, Summarizer: summarizer, Logf: quietLogf})
	result, err := worker.SummarizeParagraph(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	if result.Paragraphs != 1 || result.Summary != "确定了发布安排。" || summarizer.input != "讨论发布安排" {
		t.Fatalf("result = %+v, input = %q", result, summarizer.input)
	}
	all := paragraphStore.All()
	if all[0].Digest != "确定了发布安排。" || all[0].Summary != "旧主题简介" || all[1].Digest != "" {
		t.Fatalf("paragraphs = %+v", all)
	}
}

func TestWorkerCorrectParagraphOnlyTarget(t *testing.T) {
	clock := newFakeClock(testBase)
	segments := []store.Segment{
		testSegment("a", "说话人1", "在要甲。", testBase, testBase.Add(time.Second)),
		testSegment("b", "说话人2", "在要乙。", testBase.Add(2*time.Second), testBase.Add(3*time.Second)),
	}
	paragraphStore := openTestStore(t)
	for _, segment := range segments {
		if err := paragraphStore.SaveMany([]Paragraph{{
			ID: segment.ID, StartedAt: segment.StartedAt, EndedAt: segment.EndedAt,
			Speaker: segment.Speaker, Text: segment.Text, SegmentIDs: []string{segment.ID}, Source: SourceLLM,
		}}); err != nil {
			t.Fatal(err)
		}
	}
	corrector := &fakeCorrector{fn: func(_ context.Context, texts []string) ([]string, error) {
		if !reflect.DeepEqual(texts, []string{"在要乙。"}) {
			t.Fatalf("correction input = %v", texts)
		}
		return []string{"摘要乙。"}, nil
	}}
	worker := NewWorker(WorkerOptions{
		Store: paragraphStore, Segments: fakeSegments(segments), Corrector: corrector,
		Model: "manual-model", Clock: clock, Logf: quietLogf,
	})
	worker.Start(context.Background())
	defer worker.Close()

	result, err := worker.CorrectParagraph(context.Background(), "b")
	if err != nil {
		t.Fatal(err)
	}
	if result.Paragraphs != 1 || result.Changed != 1 {
		t.Fatalf("result = %+v", result)
	}
	all := paragraphStore.All()
	if all[0].Text != "在要甲。" || all[1].Text != "摘要乙。" {
		t.Fatalf("paragraphs = %+v", all)
	}
	if _, err := worker.CorrectParagraph(context.Background(), "missing"); !errors.Is(err, ErrParagraphNotFound) {
		t.Fatalf("missing paragraph error = %v", err)
	}
}
