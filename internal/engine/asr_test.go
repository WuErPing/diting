package engine

import (
	"errors"
	"path/filepath"
	"testing"

	"diting/internal/lexicon"
)

type fakeAudioTranscriber struct {
	text  string
	err   error
	calls int
}

func (f *fakeAudioTranscriber) Transcribe([]float32) (string, error) {
	f.calls++
	return f.text, f.err
}

func TestEngineUsesCloudASRWhenSelected(t *testing.T) {
	cloud := &fakeAudioTranscriber{text: " 云端文本 "}
	engine := &Engine{cloudASR: cloud, asrProvider: ASRProviderMiMo}
	lex, err := lexicon.Open(filepath.Join(t.TempDir(), "lexicon.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := lex.Replace([]lexicon.Rule{{From: "云端", To: "MiMo"}}); err != nil {
		t.Fatal(err)
	}
	engine.lexicon = lex

	if got := engine.refineWithAlternatives([]float32{0.1}, "本地临时文本", ""); got != "MiMo文本" {
		t.Fatalf("text = %q", got)
	}
	if cloud.calls != 1 {
		t.Fatalf("calls = %d", cloud.calls)
	}
}

func TestSetASRProviderValidatesConfiguration(t *testing.T) {
	engine := &Engine{}
	if err := engine.SetASRProvider(ASRProviderMiMo); err == nil {
		t.Fatal("expected missing cloud client error")
	}
	engine.cloudASR = &fakeAudioTranscriber{}
	if err := engine.SetASRProvider(ASRProviderMiMo); err != nil {
		t.Fatal(err)
	}
	if err := engine.SetASRProvider(ASRProviderLocal); err != nil {
		t.Fatal(err)
	}
	if err := engine.SetASRProvider("other"); err == nil {
		t.Fatal("expected invalid provider error")
	}
}

func TestCloudASRFailureIsReported(t *testing.T) {
	cloud := &fakeAudioTranscriber{err: errors.New("unavailable")}
	engine := &Engine{cloudASR: cloud, asrProvider: ASRProviderMiMo}
	if _, err := engine.cloudTranscript([]float32{0.1}); err == nil {
		t.Fatal("expected transcription error")
	}
}
