package observability

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Registry is a minimal, thread-safe metrics registry that renders Prometheus
// text exposition format without external dependencies.
type Registry struct {
	mu         sync.RWMutex
	counters   map[string]*counter
	gauges     map[string]*gauge
	histograms map[string]*histogram
}

type counter struct {
	name  string
	help  string
	value atomic.Uint64
}

type gauge struct {
	name  string
	help  string
	value atomic.Int64
}

type histogram struct {
	name    string
	help    string
	buckets []float64
	counts  []atomic.Uint64
	sum     atomic.Uint64 // microseconds to keep integer precision
	count   atomic.Uint64
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		counters:   make(map[string]*counter),
		gauges:     make(map[string]*gauge),
		histograms: make(map[string]*histogram),
	}
}

// Default is the process-wide registry used by the HTTP handlers.
var Default = NewRegistry()

// Counter returns (or creates) a counter with the given fully-qualified name.
func (r *Registry) Counter(name, help string) *counter {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.counters[name]; ok {
		return c
	}
	c := &counter{name: name, help: help}
	r.counters[name] = c
	return c
}

// Gauge returns (or creates) a gauge with the given fully-qualified name.
func (r *Registry) Gauge(name, help string) *gauge {
	r.mu.Lock()
	defer r.mu.Unlock()
	if g, ok := r.gauges[name]; ok {
		return g
	}
	g := &gauge{name: name, help: help}
	r.gauges[name] = g
	return g
}

// Histogram returns (or creates) a histogram. Buckets are upper bounds in
// seconds; +Inf is appended automatically.
func (r *Registry) Histogram(name, help string, buckets []float64) *histogram {
	r.mu.Lock()
	defer r.mu.Unlock()
	if h, ok := r.histograms[name]; ok {
		return h
	}
	h := &histogram{name: name, help: help, buckets: buckets}
	h.counts = make([]atomic.Uint64, len(buckets)+1)
	r.histograms[name] = h
	return h
}

// Inc increments the counter by 1.
func (c *counter) Inc() { c.value.Add(1) }

// Add adds delta to the counter.
func (c *counter) Add(delta uint64) { c.value.Add(delta) }

// Set overwrites the counter to an absolute value. It is intended for
// synchronizing an external monotonic counter, not for regular increments.
func (c *counter) Set(value uint64) { c.value.Store(value) }

// Value returns the current counter value.
func (c *counter) Value() uint64 { return c.value.Load() }

// Set sets the gauge to value.
func (g *gauge) Set(value int64) { g.value.Store(value) }

// Inc increments the gauge by 1.
func (g *gauge) Inc() { g.value.Add(1) }

// Dec decrements the gauge by 1.
func (g *gauge) Dec() { g.value.Add(-1) }

// Add adds delta to the gauge.
func (g *gauge) Add(delta int64) { g.value.Add(delta) }

// Value returns the current gauge value.
func (g *gauge) Value() int64 { return g.value.Load() }

// Observe records a duration in the histogram.
func (h *histogram) Observe(d time.Duration) {
	us := uint64(d.Microseconds())
	h.sum.Add(us)
	h.count.Add(1)
	seconds := d.Seconds()
	for i, upper := range h.buckets {
		if seconds <= upper {
			h.counts[i].Add(1)
			return
		}
	}
	h.counts[len(h.buckets)].Add(1)
}

// WritePrometheus renders every registered metric in Prometheus text format.
func (r *Registry) WritePrometheus(w io.Writer) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	counterNames := make([]string, 0, len(r.counters))
	for name := range r.counters {
		counterNames = append(counterNames, name)
	}
	sort.Strings(counterNames)
	for _, name := range counterNames {
		c := r.counters[name]
		fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s counter\n%s %d\n", c.name, c.help, c.name, c.name, c.value.Load())
	}

	gaugeNames := make([]string, 0, len(r.gauges))
	for name := range r.gauges {
		gaugeNames = append(gaugeNames, name)
	}
	sort.Strings(gaugeNames)
	for _, name := range gaugeNames {
		g := r.gauges[name]
		fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s gauge\n%s %d\n", g.name, g.help, g.name, g.name, g.value.Load())
	}

	histogramNames := make([]string, 0, len(r.histograms))
	for name := range r.histograms {
		histogramNames = append(histogramNames, name)
	}
	sort.Strings(histogramNames)
	for _, name := range histogramNames {
		h := r.histograms[name]
		fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s histogram\n", h.name, h.help, h.name)
		for i, upper := range h.buckets {
			fmt.Fprintf(w, "%s_bucket{le=%q} %d\n", h.name, formatFloat(upper), h.counts[i].Load())
		}
		fmt.Fprintf(w, "%s_bucket{le=\"+Inf\"} %d\n", h.name, h.counts[len(h.buckets)].Load())
		fmt.Fprintf(w, "%s_sum %g\n", h.name, float64(h.sum.Load())/1e6)
		fmt.Fprintf(w, "%s_count %d\n", h.name, h.count.Load())
	}
}

func formatFloat(f float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.6f", f), "0"), ".")
}

// Pre-registered metrics used across the application. They are created eagerly
// so the exposition format always includes them even before the first event.
var (
	SegmentsSaved = Default.Counter(
		"diting_segments_saved_total",
		"Total number of speech segments successfully persisted",
	)
	SegmentSaveDuration = Default.Histogram(
		"diting_segment_save_duration_seconds",
		"Time spent refining, identifying and saving a speech segment",
		[]float64{0.1, 0.5, 1, 2, 5, 10, 30},
	)
	DroppedAudioChunks = Default.Counter(
		"diting_dropped_audio_chunks_total",
		"Total number of audio chunks dropped because the processing queue was full",
	)
	LLMRequests = Default.Counter(
		"diting_llm_requests_total",
		"Total number of LLM API requests attempted",
	)
	LLMFailures = Default.Counter(
		"diting_llm_failures_total",
		"Total number of LLM API requests that failed",
	)
	LLMDuration = Default.Histogram(
		"diting_llm_request_duration_seconds",
		"Duration of LLM API requests",
		[]float64{1, 5, 15, 30, 60, 120},
	)
	ParagraphsSaved = Default.Counter(
		"diting_paragraphs_saved_total",
		"Total number of derived paragraphs saved",
	)
	SSEConnections = Default.Gauge(
		"diting_sse_connections",
		"Current number of connected Server-Sent Events clients",
	)
	CaptureState = Default.Gauge(
		"diting_capture_state",
		"Current capture state (0=stopped, 1=running, 2=stopping)",
	)
)
