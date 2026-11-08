package engine

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
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

func TestRetranscribeDecodesCompressedAudio(t *testing.T) {
	originalRead := readWave
	originalConvert := convertToWAV
	defer func() {
		readWave = originalRead
		convertToWAV = originalConvert
	}()

	converted := ""
	cleaned := false
	convertToWAV = func(path string) (string, func(), error) {
		converted = path
		return "/tmp/fake-decoded.wav", func() { cleaned = true }, nil
	}
	readWave = func(path string) *sherpa.Wave {
		if path != "/tmp/fake-decoded.wav" {
			t.Fatalf("readWave path = %q", path)
		}
		return &sherpa.Wave{Samples: []float32{0.1}, SampleRate: SampleRate}
	}

	history := &fakeHistory{
		root: t.TempDir(),
		segments: []store.Segment{{
			ID:         "a",
			DurationMS: 1000,
			Text:       "旧文本",
			AudioPath:  "audio/20261106/a.m4a",
		}},
	}
	stats, err := RetranscribeAll(history, fakeProcessor{text: "新文本", speaker: "说话人1"})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Updated != 1 || converted != filepath.Join(history.root, "audio/20261106/a.m4a") {
		t.Fatalf("stats = %+v, converted = %q", stats, converted)
	}
	if !cleaned {
		t.Fatal("decoded temp file was not cleaned up")
	}
	if history.rewritten[0].Text != "新文本" {
		t.Fatalf("rewritten = %+v", history.rewritten[0])
	}
}

func TestLoadAudioSamplesUsesCanonicalDecoder(t *testing.T) {
	originalRead := readWave
	originalConvert := convertToWAV
	defer func() {
		readWave = originalRead
		convertToWAV = originalConvert
	}()
	convertToWAV = func(path string) (string, func(), error) {
		return "/tmp/canonical.wav", func() {}, nil
	}
	readWave = func(path string) *sherpa.Wave {
		if path != "/tmp/canonical.wav" {
			t.Fatalf("readWave path = %q", path)
		}
		return &sherpa.Wave{Samples: []float32{0.1, 0.2}, SampleRate: SampleRate}
	}

	samples, err := LoadAudioSamples("recording.m4a")
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 2 {
		t.Fatalf("samples = %d, want 2", len(samples))
	}
}

func TestCanonicalizeWAVRewritesExtensibleHeader(t *testing.T) {
	const (
		channels = 1
		rate     = 16000
		bits     = 16
	)
	samples := make([]byte, 320)

	var fmtPayload [40]byte
	binary.LittleEndian.PutUint16(fmtPayload[0:2], 0xFFFE)
	binary.LittleEndian.PutUint16(fmtPayload[2:4], channels)
	binary.LittleEndian.PutUint32(fmtPayload[4:8], rate)
	binary.LittleEndian.PutUint32(fmtPayload[8:12], rate*channels*bits/8)
	binary.LittleEndian.PutUint16(fmtPayload[12:14], channels*bits/8)
	binary.LittleEndian.PutUint16(fmtPayload[14:16], bits)
	binary.LittleEndian.PutUint16(fmtPayload[16:18], 22)
	binary.LittleEndian.PutUint16(fmtPayload[18:20], bits)
	binary.LittleEndian.PutUint16(fmtPayload[24:26], 1)

	var source bytes.Buffer
	source.WriteString("RIFF")
	_ = binary.Write(&source, binary.LittleEndian, uint32(4+8+40+8+len(samples)))
	source.WriteString("WAVE")
	source.WriteString("fmt ")
	_ = binary.Write(&source, binary.LittleEndian, uint32(40))
	source.Write(fmtPayload[:])
	source.WriteString("data")
	_ = binary.Write(&source, binary.LittleEndian, uint32(len(samples)))
	source.Write(samples)

	path := filepath.Join(t.TempDir(), "extensible.wav")
	if err := os.WriteFile(path, source.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := canonicalizeWAV(path); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 12+8+16+8+len(samples) {
		t.Fatalf("size = %d", len(got))
	}
	if string(got[12:16]) != "fmt " || binary.LittleEndian.Uint32(got[16:20]) != 16 {
		t.Fatalf("fmt chunk = %q size %d", got[12:16], binary.LittleEndian.Uint32(got[16:20]))
	}
	fmtChunk := got[20:36]
	if binary.LittleEndian.Uint16(fmtChunk[0:2]) != 1 ||
		binary.LittleEndian.Uint16(fmtChunk[2:4]) != channels ||
		binary.LittleEndian.Uint32(fmtChunk[4:8]) != rate ||
		binary.LittleEndian.Uint16(fmtChunk[12:14]) != channels*bits/8 ||
		binary.LittleEndian.Uint16(fmtChunk[14:16]) != bits {
		t.Fatalf("fmt chunk = % x", fmtChunk)
	}
	if string(got[36:40]) != "data" || !bytes.Equal(got[44:], samples) {
		t.Fatalf("data chunk mismatch")
	}
}
