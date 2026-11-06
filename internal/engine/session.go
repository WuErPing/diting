package engine

import (
	"strings"
	"time"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/v2/sherpa_onnx"

	"diting/internal/store"
)

const (
	preRollSamples   = SampleRate * 3 / 10
	maxSpeechSamples = SampleRate * 30
)

type captureSession struct {
	pcm        chan []float32
	done       chan struct{}
	vad        *sherpa.VoiceActivityDetector
	stream     *sherpa.OnlineStream
	recognizer *sherpa.OnlineRecognizer
	store      *store.Store
	engine     *Engine
}

type utterance struct {
	id            string
	startedAt     time.Time
	preRoll       []float32
	lastText      string
	speechSamples int
}

func (s *captureSession) run() {
	defer close(s.done)
	defer sherpa.DeleteOnlineStream(s.stream)
	defer sherpa.DeleteVoiceActivityDetector(s.vad)

	ring := make([]float32, 0, preRollSamples)
	var current *utterance
	for samples := range s.pcm {
		before := append([]float32(nil), ring...)
		s.stream.AcceptWaveform(SampleRate, samples)
		s.vad.AcceptWaveform(samples)
		s.decode()

		if s.vad.IsSpeech() {
			if current == nil {
				duration := sampleDuration(len(before) + len(samples))
				current = &utterance{
					id:            time.Now().Format("20060102-150405.000000000"),
					startedAt:     time.Now().Add(-duration),
					preRoll:       before,
					speechSamples: len(samples),
				}
			} else {
				current.speechSamples += len(samples)
			}
		}
		ring = append(ring, samples...)
		if len(ring) > preRollSamples {
			ring = append([]float32(nil), ring[len(ring)-preRollSamples:]...)
		}

		if current != nil {
			s.publishInterim(current)
		}
		current = s.finishReady(current)
		if current != nil && current.speechSamples >= maxSpeechSamples {
			s.vad.Flush()
			current = s.finishReady(current)
		}
	}

	if current != nil {
		tail := make([]float32, SampleRate*4/5)
		s.stream.AcceptWaveform(SampleRate, tail)
		s.vad.AcceptWaveform(tail)
		s.decode()
	}
	s.vad.Flush()
	current = s.finishReady(current)
	if current != nil {
		s.engine.publish(Event{Type: "interim", Interim: Interim{SegmentID: current.id}})
	}
}

func (s *captureSession) decode() {
	for s.recognizer.IsReady(s.stream) {
		s.recognizer.Decode(s.stream)
	}
}

func (s *captureSession) publishInterim(current *utterance) {
	text := strings.TrimSpace(s.recognizer.GetResult(s.stream).Text)
	if text == current.lastText {
		return
	}
	current.lastText = text
	s.engine.publish(Event{Type: "interim", Interim: Interim{
		SegmentID: current.id,
		StartedAt: current.startedAt,
		Text:      text,
	}})
}

func (s *captureSession) finishReady(current *utterance) *utterance {
	for !s.vad.IsEmpty() {
		segment := s.vad.Front()
		s.vad.Pop()
		if current == nil {
			current = &utterance{
				id:        time.Now().Format("20060102-150405.000000000"),
				startedAt: time.Now().Add(-sampleDuration(len(segment.Samples))),
			}
		}

		samples := make([]float32, 0, len(current.preRoll)+len(segment.Samples))
		samples = append(samples, current.preRoll...)
		samples = append(samples, segment.Samples...)
		text := s.engine.refine(samples)
		saved, err := s.store.Save(store.Segment{
			ID:        current.id,
			StartedAt: current.startedAt,
			EndedAt:   current.startedAt.Add(sampleDuration(len(samples))),
			Text:      text,
		}, samples)
		if err != nil {
			s.engine.setError(err)
		} else {
			s.engine.segmentSaved()
			s.engine.publish(Event{Type: "segment", Segment: saved})
		}
		s.recognizer.Reset(s.stream)
		current = nil
	}
	return current
}

func sampleDuration(samples int) time.Duration {
	return time.Duration(samples) * time.Second / SampleRate
}
