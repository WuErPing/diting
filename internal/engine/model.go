package engine

import (
	"fmt"
	"path/filepath"
	"strings"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/v2/sherpa_onnx"
)

const SampleRate = 16000

type ModelPaths struct {
	ASRDir     string
	OfflineDir string
	VAD        string
}

func DefaultModelPaths(root string) ModelPaths {
	return ModelPaths{
		ASRDir:     filepath.Join(root, "sherpa-onnx-streaming-zipformer-zh-int8-2025-06-30"),
		OfflineDir: filepath.Join(root, "sherpa-onnx-sense-voice-zh-en-ja-ko-yue-int8-2024-07-17"),
		VAD:        filepath.Join(root, "silero_vad.onnx"),
	}
}

func NewOfflineRecognizer(paths ModelPaths) (*sherpa.OfflineRecognizer, error) {
	config := sherpa.OfflineRecognizerConfig{}
	config.FeatConfig = sherpa.FeatureConfig{SampleRate: SampleRate, FeatureDim: 80}
	config.ModelConfig.SenseVoice.Model = filepath.Join(paths.OfflineDir, "model.int8.onnx")
	config.ModelConfig.SenseVoice.Language = "zh"
	config.ModelConfig.SenseVoice.UseInverseTextNormalization = 1
	config.ModelConfig.Tokens = filepath.Join(paths.OfflineDir, "tokens.txt")
	config.ModelConfig.NumThreads = 2
	config.ModelConfig.Provider = "cpu"
	config.DecodingMethod = "greedy_search"

	recognizer := sherpa.NewOfflineRecognizer(&config)
	if recognizer == nil {
		return nil, fmt.Errorf("load offline recognizer from %s", paths.OfflineDir)
	}
	return recognizer, nil
}

func NewOnlineRecognizer(paths ModelPaths) (*sherpa.OnlineRecognizer, error) {
	encoder, err := modelFile(paths.ASRDir, "encoder-")
	if err != nil {
		return nil, err
	}
	decoder, err := modelFile(paths.ASRDir, "decoder-")
	if err != nil {
		return nil, err
	}
	joiner, err := modelFile(paths.ASRDir, "joiner-")
	if err != nil {
		return nil, err
	}

	config := sherpa.OnlineRecognizerConfig{}
	config.FeatConfig = sherpa.FeatureConfig{SampleRate: SampleRate, FeatureDim: 80}
	config.ModelConfig.Transducer.Encoder = encoder
	config.ModelConfig.Transducer.Decoder = decoder
	config.ModelConfig.Transducer.Joiner = joiner
	config.ModelConfig.Tokens = filepath.Join(paths.ASRDir, "tokens.txt")
	config.ModelConfig.NumThreads = 2
	config.ModelConfig.Provider = "cpu"
	config.DecodingMethod = "greedy_search"
	config.EnableEndpoint = 1
	config.Rule1MinTrailingSilence = 2.4
	config.Rule2MinTrailingSilence = 0.8
	config.Rule3MinUtteranceLength = 30

	recognizer := sherpa.NewOnlineRecognizer(&config)
	if recognizer == nil {
		return nil, fmt.Errorf("load streaming recognizer from %s", paths.ASRDir)
	}
	return recognizer, nil
}

func NewVAD(paths ModelPaths) (*sherpa.VoiceActivityDetector, error) {
	config := sherpa.VadModelConfig{}
	config.SileroVad.Model = paths.VAD
	config.SileroVad.Threshold = 0.5
	config.SileroVad.MinSilenceDuration = 0.6
	config.SileroVad.MinSpeechDuration = 0.25
	config.SileroVad.WindowSize = 512
	config.SileroVad.MaxSpeechDuration = 30
	config.SampleRate = SampleRate
	config.NumThreads = 1
	config.Provider = "cpu"

	vad := sherpa.NewVoiceActivityDetector(&config, 60)
	if vad == nil {
		return nil, fmt.Errorf("load VAD model from %s", paths.VAD)
	}
	return vad, nil
}

func modelFile(dir, prefix string) (string, error) {
	stem := strings.TrimSuffix(prefix, "-")
	matches, err := filepath.Glob(filepath.Join(dir, stem+"*.int8.onnx"))
	if err != nil || len(matches) > 0 {
		return firstMatch(matches, err)
	}
	matches, err = filepath.Glob(filepath.Join(dir, stem+"*.onnx"))
	if err != nil {
		return "", err
	}
	filtered := matches[:0]
	for _, match := range matches {
		if !strings.HasSuffix(match, ".int8.onnx") {
			filtered = append(filtered, match)
		}
	}
	return firstMatch(filtered, nil)
}

func firstMatch(matches []string, err error) (string, error) {
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("model file not found")
	}
	return matches[0], nil
}
