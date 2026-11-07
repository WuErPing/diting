package speaker

import (
	"errors"
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

	if got := registry.Identify([]float32{1, 0, 0, 0}, 3*time.Second); got != "说话人1" {
		t.Fatalf("first speaker = %q, want 说话人1", got)
	}
	if got := registry.Identify([]float32{0.95, 0.05, 0, 0}, 2*time.Second); got != "说话人1" {
		t.Fatalf("similar embedding = %q, want 说话人1", got)
	}
	if got := registry.Identify([]float32{0, 1, 0, 0}, 3*time.Second); got != "说话人2" {
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
	if got := registry.Identify([]float32{1, 0, 0, 0}, 3*time.Second); got != "说话人1" {
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
	if got := reopened.Identify([]float32{0, 1, 0, 0}, 3*time.Second); got != "说话人2" {
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
	registry.Identify([]float32{1, 0, 0, 0}, 3*time.Second)

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

func TestShortMatchDoesNotUpdateCentroid(t *testing.T) {
	registry, err := Open("", 4, 0.6)
	if err != nil {
		t.Fatal(err)
	}
	if got := registry.Identify([]float32{1, 0, 0, 0}, 3*time.Second); got != "说话人1" {
		t.Fatalf("enroll = %q", got)
	}
	before := append([]float32(nil), registry.profiles[0].Centroid...)
	if got := registry.Identify([]float32{0.8, 0.6, 0, 0}, time.Second); got != "说话人1" {
		t.Fatalf("short match = %q", got)
	}
	if registry.profiles[0].Count != 1 {
		t.Fatalf("count = %d, want 1", registry.profiles[0].Count)
	}
	if Cosine(before, registry.profiles[0].Centroid) < 0.999999 {
		t.Fatal("short segment changed the centroid")
	}
}

func TestUncertainMatchReusesSpeakerWithoutEnrolling(t *testing.T) {
	registry, err := Open("", 4, 0.6)
	if err != nil {
		t.Fatal(err)
	}
	registry.Identify([]float32{1, 0, 0, 0}, 3*time.Second)
	if got := registry.Identify([]float32{0.55, 0.835, 0, 0}, 3*time.Second); got != "说话人1" {
		t.Fatalf("uncertain match = %q, want 说话人1", got)
	}
	if registry.Len() != 1 {
		t.Fatalf("speakers = %d, want 1", registry.Len())
	}
	if registry.profiles[0].Count != 1 {
		t.Fatalf("count = %d, want 1 (uncertain matches must not update)", registry.profiles[0].Count)
	}
	if got := registry.Identify([]float32{0.4, 0.9165, 0, 0}, time.Second); got != "" {
		t.Fatalf("low score short segment = %q, want empty", got)
	}
	if got := registry.Identify([]float32{0, 1, 0, 0}, 3*time.Second); got != "说话人2" {
		t.Fatalf("clearly different = %q, want 说话人2", got)
	}
}

func TestBlendWeightIsCapped(t *testing.T) {
	sum := blend([]float32{1, 0}, 100, []float32{0, 1})
	if sum[0] != maxBlendWeight || sum[1] != 1 {
		t.Fatalf("blend = %v", sum)
	}
}

func TestAliasRoundTripAndValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "speakers.json")
	registry, err := Open(path, 4, 0.6)
	if err != nil {
		t.Fatal(err)
	}
	registry.Identify([]float32{1, 0, 0, 0}, 3*time.Second)
	registry.Identify([]float32{0, 1, 0, 0}, 3*time.Second)

	if err := registry.SetAlias("说话人1", " 章总 "); err != nil {
		t.Fatal(err)
	}
	if got := registry.DisplayName("说话人1"); got != "章总" {
		t.Fatalf("display = %q", got)
	}
	if got := registry.DisplayName("说话人2"); got != "说话人2" {
		t.Fatalf("display = %q", got)
	}
	if err := registry.SetAlias("说话人2", "说话人1"); !errors.Is(err, ErrInvalidAlias) {
		t.Fatalf("label collision err = %v", err)
	}
	if err := registry.SetAlias("说话人9", "无名"); !errors.Is(err, ErrUnknownSpeaker) {
		t.Fatalf("unknown speaker err = %v", err)
	}
	if err := registry.SetAlias("说话人2", "a\nb"); !errors.Is(err, ErrInvalidAlias) {
		t.Fatalf("control character err = %v", err)
	}
	// Duplicate alias auto-merges into the speaker that already has it.
	if err := registry.SetAlias("说话人2", "章总"); err != nil {
		t.Fatalf("duplicate alias should auto-merge, got err = %v", err)
	}
	if got := registry.CanonicalLabel("说话人2"); got != "说话人1" {
		t.Fatalf("canonical after auto-merge = %q, want 说话人1", got)
	}
	if got := registry.DisplayName("说话人2"); got != "章总" {
		t.Fatalf("display after auto-merge = %q, want 章总", got)
	}
	// Merged speaker rejects further alias changes.
	if err := registry.SetAlias("说话人2", "别的名字"); !errors.Is(err, ErrMergedSpeaker) {
		t.Fatalf("merged speaker alias err = %v", err)
	}

	reopened, err := Open(path, 4, 0.6)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.DisplayName("说话人1"); got != "章总" {
		t.Fatalf("alias after reopen = %q", got)
	}
	views := reopened.Views()
	if len(views) != 2 || views[0].Name != "章总" || views[1].Name != "章总" {
		t.Fatalf("views = %+v", views)
	}
	if views[1].MergedInto != "说话人1" {
		t.Fatalf("views[1].MergedInto = %q, want 说话人1", views[1].MergedInto)
	}

	if err := reopened.SetAlias("说话人1", ""); err != nil {
		t.Fatal(err)
	}
	if got := reopened.DisplayName("说话人1"); got != "说话人1" {
		t.Fatalf("cleared alias = %q", got)
	}
}
