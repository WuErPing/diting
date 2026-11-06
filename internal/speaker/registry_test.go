package speaker

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIdentifyEnrollsAndMatches(t *testing.T) {
	registry, err := Open(filepath.Join(t.TempDir(), "speakers.json"), 4, 0.6)
	if err != nil {
		t.Fatal(err)
	}

	if got := registry.Identify([]float32{1, 0, 0, 0}, 2*time.Second); got != "说话人1" {
		t.Fatalf("first speaker = %q, want 说话人1", got)
	}
	if got := registry.Identify([]float32{0.95, 0.05, 0, 0}, 2*time.Second); got != "说话人1" {
		t.Fatalf("similar embedding = %q, want 说话人1", got)
	}
	if got := registry.Identify([]float32{0, 1, 0, 0}, 2*time.Second); got != "说话人2" {
		t.Fatalf("different embedding = %q, want 说话人2", got)
	}
	if registry.Len() != 2 {
		t.Fatalf("len = %d, want 2", registry.Len())
	}
}

func TestShortSegmentDoesNotEnroll(t *testing.T) {
	registry, err := Open(filepath.Join(t.TempDir(), "speakers.json"), 4, 0.6)
	if err != nil {
		t.Fatal(err)
	}

	if got := registry.Identify([]float32{0, 0, 1, 0}, time.Second); got != "" {
		t.Fatalf("short segment = %q, want empty", got)
	}
	if registry.Len() != 0 {
		t.Fatalf("len = %d, want 0", registry.Len())
	}
}

func TestZeroEmbeddingIgnored(t *testing.T) {
	registry, err := Open(filepath.Join(t.TempDir(), "speakers.json"), 4, 0.6)
	if err != nil {
		t.Fatal(err)
	}

	if got := registry.Identify([]float32{0, 0, 0, 0}, 5*time.Second); got != "" {
		t.Fatalf("zero embedding = %q, want empty", got)
	}
	if got := registry.Identify([]float32{1}, 5*time.Second); got != "" {
		t.Fatalf("wrong dimension = %q, want empty", got)
	}
	if registry.Len() != 0 {
		t.Fatalf("len = %d, want 0", registry.Len())
	}
}

func TestRegistryRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "speakers.json")
	registry, err := Open(path, 4, 0.6)
	if err != nil {
		t.Fatal(err)
	}
	if got := registry.Identify([]float32{1, 0, 0, 0}, 2*time.Second); got != "说话人1" {
		t.Fatalf("enroll = %q", got)
	}
	if err := registry.Save(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path, 4, 0.6)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.Identify([]float32{0.9, 0.1, 0, 0}, 2*time.Second); got != "说话人1" {
		t.Fatalf("match after reopen = %q, want 说话人1", got)
	}
	if got := reopened.Identify([]float32{0, 1, 0, 0}, 2*time.Second); got != "说话人2" {
		t.Fatalf("new speaker after reopen = %q, want 说话人2", got)
	}
}

func TestDimensionMismatchAndCorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "speakers.json")
	registry, err := Open(path, 4, 0.6)
	if err != nil {
		t.Fatal(err)
	}
	registry.Identify([]float32{1, 0, 0, 0}, 2*time.Second)

	if _, err := Open(path, 8, 0.6); err == nil {
		t.Fatal("expected dimension mismatch error")
	}

	corrupt := filepath.Join(dir, "corrupt.json")
	if err := os.WriteFile(corrupt, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(corrupt, 4, 0.6); err == nil {
		t.Fatal("expected corrupt registry error")
	}
}

func TestAssignThreshold(t *testing.T) {
	profiles := []Profile{{Name: "a", Centroid: Normalize([]float32{1, 0, 0, 0})}}
	if index, _ := Assign(profiles, Normalize([]float32{1, 0, 0, 0}), 0.6); index != 0 {
		t.Fatalf("index = %d, want 0", index)
	}
	if index, _ := Assign(profiles, Normalize([]float32{0, 1, 0, 0}), 0.6); index != -1 {
		t.Fatalf("index = %d, want -1", index)
	}
}

func TestNormalizeAndCosineEdges(t *testing.T) {
	if Normalize(nil) != nil || Normalize([]float32{0, 0}) != nil {
		t.Fatal("zero vectors must normalize to nil")
	}
	if Cosine([]float32{1}, []float32{1, 2}) != 0 {
		t.Fatal("dimension mismatch must score 0")
	}
	score := Cosine(Normalize([]float32{1, 1}), Normalize([]float32{1, 1}))
	if score < 0.999 {
		t.Fatalf("self cosine = %f", score)
	}
}
