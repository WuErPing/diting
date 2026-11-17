package paragraph

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"diting/internal/observability"
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

type Corrector interface {
	Correct(ctx context.Context, texts []string) ([]string, error)
}

type RetranscribeFunc func(ctx context.Context, selected []store.Segment) ([]store.Segment, int, error)

type Summarizer interface {
	Summarize(ctx context.Context, text string) (string, error)
}

type WorkerOptions struct {
	Store        *Store
	Segments     SegmentSource
	Grouper      Grouper
	Corrector    Corrector
	Retranscribe RetranscribeFunc
	Summarizer   Summarizer
	Model        string
	MaxSegments  int
	MaxChars     int
	Clock        Clock
	Logf         func(format string, args ...any)
}

type Worker struct {
	opts    WorkerOptions
	clock   Clock
	runOpts RunOptions

	processMu    sync.Mutex
	mu           sync.Mutex
	inbox        []store.Segment
	onParagraph  func(Paragraph)
	resolve      func(string) string
	model        string
	correctionOn bool
	started      bool
	closed       bool
	cancel       context.CancelFunc
	wake         chan struct{}
	reload       chan struct{}
	done         chan struct{}

	seen       map[string]struct{}
	open       []store.Segment
	openChars  int
	openDirty  bool
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
	// final marks a closed run; correction is expensive (thinking-enabled LLM
	// call), so it only runs once per run instead of on every live re-group.
	final bool
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
			MaxSegments: opts.MaxSegments,
			MaxChars:    opts.MaxChars,
		},
		model:        opts.Model,
		correctionOn: opts.Corrector != nil,
		wake:         make(chan struct{}, 1),
		reload:       make(chan struct{}, 1),
		done:         make(chan struct{}),
	}
}

func (w *Worker) SetOnParagraph(fn func(Paragraph)) {
	w.mu.Lock()
	w.onParagraph = fn
	w.mu.Unlock()
}

func (w *Worker) SetSpeakerResolver(fn func(string) string) {
	w.mu.Lock()
	w.resolve = fn
	w.mu.Unlock()
}

func (w *Worker) speakerResolve(label string) string {
	w.mu.Lock()
	fn := w.resolve
	w.mu.Unlock()
	if fn != nil {
		return fn(label)
	}
	return label
}

// SetModel changes the model stamped on paragraphs composed from now on.
func (w *Worker) SetModel(model string) {
	w.mu.Lock()
	w.model = model
	w.mu.Unlock()
}

func (w *Worker) currentModel() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.model
}

// SetCorrectionEnabled toggles whether closed runs get a semantic-correction
// pass. Disabling it only affects future runs; saved paragraphs are untouched.
func (w *Worker) SetCorrectionEnabled(on bool) {
	w.mu.Lock()
	w.correctionOn = on
	w.mu.Unlock()
}

func (w *Worker) correctionEnabled() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.correctionOn
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

// Reload discards queued work and rebuilds the worker state from the current
// transcript store. It is used after a day is deleted so stale paragraphs and
// in-memory runs cannot be written back.
func (w *Worker) Reload() {
	select {
	case w.reload <- struct{}{}:
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

		if w.reloadRequested() {
			w.backfill(ctx)
			continue
		}

		if len(w.high) > 0 {
			w.process(ctx, w.high[0])
			w.high = w.high[1:]
			continue
		}

		// Re-group the whole open run as soon as a new segment arrives. Clear the
		// flag first so segments accepted mid-request are processed again.
		if w.openDirty && len(w.open) > 0 {
			run := append([]store.Segment(nil), w.open...)
			w.openDirty = false
			w.process(ctx, job{run: run})
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
		case <-w.reload:
			w.backfill(ctx)
		case <-w.wake:
		}
	}
}

func (w *Worker) reloadRequested() bool {
	select {
	case <-w.reload:
		return true
	default:
		return false
	}
}

func (w *Worker) backfill(ctx context.Context) {
	opts := w.runOpts.withDefaults()
	w.seen = make(map[string]struct{})
	w.open = nil
	w.openChars = 0
	w.high = nil
	w.low = nil
	w.backfillN = 0
	w.backfillLL = 0
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
	for i, run := range runs {
		if ctx.Err() != nil {
			return
		}
		if i == len(runs)-1 {
			// Keep the newest run open so live segments extend and re-judge it.
			w.open = append([]store.Segment(nil), run...)
			w.openChars = runChars(run)
			w.openDirty = !coveredBySource(run, coverage, false)
			continue
		}
		if coveredBySource(run, coverage, false) {
			continue
		}
		w.low = append(w.low, job{run: run, final: true})
		w.backfillN++
		if w.opts.Grouper != nil {
			w.backfillLL++
		}
	}
	if w.backfillN > 0 {
		w.opts.Logf("段落回填已入队: run=%d 需要模型=%d", w.backfillN, w.backfillLL)
	}
}

func (w *Worker) drainInbox() {
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
	w.openDirty = true
}

func (w *Worker) continues(segment store.Segment) bool {
	opts := w.runOpts.withDefaults()
	return len(w.open)+1 <= opts.MaxSegments &&
		w.openChars+textLength(segment.Text) <= opts.MaxChars
}

func promptTexts(run []store.Segment, resolve func(string) string) []string {
	texts := make([]string, len(run))
	for i, segment := range run {
		speaker := segment.Speaker
		if resolve != nil {
			speaker = resolve(speaker)
		}
		texts[i] = segment.Text
		if speaker != "" {
			texts[i] = speaker + "：" + texts[i]
		}
	}
	return texts
}

func (w *Worker) closeOpen() {
	if len(w.open) == 0 {
		return
	}
	run := w.open
	dirty := w.openDirty
	w.open = nil
	w.openChars = 0
	w.openDirty = false
	// With a corrector, every closed run needs one final pass even when its
	// paragraphs are already saved, because open-run processing skips correction.
	// When correction is paused, a clean already-covered run needs no final pass.
	if !dirty && (w.opts.Corrector == nil || !w.correctionEnabled()) && coveredBySource(run, w.opts.Store.Coverage(), false) {
		return
	}
	w.high = append(w.high, job{run: run, final: true})
}

func (w *Worker) process(ctx context.Context, job job) {
	w.processMu.Lock()
	defer w.processMu.Unlock()

	run := job.run
	if len(run) == 0 {
		return
	}
	model := w.currentModel()

	source := SourceSingle
	groups := FallbackGroups(len(run))
	if len(run) > 1 || w.opts.Grouper != nil {
		source = SourceFallback
	}
	if w.opts.Grouper != nil && !w.cooling() {
		texts := promptTexts(run, w.speakerResolve)
		start := w.clock.Now()
		callCtx, cancel := context.WithTimeout(ctx, requestDeadline)
		observability.LLMRequests.Inc()
		result, err := w.opts.Grouper.Group(callCtx, texts)
		cancel()
		observability.LLMDuration.Observe(w.clock.Now().Sub(start))
		if err != nil {
			observability.LLMFailures.Inc()
		}
		if err == nil {
			var changed bool
			result, changed, err = normalizeGroups(result, len(texts))
			if err == nil && changed {
				w.opts.Logf("段落边界缺口已自动补齐: segments=%d groups=%d", len(texts), len(result))
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
			w.opts.Logf("Paragraph splitting failed; falling back to concatenation: model=%s segments=%d chars=%d elapsed=%s err=%v",
				model, len(run), runChars(run), w.clock.Now().Sub(start).Round(time.Millisecond), err)
		} else {
			source = SourceLLM
			groups = result
			w.resetFailure()
		}
	}

	paragraphs := ComposeParagraphs(run, groups, source, model, w.clock.Now(), w.speakerResolve)
	if w.opts.Corrector != nil && w.correctionEnabled() && job.final && !w.cooling() {
		w.correctParagraphs(ctx, paragraphs)
	}
	if err := w.opts.Store.SaveMany(paragraphs); err != nil {
		w.opts.Logf("保存段落失败: %v", err)
		return
	}
	observability.ParagraphsSaved.Add(uint64(len(paragraphs)))
	if w.llmCalls > 0 && w.llmCalls%20 == 0 && source == SourceLLM {
		w.opts.Logf("Paragraph merge progress: model=%s calls=%d", model, w.llmCalls)
	}
	for _, paragraph := range paragraphs {
		w.fire(paragraph)
	}
}

var (
	ErrCorrectionUnavailable = errors.New("语义修正不可用")
	ErrSplitUnavailable      = errors.New("自动分段不可用")
	ErrASRUnavailable        = errors.New("ASR 重识别不可用")
	ErrSummaryUnavailable    = errors.New("段落总结不可用")
	ErrParagraphNotFound     = errors.New("段落不存在")
)

type CorrectionResult struct {
	Paragraphs int `json:"paragraphs"`
	Changed    int `json:"changed"`
}

type ASRResult struct {
	Paragraphs int `json:"paragraphs"`
	Segments   int `json:"segments"`
	Changed    int `json:"changed"`
}

type SummaryResult struct {
	Paragraphs int    `json:"paragraphs"`
	Summary    string `json:"summary"`
}

// SummarizeParagraph generates and saves a manual summary without replacing the
// topic metadata or the paragraph's transcript text.
func (w *Worker) SummarizeParagraph(ctx context.Context, id string) (SummaryResult, error) {
	var result SummaryResult
	if w.opts.Summarizer == nil || w.opts.Store == nil {
		return result, ErrSummaryUnavailable
	}
	w.processMu.Lock()
	defer w.processMu.Unlock()
	for _, selected := range w.opts.Store.All() {
		if selected.ID != id {
			continue
		}
		summary, err := w.opts.Summarizer.Summarize(ctx, selected.Text)
		if err != nil {
			return result, err
		}
		selected.Digest = summary
		selected.UpdatedAt = w.clock.Now()
		if err := w.opts.Store.SaveMany([]Paragraph{selected}); err != nil {
			return result, fmt.Errorf("保存段落总结: %w", err)
		}
		w.fire(selected)
		return SummaryResult{Paragraphs: 1, Summary: summary}, nil
	}
	return result, ErrParagraphNotFound
}

// RetranscribeParagraph re-recognizes every recording in one saved paragraph,
// then refreshes that paragraph from the updated raw transcripts.
func (w *Worker) RetranscribeParagraph(ctx context.Context, id string) (ASRResult, error) {
	var result ASRResult
	if w.opts.Retranscribe == nil || w.opts.Store == nil {
		return result, ErrASRUnavailable
	}
	w.processMu.Lock()
	defer w.processMu.Unlock()
	var selected Paragraph
	for _, candidate := range w.opts.Store.All() {
		if candidate.ID == id {
			selected = candidate
			break
		}
	}
	if selected.ID == "" {
		return result, ErrParagraphNotFound
	}
	members := make([]store.Segment, len(selected.SegmentIDs))
	for i, segmentID := range selected.SegmentIDs {
		members[i].ID = segmentID
	}
	updated, changed, err := w.opts.Retranscribe(ctx, members)
	if err != nil {
		return result, err
	}
	result = ASRResult{Paragraphs: 1, Segments: len(members), Changed: changed}
	selected.Text = JoinTexts(updated)
	selected.UpdatedAt = w.clock.Now()
	if err := w.opts.Store.SaveMany([]Paragraph{selected}); err != nil {
		return result, fmt.Errorf("保存重识别段落: %w", err)
	}
	w.fire(selected)
	w.Reload()
	w.opts.Logf("段落 ASR 重识别: %d/%d 条录音文本已更新", changed, len(members))
	return result, nil
}

// RecentParagraphs selects saved paragraphs intersecting the recent segment window.
func RecentParagraphs(paragraphs []Paragraph, segments []store.Segment, limit int) []Paragraph {
	if limit > len(segments) {
		limit = len(segments)
	}
	if limit <= 0 {
		return nil
	}
	recent := make(map[string]struct{}, limit)
	for _, segment := range segments[len(segments)-limit:] {
		recent[segment.ID] = struct{}{}
	}
	var selected []Paragraph
	for _, paragraph := range paragraphs {
		for _, id := range paragraph.SegmentIDs {
			if _, ok := recent[id]; ok {
				selected = append(selected, paragraph)
				break
			}
		}
	}
	return selected
}

// CorrectRecent corrects saved paragraphs intersecting the most recent segment
// window. It is an explicit action, so it ignores the automatic-correction pause.
func (w *Worker) CorrectRecent(ctx context.Context, limit int) (CorrectionResult, error) {
	var result CorrectionResult
	if w.opts.Corrector == nil || w.opts.Store == nil || w.opts.Segments == nil {
		return result, ErrCorrectionUnavailable
	}
	if limit <= 0 {
		return result, fmt.Errorf("limit 必须大于 0")
	}
	w.processMu.Lock()
	defer w.processMu.Unlock()
	segments, err := w.opts.Segments.All()
	if err != nil {
		return result, fmt.Errorf("读取转写记录: %w", err)
	}
	return w.correctSelected(ctx, RecentParagraphs(w.opts.Store.All(), segments, limit))
}

// CorrectParagraph corrects exactly one saved paragraph, independent of the
// automatic-correction pause and any other paragraphs in the preview.
func (w *Worker) CorrectParagraph(ctx context.Context, id string) (CorrectionResult, error) {
	var result CorrectionResult
	if w.opts.Corrector == nil || w.opts.Store == nil {
		return result, ErrCorrectionUnavailable
	}
	w.processMu.Lock()
	defer w.processMu.Unlock()
	for _, paragraph := range w.opts.Store.All() {
		if paragraph.ID == id {
			return w.correctSelected(ctx, []Paragraph{paragraph})
		}
	}
	return result, ErrParagraphNotFound
}

// correctSelected applies and saves corrections while processMu is held.
func (w *Worker) correctSelected(ctx context.Context, selected []Paragraph) (CorrectionResult, error) {
	result := CorrectionResult{Paragraphs: len(selected)}
	if len(selected) == 0 {
		return result, nil
	}
	texts := make([]string, len(selected))
	for i, paragraph := range selected {
		texts[i] = paragraph.Text
	}
	corrected, err := correctTexts(ctx, w.opts.Corrector, texts)
	if err != nil {
		return result, err
	}
	now := w.clock.Now()
	model := w.currentModel()
	changed := make([]Paragraph, 0, len(selected))
	for i := range selected {
		if selected[i].Text == corrected[i] {
			continue
		}
		selected[i].Text = corrected[i]
		selected[i].Model = model
		selected[i].UpdatedAt = now
		changed = append(changed, selected[i])
	}
	result.Changed = len(changed)
	if len(changed) == 0 {
		return result, nil
	}
	if err := w.opts.Store.SaveMany(changed); err != nil {
		return result, fmt.Errorf("保存修正段落: %w", err)
	}
	for _, paragraph := range changed {
		w.fire(paragraph)
	}
	w.opts.Logf("手动语义修正: %d/%d 段已更新", result.Changed, result.Paragraphs)
	return result, nil
}

func (w *Worker) cooling() bool {
	return w.clock.Now().Before(w.failUntil)
}

// correctParagraphs applies best-effort semantic corrections in place; on any
// failure the original texts are kept and the paragraphs still get saved.
func (w *Worker) correctParagraphs(ctx context.Context, paragraphs []Paragraph) {
	texts := make([]string, len(paragraphs))
	for i, paragraph := range paragraphs {
		texts[i] = paragraph.Text
	}
	updated, err := correctTexts(ctx, w.opts.Corrector, texts)
	if ctx.Err() != nil {
		return
	}
	if err != nil {
		if !errors.Is(err, ErrInvalidCorrection) {
			w.recordFailure()
		}
		w.opts.Logf("段落语义修正失败，保留原文: paragraphs=%d err=%v", len(paragraphs), err)
		return
	}
	w.resetFailure()
	applied := 0
	for i := range paragraphs {
		if paragraphs[i].Text != updated[i] {
			paragraphs[i].Text = updated[i]
			applied++
		}
	}
	if applied > 0 {
		w.opts.Logf("段落语义修正: %d/%d 段已更新", applied, len(paragraphs))
	}
}

// correctTexts applies the corrector and adopts only edits within the budget.
// The client bounds each chunk request itself, so no overall deadline is
// imposed here; a multi-chunk batch must not be cut off mid-way.
func correctTexts(ctx context.Context, corrector Corrector, texts []string) ([]string, error) {
	corrected, err := corrector.Correct(ctx, texts)
	if err != nil {
		return nil, err
	}
	merged, _ := ApplyCorrections(texts, corrected)
	return merged, nil
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

func normalizeGroups(groups []Group, n int) ([]Group, bool, error) {
	repaired, changed, err := NormalizeGroups(groups, n)
	if err != nil {
		return nil, false, fmt.Errorf("%w: %v", ErrInvalidGrouping, err)
	}
	return repaired, changed, nil
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
	Runs       int `json:"runs"`
	LLM        int `json:"llm"`
	Single     int `json:"single"`
	Fallback   int `json:"fallback"`
	Paragraphs int `json:"paragraphs"`
}

// SplitRecent re-groups complete runs intersecting the latest segment window.
// Other paragraphs remain untouched, and only the derived paragraph layer changes.
func (w *Worker) SplitRecent(ctx context.Context, limit int) (RebuildStats, error) {
	var stats RebuildStats
	if w.opts.Grouper == nil || w.opts.Store == nil || w.opts.Segments == nil {
		return stats, ErrSplitUnavailable
	}
	if limit <= 0 {
		return stats, fmt.Errorf("limit 必须大于 0")
	}
	w.processMu.Lock()
	defer w.processMu.Unlock()
	segments, err := w.opts.Segments.All()
	if err != nil {
		return stats, fmt.Errorf("读取转写记录: %w", err)
	}
	if limit > len(segments) {
		limit = len(segments)
	}
	if limit == 0 {
		return stats, nil
	}
	recent := make(map[string]struct{}, limit)
	for _, segment := range segments[len(segments)-limit:] {
		recent[segment.ID] = struct{}{}
	}
	opts := w.runOpts
	var paragraphs []Paragraph
	cooling := false
	var corrector Corrector
	if w.correctionEnabled() {
		corrector = w.opts.Corrector
	}
	for _, run := range BuildRuns(segments, opts) {
		selected := false
		for _, segment := range run {
			if _, ok := recent[segment.ID]; ok {
				selected = true
				break
			}
		}
		if !selected {
			continue
		}
		stats.Runs++
		composed, source, err := composeRun(ctx, run, w.opts.Grouper, corrector, w.currentModel(), w.clock, w.speakerResolve, &cooling)
		if err != nil {
			return stats, err
		}
		paragraphs = append(paragraphs, composed...)
		switch source {
		case SourceLLM:
			stats.LLM++
		case SourceSingle:
			stats.Single++
		default:
			stats.Fallback++
		}
	}
	if err := w.opts.Store.SaveMany(paragraphs); err != nil {
		return stats, fmt.Errorf("保存自动分段: %w", err)
	}
	stats.Paragraphs = len(paragraphs)
	for _, paragraph := range paragraphs {
		w.fire(paragraph)
	}
	return stats, nil
}

// Rebuild regenerates every paragraph from the given segments and atomically
// replaces the paragraph file. Failures fall back to whole-run concatenation.
func Rebuild(ctx context.Context, paragraphStore *Store, segments []store.Segment, grouper Grouper, corrector Corrector,
	model string, opts RunOptions, clock Clock, resolve func(string) string, progress func(done, total int)) (RebuildStats, error) {
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
		composed, source, err := composeRun(ctx, run, grouper, corrector, model, clock, resolve, &cooling)
		if err != nil {
			return stats, err
		}
		paragraphs = append(paragraphs, composed...)
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
	observability.ParagraphsSaved.Add(uint64(len(paragraphs)))
	stats.Paragraphs = len(paragraphs)
	return stats, nil
}

// composeRun groups a run and optionally applies semantic correction, mirroring
// Rebuild's per-run logic. cooling is shared across runs so a transport failure
// suppresses further model calls; ErrInvalidGrouping/ErrInvalidCorrection do not
// trip it. It returns the composed paragraphs and their resulting source.
func composeRun(ctx context.Context, run []store.Segment, grouper Grouper, corrector Corrector,
	model string, clock Clock, resolve func(string) string, cooling *bool) ([]Paragraph, string, error) {
	source := SourceSingle
	groups := FallbackGroups(len(run))
	if len(run) > 1 || grouper != nil {
		source = SourceFallback
	}
	if grouper != nil && !*cooling {
		texts := promptTexts(run, resolve)
		start := clock.Now()
		callCtx, cancel := context.WithTimeout(ctx, requestDeadline)
		observability.LLMRequests.Inc()
		result, err := grouper.Group(callCtx, texts)
		cancel()
		observability.LLMDuration.Observe(clock.Now().Sub(start))
		if err == nil {
			result, _, err = normalizeGroups(result, len(texts))
		}
		if err != nil {
			observability.LLMFailures.Inc()
			if ctx.Err() != nil {
				return nil, source, ctx.Err()
			}
			if !errors.Is(err, ErrInvalidGrouping) {
				*cooling = true
			}
		} else {
			source = SourceLLM
			groups = result
		}
	}
	composed := ComposeParagraphs(run, groups, source, model, clock.Now(), resolve)
	if corrector != nil && !*cooling && len(composed) > 0 {
		texts := make([]string, len(composed))
		for j := range composed {
			texts[j] = composed[j].Text
		}
		updated, err := correctTexts(ctx, corrector, texts)
		if err != nil {
			if ctx.Err() != nil {
				return nil, source, ctx.Err()
			}
			if !errors.Is(err, ErrInvalidCorrection) {
				*cooling = true
			}
		} else {
			for j := range composed {
				composed[j].Text = updated[j]
			}
		}
	}
	return composed, source, nil
}

// Comparison holds one run's ASR text alongside the currently stored text and a
// fresh model pass, for non-destructive before/after previews.
type Comparison struct {
	Speaker   string
	StartedAt time.Time
	Raw       string // JoinTexts(run): ASR output with no LLM applied
	Before    string // text currently stored in paragraphs.jsonl
	After     string // fresh group+correct result with the trial model
}

// CompareRecent builds the runs covering the most recent n segments and returns
// their stored text versus a fresh group+correct pass with the given model. It
// never writes to paragraphStore, so previews leave saved data untouched.
func CompareRecent(ctx context.Context, paragraphStore *Store, segments []store.Segment,
	grouper Grouper, corrector Corrector, model string, opts RunOptions,
	clock Clock, resolve func(string) string, n int) ([]Comparison, error) {
	if clock == nil {
		clock = realClock{}
	}
	if n <= 0 || len(segments) == 0 {
		return nil, nil
	}
	if n > len(segments) {
		n = len(segments)
	}
	recent := make(map[string]struct{}, n)
	for _, segment := range segments[len(segments)-n:] {
		recent[segment.ID] = struct{}{}
	}
	stored := paragraphStore.All()

	runs := BuildRuns(segments, opts)
	cooling := false
	var comparisons []Comparison
	for _, run := range runs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		ids := make(map[string]struct{}, len(run))
		touchesRecent := false
		for _, segment := range run {
			ids[segment.ID] = struct{}{}
			if _, ok := recent[segment.ID]; ok {
				touchesRecent = true
			}
		}
		if !touchesRecent {
			continue
		}
		composed, _, err := composeRun(ctx, run, grouper, corrector, model, clock, resolve, &cooling)
		if err != nil {
			return nil, err
		}
		after := make([]string, len(composed))
		for i, paragraph := range composed {
			after[i] = paragraph.Text
		}
		var before []string
		for _, paragraph := range stored {
			for _, id := range paragraph.SegmentIDs {
				if _, ok := ids[id]; ok {
					before = append(before, paragraph.Text)
					break
				}
			}
		}
		speaker := run[0].Speaker
		if resolve != nil {
			speaker = resolve(speaker)
		}
		comparisons = append(comparisons, Comparison{
			Speaker:   speaker,
			StartedAt: run[0].StartedAt,
			Raw:       JoinTexts(run),
			Before:    strings.Join(before, ""),
			After:     strings.Join(after, ""),
		})
	}
	return comparisons, nil
}
