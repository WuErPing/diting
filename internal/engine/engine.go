package engine

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/v2/sherpa_onnx"

	"diting/internal/audio"
	"diting/internal/speaker"
	"diting/internal/store"
)

type CaptureState string

const (
	StateStopped  CaptureState = "STOPPED"
	StateRunning  CaptureState = "RUNNING"
	StateStopping CaptureState = "STOPPING"
)

type Status struct {
	State         CaptureState `json:"state"`
	StartedAt     *time.Time   `json:"startedAt,omitempty"`
	SegmentCount  uint64       `json:"segmentCount"`
	DroppedChunks uint64       `json:"droppedChunks"`
	LastError     string       `json:"lastError,omitempty"`
}

type Interim struct {
	SegmentID string    `json:"segmentId"`
	StartedAt time.Time `json:"startedAt"`
	Text      string    `json:"text"`
}

type Event struct {
	Type    string
	Status  Status
	Interim Interim
	Segment store.Segment
}

type Options struct {
	SpeakerRegistry  string
	SpeakerThreshold float32
}

type Engine struct {
	paths      ModelPaths
	store      *store.Store
	recognizer *sherpa.OnlineRecognizer
	refiner    *sherpa.OfflineRecognizer
	speakerEx  *sherpa.SpeakerEmbeddingExtractor
	speakers   *speaker.Registry

	opMu            sync.Mutex
	mu              sync.RWMutex
	status          Status
	capture         *audio.Capture
	session         *captureSession
	events          chan Event
	dropped         atomic.Uint64
	speakerErrorOne sync.Once
}

func New(paths ModelPaths, segmentStore *store.Store, opts Options) (*Engine, error) {
	recognizer, err := NewOnlineRecognizer(paths)
	if err != nil {
		return nil, err
	}
	refiner, err := NewOfflineRecognizer(paths)
	if err != nil {
		sherpa.DeleteOnlineRecognizer(recognizer)
		return nil, err
	}
	engine := &Engine{
		paths:      paths,
		store:      segmentStore,
		recognizer: recognizer,
		refiner:    refiner,
		status:     Status{State: StateStopped},
		events:     make(chan Event, 256),
	}
	if opts.SpeakerRegistry != "" {
		extractor, err := NewSpeakerExtractor(paths)
		if err != nil {
			log.Printf("说话人识别不可用，继续无标签运行: %v", err)
		} else {
			registry, err := speaker.Open(opts.SpeakerRegistry, extractor.Dim(), opts.SpeakerThreshold)
			if err != nil {
				log.Printf("说话人识别不可用，继续无标签运行: %v", err)
				sherpa.DeleteSpeakerEmbeddingExtractor(extractor)
			} else {
				engine.speakerEx = extractor
				engine.speakers = registry
			}
		}
	}
	return engine, nil
}

func (e *Engine) Events() <-chan Event {
	return e.events
}

func (e *Engine) Status() Status {
	e.mu.RLock()
	defer e.mu.RUnlock()
	status := e.status
	status.DroppedChunks = e.dropped.Load()
	return status
}

func (e *Engine) Start() error {
	e.opMu.Lock()
	defer e.opMu.Unlock()
	if e.status.State == StateRunning {
		return nil
	}
	if e.status.State == StateStopping {
		return fmt.Errorf("capture is stopping")
	}

	vad, err := NewVAD(e.paths)
	if err != nil {
		e.setError(err)
		return err
	}
	stream := sherpa.NewOnlineStream(e.recognizer)
	pcm := make(chan []float32, 256)
	done := make(chan struct{})
	session := &captureSession{
		pcm:        pcm,
		done:       done,
		vad:        vad,
		stream:     stream,
		recognizer: e.recognizer,
		store:      e.store,
		engine:     e,
	}
	capture, err := audio.StartCapture(pcm, &e.dropped)
	if err != nil {
		sherpa.DeleteOnlineStream(stream)
		sherpa.DeleteVoiceActivityDetector(vad)
		e.setError(err)
		return err
	}

	e.capture = capture
	e.session = session
	now := time.Now()
	e.setStatus(Status{
		State:        StateRunning,
		StartedAt:    &now,
		SegmentCount: e.status.SegmentCount,
	})
	go session.run()
	return nil
}

func (e *Engine) Stop() {
	e.opMu.Lock()
	defer e.opMu.Unlock()
	if e.session == nil {
		return
	}

	e.setStatus(Status{State: StateStopping, SegmentCount: e.status.SegmentCount})
	session := e.session
	e.capture.Close()
	close(session.pcm)
	<-session.done
	e.capture = nil
	e.session = nil
	e.setStatus(Status{State: StateStopped, SegmentCount: e.status.SegmentCount})
}

func (e *Engine) Close() {
	e.Stop()
	if e.speakers != nil {
		if err := e.speakers.Save(); err != nil {
			log.Printf("保存声纹注册表失败: %v", err)
		}
	}
	if e.speakerEx != nil {
		sherpa.DeleteSpeakerEmbeddingExtractor(e.speakerEx)
	}
	sherpa.DeleteOnlineRecognizer(e.recognizer)
	sherpa.DeleteOfflineRecognizer(e.refiner)
}

func (e *Engine) RefineWAV(samples []float32) string {
	return e.refine(samples)
}

func (e *Engine) IdentifySamples(samples []float32, duration time.Duration) string {
	if e.speakerEx == nil || e.speakers == nil || len(samples) < SampleRate/2 {
		return ""
	}
	stream := e.speakerEx.CreateStream()
	defer sherpa.DeleteOnlineStream(stream)
	stream.AcceptWaveform(SampleRate, samples)
	stream.InputFinished()
	if !e.speakerEx.IsReady(stream) {
		return ""
	}
	embedding := e.speakerEx.Compute(stream)
	if len(embedding) == 0 {
		e.speakerErrorOne.Do(func() {
			log.Printf("提取声纹失败，后续片段将不带说话人标签")
		})
		return ""
	}
	return e.speakers.Identify(embedding, duration)
}

func (e *Engine) refine(samples []float32) string {
	stream := sherpa.NewOfflineStream(e.refiner)
	defer sherpa.DeleteOfflineStream(stream)
	stream.AcceptWaveform(SampleRate, samples)
	e.refiner.Decode(stream)
	return strings.TrimSpace(stream.GetResult().Text)
}

func (e *Engine) setStatus(status Status) {
	e.mu.Lock()
	e.status = status
	e.mu.Unlock()
	e.publish(Event{Type: "status", Status: e.Status()})
}

func (e *Engine) setError(err error) {
	e.mu.Lock()
	e.status.LastError = err.Error()
	e.mu.Unlock()
	e.publish(Event{Type: "error", Status: e.Status()})
}

func (e *Engine) segmentSaved() {
	e.mu.Lock()
	e.status.SegmentCount++
	e.status.LastError = ""
	e.mu.Unlock()
}

func (e *Engine) publish(event Event) {
	select {
	case e.events <- event:
	default:
	}
}

func (e *Engine) ProcessWAV(samples []float32) error {
	e.opMu.Lock()
	defer e.opMu.Unlock()
	if e.session != nil {
		return fmt.Errorf("capture is running")
	}

	vad, err := NewVAD(e.paths)
	if err != nil {
		return err
	}
	stream := sherpa.NewOnlineStream(e.recognizer)
	session := &captureSession{
		pcm:        make(chan []float32, 256),
		done:       make(chan struct{}),
		vad:        vad,
		stream:     stream,
		recognizer: e.recognizer,
		store:      e.store,
		engine:     e,
	}
	e.session = session
	defer func() { e.session = nil }()
	go session.run()
	for start := 0; start < len(samples); start += 320 {
		end := min(start+320, len(samples))
		session.pcm <- samples[start:end]
	}
	close(session.pcm)
	<-session.done
	return nil
}
