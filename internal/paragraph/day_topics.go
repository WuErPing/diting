package paragraph

import (
	"context"
	"errors"

	"diting/internal/store"
)

var ErrDayTopicsUnavailable = errors.New("按天主题分类不可用")

type DayTopicsResult struct {
	Date     string `json:"date"`
	Segments int    `json:"segments"`
	RebuildStats
}

func (w *Worker) ClassifyDay(ctx context.Context, date string) (DayTopicsResult, error) {
	result := DayTopicsResult{Date: date}
	if w.opts.Grouper == nil || w.opts.Store == nil || w.opts.Segments == nil {
		return result, ErrDayTopicsUnavailable
	}
	w.processMu.Lock()
	defer w.processMu.Unlock()

	segments, err := w.opts.Segments.All()
	if err != nil {
		return result, err
	}
	selected := make([]store.Segment, 0)
	for _, segment := range segments {
		if segment.StartedAt.Local().Format("2006-01-02") == date {
			selected = append(selected, segment)
		}
	}
	result.Segments = len(selected)
	if len(selected) == 0 {
		return result, ErrDayNotFound
	}

	runs := BuildRuns(selected, w.runOpts)
	result.Runs = len(runs)
	paragraphs := make([]Paragraph, 0, len(selected))
	cooling := false
	var corrector Corrector
	if w.correctionEnabled() {
		corrector = w.opts.Corrector
	}
	for _, run := range runs {
		composed, source, err := composeRun(ctx, run, w.opts.Grouper, corrector, w.currentModel(), w.clock, w.speakerResolve, &cooling)
		if err != nil {
			return result, err
		}
		paragraphs = append(paragraphs, composed...)
		switch source {
		case SourceLLM:
			result.LLM++
		case SourceSingle:
			result.Single++
		default:
			result.Fallback++
		}
	}
	canonicalizeTimePeriods(ctx, paragraphs, w.opts.Grouper)
	if err := w.opts.Store.ReplaceDay(date, paragraphs); err != nil {
		return result, err
	}
	result.Paragraphs = len(paragraphs)
	for _, item := range paragraphs {
		w.fire(item)
	}
	return result, nil
}
