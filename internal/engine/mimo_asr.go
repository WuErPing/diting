package engine

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
)

const (
	DefaultMiMoASRModel = "mimo-v2.5-asr"
	maxASRResponseBytes = 1 << 20
)

type AudioTranscriber interface {
	Transcribe(samples []float32) (string, error)
}

type MiMoASRConfig struct {
	BaseURL  string
	APIKey   string
	Model    string
	Language string
	Timeout  time.Duration
}

type MiMoASR struct {
	baseURL  string
	apiKey   string
	model    string
	language string
	http     *http.Client
}

func NewMiMoASR(config MiMoASRConfig) *MiMoASR {
	if config.Model == "" {
		config.Model = DefaultMiMoASRModel
	}
	if config.Language == "" {
		config.Language = "zh"
	}
	if config.Timeout <= 0 {
		config.Timeout = 90 * time.Second
	}
	return &MiMoASR{
		baseURL:  strings.TrimRight(config.BaseURL, "/"),
		apiKey:   config.APIKey,
		model:    config.Model,
		language: config.Language,
		http:     &http.Client{Timeout: config.Timeout},
	}
}

func (c *MiMoASR) Transcribe(samples []float32) (string, error) {
	if len(samples) == 0 {
		return "", fmt.Errorf("音频为空")
	}
	audio := base64.StdEncoding.EncodeToString(wavBytes(samples))
	requestBody := struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content []struct {
				Type       string `json:"type"`
				InputAudio struct {
					Data string `json:"data"`
				} `json:"input_audio"`
			} `json:"content"`
		} `json:"messages"`
		Stream     bool `json:"stream"`
		ASROptions struct {
			Language string `json:"language"`
		} `json:"asr_options"`
	}{Model: c.model}
	requestBody.Messages = append(requestBody.Messages, struct {
		Role    string `json:"role"`
		Content []struct {
			Type       string `json:"type"`
			InputAudio struct {
				Data string `json:"data"`
			} `json:"input_audio"`
		} `json:"content"`
	}{Role: "user"})
	requestBody.Messages[0].Content = append(requestBody.Messages[0].Content, struct {
		Type       string `json:"type"`
		InputAudio struct {
			Data string `json:"data"`
		} `json:"input_audio"`
	}{Type: "input_audio"})
	requestBody.Messages[0].Content[0].InputAudio.Data = "data:audio/wav;base64," + audio
	requestBody.ASROptions.Language = c.language

	body, err := json.Marshal(requestBody)
	if err != nil {
		return "", fmt.Errorf("encode MiMo ASR request: %w", err)
	}
	request, err := http.NewRequest(http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		request.Header.Set("api-key", c.apiKey)
		request.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return "", fmt.Errorf("MiMo ASR request failed: %w", err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxASRResponseBytes))
	if err != nil {
		return "", fmt.Errorf("read MiMo ASR response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		var payload struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &payload)
		if payload.Error.Message != "" {
			return "", fmt.Errorf("MiMo ASR returned %d: %s", response.StatusCode, payload.Error.Message)
		}
		return "", fmt.Errorf("MiMo ASR returned %d", response.StatusCode)
	}
	var payload struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", fmt.Errorf("decode MiMo ASR response: %w", err)
	}
	if len(payload.Choices) == 0 {
		return "", fmt.Errorf("MiMo ASR returned no choices")
	}
	text := strings.TrimSpace(payload.Choices[0].Message.Content)
	if text == "" {
		return "", fmt.Errorf("MiMo ASR returned empty text")
	}
	return text, nil
}

func wavBytes(samples []float32) []byte {
	dataSize := len(samples) * 2
	buffer := bytes.NewBuffer(make([]byte, 0, 44+dataSize))
	buffer.WriteString("RIFF")
	_ = binary.Write(buffer, binary.LittleEndian, uint32(36+dataSize))
	buffer.WriteString("WAVE")
	buffer.WriteString("fmt ")
	_ = binary.Write(buffer, binary.LittleEndian, uint32(16))
	_ = binary.Write(buffer, binary.LittleEndian, uint16(1))
	_ = binary.Write(buffer, binary.LittleEndian, uint16(1))
	_ = binary.Write(buffer, binary.LittleEndian, uint32(SampleRate))
	_ = binary.Write(buffer, binary.LittleEndian, uint32(SampleRate*2))
	_ = binary.Write(buffer, binary.LittleEndian, uint16(2))
	_ = binary.Write(buffer, binary.LittleEndian, uint16(16))
	buffer.WriteString("data")
	_ = binary.Write(buffer, binary.LittleEndian, uint32(dataSize))
	for _, sample := range samples {
		sample = max(-1, min(1, sample))
		_ = binary.Write(buffer, binary.LittleEndian, int16(math.Round(float64(sample*32767))))
	}
	return buffer.Bytes()
}
