package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSpeakerRegistryDimension(t *testing.T) {
	path := filepath.Join(t.TempDir(), "speakers.json")
	if err := os.WriteFile(path, []byte(`{"dim":512}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := speakerRegistryDimension(path, 192); got != 512 {
		t.Fatalf("dimension = %d, want 512", got)
	}
}

func TestSpeakerRegistryDimensionUsesFallbackForMissingOrInvalidFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "speakers.json")
	if got := speakerRegistryDimension(path, 192); got != 192 {
		t.Fatalf("missing file dimension = %d, want 192", got)
	}
	if err := os.WriteFile(path, []byte(`{"dim":0}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := speakerRegistryDimension(path, 192); got != 192 {
		t.Fatalf("invalid dimension = %d, want 192", got)
	}
}
