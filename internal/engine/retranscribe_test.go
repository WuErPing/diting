package engine

import (
	"testing"
	"time"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/v2/sherpa_onnx"

	"diting/internal/store"
)

type fakeHistory struct {
	root      string
	segments  []store.Segment
	rewritten []store.Segment
}

func (h *fakeHistory) Root() string { return h.root }

func (h *fakeHistory) All() ([]store.Segment, error) {
	return append([]store.Segment(nil), h.segments...), nil
}

func (h *fakeHistory) Rewrite(segments []store.Segment) error {
	h.rewritten = append([]store.Segment(nil), segments...)
	return nil
}

type fakeProcessor struct {
	text    string
	speaker string
}

func (p fakeProcessor) RefineWAV([]float32) string { return p.text }

func (p fakeProcessor) IdentifySamples([]float32, time.Duration) string { return p.speaker }

func TestRetranscribeAll(t *testing.T) {
	original := readWave
	defer func() { readWave = original }()
	readWave = func(string) *sherpa.Wave {
		return &sherpa.Wave{Samples: []float32{0.1, 0.2}, SampleRate: SampleRate}
	}

	started := time.Date(2026, 11, 6, 14, 0, 0, 0, time.Local)
	history := &fakeHistory{
		root: t.TempDir(),
		segments: []store.Segment{{
			ID:         "20261106-140000.000000000",
			StartedAt:  started,
			EndedAt:    started.Add(2 * time.Second),
			DurationMS: 2000,
			Text:       "旧文本",
			AudioPath:  "audio/20261106/20261106-140000.000000000.wav",
		}},
	}

	stats, err := RetranscribeAll(history, fakeProcessor{text: "新文本", speaker: "说话人1"})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Total != 1 || stats.Updated != 1 || stats.MissingAudio != 0 || stats.EmptyText != 0 {
		t.Fatalf("stats = %+v", stats)
	}
	got := history.rewritten[0]
	if got.Text != "新文本" || got.Speaker != "说话人1" {
		t.Fatalf("rewritten = %+v", got)
	}
	if !got.StartedAt.Equal(started) || got.DurationMS != 2000 || got.AudioPath != history.segments[0].AudioPath {
		t.Fatalf("record fields changed: %+v", got)
	}
}

func TestRetranscribeKeepsOldTextWhenDecodeEmpty(t *testing.T) {
	original := readWave
	defer func() { readWave = original }()
	readWave = func(string) *sherpa.Wave {
		return &sherpa.Wave{Samples: []float32{0.1}, SampleRate: SampleRate}
	}

	history := &fakeHistory{
		root: t.TempDir(),
		segments: []store.Segment{{
			ID:         "a",
			DurationMS: 1000,
			Text:       "保留我",
			Speaker:    "旧说话人",
			AudioPath:  "audio/20261106/a.wav",
		}},
	}

	stats, err := RetranscribeAll(history, fakeProcessor{text: "  ", speaker: ""})
	if err != nil {
		t.Fatal(err)
	}
	if stats.EmptyText != 1 || stats.Updated != 0 {
		t.Fatalf("stats = %+v", stats)
	}
	if history.rewritten[0].Text != "保留我" || history.rewritten[0].Speaker != "" {
		t.Fatalf("text = %q, speaker = %q", history.rewritten[0].Text, history.rewritten[0].Speaker)
	}
}

func TestRetranscribeKeepsRecordWhenAudioMissing(t *testing.T) {
	original := readWave
	defer func() { readWave = original }()
	readWave = func(string) *sherpa.Wave { return nil }

	history := &fakeHistory{
		root: t.TempDir(),
		segments: []store.Segment{{
			ID:         "a",
			DurationMS: 1000,
			Text:       "旧文本",
			AudioPath:  "audio/20261106/a.wav",
		}},
	}

	stats, err := RetranscribeAll(history, fakeProcessor{text: "新文本", speaker: "说话人1"})
	if err != nil {
		t.Fatal(err)
	}
	if stats.MissingAudio != 1 || stats.Updated != 0 {
		t.Fatalf("stats = %+v", stats)
	}
	if history.rewritten[0].Text != "旧文本" || history.rewritten[0].Speaker != "" {
		t.Fatalf("record changed: %+v", history.rewritten[0])
	}
}

func TestResolveAudioPathRejectsTraversal(t *testing.T) {
	if _, err := resolveAudioPath("/tmp/data", "../etc/passwd"); err == nil {
		t.Fatal("expected traversal error")
	}
	if _, err := resolveAudioPath("/tmp/data", "audio/20261106/a.wav"); err != nil {
		t.Fatal(err)
	}
}
