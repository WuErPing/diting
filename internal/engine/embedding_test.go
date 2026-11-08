package engine

import "testing"

func TestEmbeddingWindowsUseWholeShortSegment(t *testing.T) {
	windows := embeddingWindows(SampleRate * 2)
	if len(windows) != 1 || windows[0] != [2]int{0, SampleRate * 2} {
		t.Fatalf("windows = %v", windows)
	}
}

func TestSelectSpeakerEmbeddingKeepsFullEmbeddingWhenWindowsUnavailable(t *testing.T) {
	full := []float32{0.9, 0.1}
	embedding, quality := selectSpeakerEmbedding(full, nil, nil)
	if quality != 1 || embedding[0] != full[0] || embedding[1] != full[1] {
		t.Fatalf("embedding = %v, quality = %f", embedding, quality)
	}
}

func TestSelectSpeakerEmbeddingKeepsFullEmbeddingWhenWindowsAgree(t *testing.T) {
	full := []float32{0.9, 0.1}
	windows := [][]float32{{1, 0.05}, {0.95, 0.1}, {0.99, 0}}
	embedding, quality := selectSpeakerEmbedding(full, windows, nil)
	if quality != 1 {
		t.Fatalf("quality = %f, want 1", quality)
	}
	if embedding[0] != full[0] || embedding[1] != full[1] {
		t.Fatal("consistent windows replaced the full-segment embedding")
	}
}

func TestSelectSpeakerEmbeddingUsesDominantWindowsWhenSpeakersSplit(t *testing.T) {
	full := []float32{0, 1}
	windows := [][]float32{{1, 0}, {0.99, 0.1}, {0, 1}, {0.1, 0.99}}
	embedding, quality := selectSpeakerEmbedding(full, windows, nil)
	if quality != 0.5 {
		t.Fatalf("quality = %f, want 0.5", quality)
	}
	if embedding[0] < 0.99 {
		t.Fatal("split speakers kept the mixed full-segment embedding")
	}
}

func TestEmbeddingWindowsCoverLongSegmentWithOverlap(t *testing.T) {
	windows := embeddingWindows(SampleRate * 5)
	want := [][2]int{
		{0, SampleRate * 3},
		{SampleRate * 3 / 2, SampleRate * 9 / 2},
		{SampleRate * 2, SampleRate * 5},
	}
	if len(windows) != len(want) {
		t.Fatalf("windows = %v, want %v", windows, want)
	}
	for i := range want {
		if windows[i] != want[i] {
			t.Fatalf("windows[%d] = %v, want %v", i, windows[i], want[i])
		}
	}
}
