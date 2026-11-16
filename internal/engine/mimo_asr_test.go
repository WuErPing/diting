package engine

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMiMoASRTranscribesWAV(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("api-key") != "tp-test-key" || r.Header.Get("Authorization") != "Bearer tp-test-key" {
			t.Errorf("authentication headers missing")
		}
		var request struct {
			Model    string `json:"model"`
			Messages []struct {
				Content []struct {
					Type       string `json:"type"`
					InputAudio struct {
						Data string `json:"data"`
					} `json:"input_audio"`
				} `json:"content"`
			} `json:"messages"`
			ASROptions struct {
				Language string `json:"language"`
			} `json:"asr_options"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Model != "mimo-v2.5-asr" || request.ASROptions.Language != "zh" {
			t.Errorf("request = %+v", request)
		}
		if len(request.Messages) != 1 || len(request.Messages[0].Content) != 1 {
			t.Fatalf("messages = %+v", request.Messages)
		}
		content := request.Messages[0].Content[0]
		if content.Type != "input_audio" || !strings.HasPrefix(content.InputAudio.Data, "data:audio/wav;base64,") {
			t.Fatalf("audio content = %+v", content)
		}
		wav, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(content.InputAudio.Data, "data:audio/wav;base64,"))
		if err != nil {
			t.Fatal(err)
		}
		if len(wav) <= 44 || string(wav[:4]) != "RIFF" || string(wav[8:12]) != "WAVE" {
			t.Fatalf("invalid WAV payload: %d bytes", len(wav))
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":" 云端识别文本。 "}}]}`))
	}))
	defer server.Close()

	client := NewMiMoASR(MiMoASRConfig{BaseURL: server.URL + "/v1", APIKey: "tp-test-key"})
	text, err := client.Transcribe([]float32{0, 0.25, -0.25})
	if err != nil {
		t.Fatal(err)
	}
	if text != "云端识别文本。" {
		t.Fatalf("text = %q", text)
	}
}

func TestMiMoASRReturnsProviderError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"Invalid API Key"}}`))
	}))
	defer server.Close()

	client := NewMiMoASR(MiMoASRConfig{BaseURL: server.URL, APIKey: "bad"})
	if _, err := client.Transcribe([]float32{0}); err == nil || !strings.Contains(err.Error(), "Invalid API Key") {
		t.Fatalf("error = %v", err)
	}
}
