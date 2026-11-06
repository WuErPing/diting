package paragraph

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"diting/internal/store"
)

const (
	requestDeadline = 2 * time.Minute
	closeTimeout    = 5 * time.Second
	baseBackoff     = 2 * time.Minute
	maxBackoff      = 10 * time.Minute
)

type SegmentSource interface {
	All() ([]store.Segment, error)
}

type Grouper interface {
	Group(ctx context.Context, texts []string) ([]Group, error)
}

type WorkerOptions struct {
	Store       *Store
	Segments    SegmentSource
	Grouper     Grouper
	Model       string
	IdleTimeout time.Duration
	MaxSegments int
	MaxChars    int
	Clock       Clock
	Logf        func(format string, args ...any)
}

type Worker struct {
	opts    WorkerOptions
	clock   Clock
	runOpts RunOptions

	mu          sync.Mutex
	inbox       []store.Segment
	onParagraph func(Paragraph)
	started     bool
	closed      bool
	cancel      context.CancelFunc
	wake        chan struct{}
	done        chan struct{}

	seen       map[string]struct{}
	open       []store.Segment
	openChars  int
	high       []job
	low        []job
	failures   int
	failUntil  time.Time
	llmCalls   int
	backfillN  int
	backfillLL int
}

type job struct {
	run []store.Segment
}

func NewWorker(opts WorkerOptions) *Worker {
	if opts.Clock == nil {
		opts.Clock = realClock{}
	}
	if opts.Logf == nil {
		opts.Logf = log.Printf
	}
	return &Worker{
		opts:  opts,
		clock: opts.Clock,
		runOpts: RunOptions{
			IdleTimeout: opts.IdleTimeout,
			MaxSegments: opts.MaxSegments,
			MaxChars:    opts.MaxChars,
		},
		wake: make(chan struct{}, 1),
		done: make(chan struct{}),
	}
}

func (w *Worker) SetOnParagraph(fn func(Paragraph)) {
	w.mu.Lock()
	w.onParagraph = fn
	w.mu.Unlock()
}

func (w *Worker) Start(ctx context.Context) {
	w.mu.Lock()
	if w.started || w.closed {
		w.mu.Unlock()
		return
	}
	w.started = true
	ctx, cancel := context.WithCancel(ctx)
	w.cancel = cancel
	w.mu.Unlock()
	go w.loop(ctx)
}

// Notify queues a saved segment without blocking the capture session.
func (w *Worker) Notify(segment store.Segment) {
	if segment.ID == "" {
		return
	}
	w.mu.Lock()
	if !w.started || w.closed {
		w.mu.Unlock()
		return
	}
	w.inbox = append(w.inbox, segment)
	w.mu.Unlock()
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *Worker) Close() error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil
	}
	w.closed = true
	started := w.started
	cancel := w.cancel
	w.mu.Unlock()
	if !started {
		return nil
	}
	if cancel != nil {
		cancel()
	}
	select {
	case <-w.done:
		return nil
	case <-time.After(closeTimeout):
		return fmt.Errorf("段落合并器关闭超时")
	}
}

func (w *Worker) loop(ctx context.Context) {
	defer close(w.done)
	w.backfill(ctx)
	for {
		if ctx.Err() != nil {
			return
		}
		w.drainInbox()

		if len(w.high) > 0 {
			w.process(ctx, w.high[0])
			w.high = w.high[1:]
			continue
		}

		deadline, hasOpen := w.openDeadline()
		if hasOpen && !w.clock.Now().Before(deadline) {
			w.closeOpen()
			continue
		}
		if hasOpen && len(w.low) == 0 {
			delay := deadline.Sub(w.clock.Now())
			timer := w.clock.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-w.wake:
			case <-timer.C():
			}
			timer.Stop()
			continue
		}

		if len(w.low) > 0 {
			w.process(ctx, w.low[0])
			w.low = w.low[1:]
			continue
		}

		select {
		case <-ctx.Done():
			return
		case <-w.wake:
		}
	}
}

func (w *Worker) backfill(ctx context.Context) {
	opts := w.runOpts.withDefaults()
	w.seen = make(map[string]struct{})
	w.open = nil
	w.openChars = 0
	if w.opts.Segments == nil || w.opts.Store == nil {
		return
	}
	segments, err := w.opts.Segments.All()
	if err != nil {
		w.opts.Logf("读取历史记录失败，跳过段落回填: %v", err)
		return
	}
	if len(segments) == 0 {
		return
	}
	for _, segment := range segments {
		w.seen[segment.ID] = struct{}{}
	}

	runs := BuildRuns(segments, opts)
	coverage := w.opts.Store.Coverage()
	now := w.clock.Now()
	for i, run := range runs {
		if ctx.Err() != nil {
			return
		}
		if coveredBySource(run, coverage, false) {
			continue
		}
		last := run[len(run)-1]
		if i == len(runs)-1 && now.Sub(last.EndedAt) < opts.IdleTimeout {
			// Keep the tail open so segments saved after startup extend it.
			w.open = append([]store.Segment(nil), run...)
			w.openChars = runChars(run)
			continue
		}
		w.low = append(w.low, job{run: run})
		w.backfillN++
		if len(run) > 1 {
			w.backfillLL++
		}
	}
	if w.backfillN > 0 {
		w.opts.Logf("段落回填已入队: run=%d 需要模型=%d", w.backfillN, w.backfillLL)
	}
}

func (w *Worker) drainInbox() {
	for {
		w.mu.Lock()
		if len(w.inbox) == 0 {
			w.mu.Unlock()
			return
		}
		batch := w.inbox
		w.inbox = nil
		w.mu.Unlock()
		for _, segment := range batch {
			w.accept(segment)
		}
	}
}

func (w *Worker) accept(segment store.Segment) {
	if _, ok := w.seen[segment.ID]; ok {
		return
	}
	w.seen[segment.ID] = struct{}{}
	if len(w.open) > 0 && !w.continues(segment) {
		w.closeOpen()
	}
	w.open = append(w.open, segment)
	w.openChars += textLength(segment.Text)
}

func (w *Worker) continues(segment store.Segment) bool {
	opts := w.runOpts.withDefaults()
	prev := w.open[len(w.open)-1]
	gap := segment.StartedAt.Sub(prev.EndedAt)
	return segment.Speaker == prev.Speaker &&
		gap <= opts.IdleTimeout &&
		len(w.open)+1 <= opts.MaxSegments &&
		w.openChars+textLength(segment.Text) <= opts.MaxChars
}

func (w *Worker) closeOpen() {
	if len(w.open) == 0 {
		return
	}
	run := w.open
	w.open = nil
	w.openChars = 0
	w.high = append(w.high, job{run: run})
}

func (w *Worker) openDeadline() (time.Time, bool) {
	if len(w.open) == 0 {
		return time.Time{}, false
	}
	return w.open[len(w.open)-1].EndedAt.Add(w.runOpts.withDefaults().IdleTimeout), true
}

func (w *Worker) process(ctx context.Context, job job) {
	run := job.run
	if len(run) == 0 || w.covered(run) {
		return
	}

	source := SourceSingle
	groups := []Group{{Start: 1, End: 1}}
	if len(run) > 1 {
		source = SourceFallback
		groups = FallbackGroups(len(run))
		if w.opts.Grouper != nil && !w.cooling() {
			texts := make([]string, len(run))
			for i, segment := range run {
				texts[i] = segment.Text
			}
			start := w.clock.Now()
			callCtx, cancel := context.WithTimeout(ctx, requestDeadline)
			result, err := w.opts.Grouper.Group(callCtx, texts)
			cancel()
			if err == nil {
				if verifyErr := ValidateGroups(result, len(texts)); verifyErr != nil {
					err = fmt.Errorf("%w: %v", ErrInvalidGrouping, verifyErr)
				}
			}
			if ctx.Err() != nil {
				return
			}
			w.llmCalls++
			if err != nil {
				if !errors.Is(err, ErrInvalidGrouping) {
					w.recordFailure()
				}
				w.opts.Logf("段落切分失败，改用整段拼接: segments=%d chars=%d elapsed=%s err=%v",
					len(run), runChars(run), w.clock.Now().Sub(start).Round(time.Millisecond), err)
			} else {
				source = SourceLLM
				groups = result
				w.resetFailure()
			}
		}
	}

	paragraphs := ComposeParagraphs(run, groups, source, w.opts.Model, w.clock.Now())
	if err := w.opts.Store.SaveMany(paragraphs); err != nil {
		w.opts.Logf("保存段落失败: %v", err)
		return
	}
	if w.llmCalls > 0 && w.llmCalls%20 == 0 && source == SourceLLM {
		w.opts.Logf("段落合并进度: 已调用模型 %d 次", w.llmCalls)
	}
	for _, paragraph := range paragraphs {
		w.fire(paragraph)
	}
}

func (w *Worker) covered(run []store.Segment) bool {
	coverage := w.opts.Store.Coverage()
	return coveredBySource(run, coverage, false)
}

func (w *Worker) cooling() bool {
	return w.clock.Now().Before(w.failUntil)
}

func (w *Worker) recordFailure() {
	w.failures++
	backoff := baseBackoff
	for i := 1; i < w.failures && backoff < maxBackoff; i++ {
		backoff *= 2
	}
	if backoff > maxBackoff {
		backoff = maxBackoff
	}
	w.failUntil = w.clock.Now().Add(backoff)
}

func (w *Worker) resetFailure() {
	w.failures = 0
	w.failUntil = time.Time{}
}

func (w *Worker) fire(paragraph Paragraph) {
	w.mu.Lock()
	fn := w.onParagraph
	w.mu.Unlock()
	if fn != nil {
		fn(paragraph)
	}
}

func coveredBySource(run []store.Segment, coverage map[string]string, fallbackCounts bool) bool {
	for _, segment := range run {
		source, ok := coverage[segment.ID]
		if !ok {
			return false
		}
		if !fallbackCounts && source == SourceFallback {
			return false
		}
	}
	return true
}

func runChars(run []store.Segment) int {
	total := 0
	for _, segment := range run {
		total += textLength(segment.Text)
	}
	return total
}

func textLength(text string) int {
	return len([]rune(strings.TrimSpace(text)))
}

type RebuildStats struct {
	Runs       int
	LLM        int
	Single     int
	Fallback   int
	Paragraphs int
}

// Rebuild regenerates every paragraph from the given segments and atomically
// replaces the paragraph file. Failures fall back to whole-run concatenation.
func Rebuild(ctx context.Context, paragraphStore *Store, segments []store.Segment, grouper Grouper,
	model string, opts RunOptions, clock Clock, progress func(done, total int)) (RebuildStats, error) {
	if clock == nil {
		clock = realClock{}
	}
	runs := BuildRuns(segments, opts)
	stats := RebuildStats{Runs: len(runs)}
	paragraphs := make([]Paragraph, 0, len(runs))
	cooling := false
	for i, run := range runs {
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		source := SourceSingle
		groups := []Group{{Start: 1, End: 1}}
		if len(run) > 1 {
			source = SourceFallback
			groups = FallbackGroups(len(run))
			if grouper != nil && !cooling {
				texts := make([]string, len(run))
				for j, segment := range run {
					texts[j] = segment.Text
				}
				callCtx, cancel := context.WithTimeout(ctx, requestDeadline)
				result, err := grouper.Group(callCtx, texts)
				cancel()
				if err == nil {
					if verifyErr := ValidateGroups(result, len(texts)); verifyErr != nil {
						err = fmt.Errorf("%w: %v", ErrInvalidGrouping, verifyErr)
					}
				}
				if err != nil {
					if ctx.Err() != nil {
						return stats, ctx.Err()
					}
					if !errors.Is(err, ErrInvalidGrouping) {
						cooling = true
					}
				} else {
					source = SourceLLM
					groups = result
				}
			}
		}
		paragraphs = append(paragraphs, ComposeParagraphs(run, groups, source, model, clock.Now())...)
		switch source {
		case SourceLLM:
			stats.LLM++
		case SourceSingle:
			stats.Single++
		default:
			stats.Fallback++
		}
		if progress != nil {
			progress(i+1, len(runs))
		}
	}
	if err := paragraphStore.Replace(paragraphs); err != nil {
		return stats, err
	}
	stats.Paragraphs = len(paragraphs)
	return stats, nil
}
