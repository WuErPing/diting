package engine

import (
	sherpa "github.com/k2-fsa/sherpa-onnx-go/v2/sherpa_onnx"

	"diting/internal/speaker"
)

const (
	speakerWindowSize  = SampleRate * 3
	speakerWindowHop   = SampleRate * 3 / 2
	speakerMultiWindow = SampleRate * 4
)

func ExtractSpeakerEmbedding(extractor *sherpa.SpeakerEmbeddingExtractor, samples []float32) ([]float32, float32) {
	if extractor == nil || len(samples) < SampleRate/2 {
		return nil, 0
	}
	full := extractSingleEmbedding(extractor, samples)
	if full == nil || len(samples) < speakerMultiWindow {
		return speaker.Normalize(full), 1
	}
	windows := embeddingWindows(len(samples))
	embeddings := make([][]float32, 0, len(windows))
	weights := make([]float32, 0, len(windows))
	for _, window := range windows {
		embedding := extractSingleEmbedding(extractor, samples[window[0]:window[1]])
		if embedding == nil {
			continue
		}
		embeddings = append(embeddings, embedding)
		weights = append(weights, float32(window[1]-window[0]))
	}
	return selectSpeakerEmbedding(full, embeddings, weights)
}

func selectSpeakerEmbedding(full []float32, embeddings [][]float32, weights []float32) ([]float32, float32) {
	if len(embeddings) == 0 {
		return full, 1
	}
	dominant, quality := speaker.AggregateEmbeddings(embeddings, weights)
	if quality >= 0.6 {
		return full, quality
	}
	return dominant, quality
}

func extractSingleEmbedding(extractor *sherpa.SpeakerEmbeddingExtractor, samples []float32) []float32 {
	stream := extractor.CreateStream()
	defer sherpa.DeleteOnlineStream(stream)
	stream.AcceptWaveform(SampleRate, samples)
	stream.InputFinished()
	if !extractor.IsReady(stream) {
		return nil
	}
	embedding := extractor.Compute(stream)
	if len(embedding) == 0 {
		return nil
	}
	return embedding
}

func embeddingWindows(sampleCount int) [][2]int {
	if sampleCount <= 0 {
		return nil
	}
	if sampleCount < speakerMultiWindow {
		return [][2]int{{0, sampleCount}}
	}
	windows := make([][2]int, 0, (sampleCount-speakerWindowSize)/speakerWindowHop+2)
	lastStart := -1
	for start := 0; start+speakerWindowSize < sampleCount; start += speakerWindowHop {
		windows = append(windows, [2]int{start, start + speakerWindowSize})
		lastStart = start
	}
	if finalStart := sampleCount - speakerWindowSize; finalStart > lastStart {
		windows = append(windows, [2]int{finalStart, sampleCount})
	}
	return windows
}
