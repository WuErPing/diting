package speaker

import (
	"testing"
	"time"
)

func TestAggregateEmbeddingsSelectsDominantConsistentWindows(t *testing.T) {
	embeddings := [][]float32{
		{1, 0.05, 0},
		{0.99, 0.1, 0},
		{0.95, 0.05, 0},
		{0, 1, 0},
	}
	weights := []float32{1, 1, 1, 1}

	embedding, quality := AggregateEmbeddings(embeddings, weights)
	if quality < 0.74 || quality > 0.76 {
		t.Fatalf("quality = %f, want 0.75", quality)
	}
	if Cosine(embedding, []float32{1, 0, 0}) < 0.99 {
		t.Fatal("dominant embedding was polluted by the outlier")
	}
}

func TestAggregateEmbeddingsReportsSplitSpeakersAsLowQuality(t *testing.T) {
	embeddings := [][]float32{{1, 0}, {0.99, 0.1}, {0, 1}, {0.1, 0.99}}
	_, quality := AggregateEmbeddings(embeddings, nil)
	if quality != 0.5 {
		t.Fatalf("quality = %f, want 0.5", quality)
	}
}

func TestIdentifySkipsLowQualityAndAmbiguousUpdates(t *testing.T) {
	registry, err := Open("", 2, 0.45)
	if err != nil {
		t.Fatal(err)
	}
	registry.Identify([]float32{1, 0}, 3*time.Second)
	registry.Identify([]float32{0, 1}, 3*time.Second)

	if got := registry.IdentifyWithQuality([]float32{1, 0.05}, 3*time.Second, 0.5); got != "说话人1" {
		t.Fatalf("low quality match = %q", got)
	}
	if registry.profiles[0].Count != 1 {
		t.Fatal("low quality embedding updated a centroid")
	}
	registry.IdentifyWithQuality([]float32{1, 1}, 3*time.Second, 1)
	if registry.profiles[0].Count != 1 || registry.profiles[1].Count != 1 {
		t.Fatal("ambiguous embedding updated a centroid")
	}
	registry.IdentifyWithQuality([]float32{1, 0.05}, 2*time.Second, 1)
	if registry.profiles[0].Count != 2 {
		t.Fatal("confident multi-window embedding did not update the centroid")
	}
}
