// Package tokusage records LLM API token usage in a local JSONL ledger and
// exports a full-history aggregate as a tokscale tui-data-cache.json, so
// tokscale-compatible tools (e.g. tokens) can ingest usage from clients that
// tokscale itself does not support. It has no third-party dependencies.
package tokusage

import (
	"fmt"
	"log"
	"sync"
	"time"
)

// Usage holds the five tokscale token counts plus request duration.
type Usage struct {
	Input      int64
	Output     int64
	CacheRead  int64
	CacheWrite int64
	Reasoning  int64
	DurationMs int64
}

func (u Usage) total() int64 {
	return u.Input + u.Output + u.CacheRead + u.CacheWrite + u.Reasoning
}

func (u Usage) add(v Usage) Usage {
	return Usage{
		Input:      u.Input + v.Input,
		Output:     u.Output + v.Output,
		CacheRead:  u.CacheRead + v.CacheRead,
		CacheWrite: u.CacheWrite + v.CacheWrite,
		Reasoning:  u.Reasoning + v.Reasoning,
		DurationMs: u.DurationMs + v.DurationMs,
	}
}

// Entry is one LLM API call. Zero Time means now.
type Entry struct {
	Time     time.Time
	Client   string
	Provider string
	Model    string
	Usage    Usage
}

type Options struct {
	LedgerPath string
	ExportPath string
	Logf       func(format string, args ...any) // nil falls back to log.Printf
	Now        func() time.Time                 // nil falls back to time.Now
}

// Recorder is safe for concurrent use. Every Record appends to the ledger and
// rewrites the export file atomically with the full-history aggregate.
type Recorder struct {
	mu     sync.Mutex
	opts   Options
	ledger *ledger
	agg    *aggregate
	closed bool
}

func Open(opts Options) (*Recorder, error) {
	if opts.LedgerPath == "" || opts.ExportPath == "" {
		return nil, fmt.Errorf("tokusage: LedgerPath 和 ExportPath 不能为空")
	}
	if opts.Logf == nil {
		opts.Logf = log.Printf
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	agg := newAggregate()
	l, err := openLedger(opts.LedgerPath, opts.Logf, agg.add)
	if err != nil {
		return nil, err
	}
	return &Recorder{opts: opts, ledger: l, agg: agg}, nil
}

func (r *Recorder) Record(e Entry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return fmt.Errorf("tokusage: recorder 已关闭")
	}
	if e.Time.IsZero() {
		e.Time = r.opts.Now()
	}
	if err := r.ledger.append(e); err != nil {
		return err
	}
	r.agg.add(e)
	return r.exportLocked()
}

// Export writes the current aggregate to ExportPath. It is a no-op when no
// entries have been recorded (an empty daily list is rejected by tokscale
// consumers).
func (r *Recorder) Export() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.exportLocked()
}

func (r *Recorder) exportLocked() error {
	if r.agg.count == 0 {
		return nil
	}
	cache := r.agg.buildCache(r.opts.Now())
	if err := writeJSONAtomic(r.opts.ExportPath, cache); err != nil {
		return fmt.Errorf("tokusage: 导出 %s: %w", r.opts.ExportPath, err)
	}
	return nil
}

func (r *Recorder) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	exportErr := r.exportLocked()
	closeErr := r.ledger.close()
	if exportErr != nil {
		return exportErr
	}
	return closeErr
}
