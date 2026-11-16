package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadOpenCodeAPIKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte(`{"xiaomi":{"type":"api","key":"mimo-secret"},"other":{"key":"other-secret"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readOpenCodeAPIKey(path); got != "mimo-secret" {
		t.Fatalf("API key = %q", got)
	}
}

func TestReadOpenCodeAPIKeyMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte(`{"other":{"key":"other-secret"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readOpenCodeAPIKey(path); got != "" {
		t.Fatalf("API key = %q", got)
	}
}

func TestReadOpenCodeConfigAPIKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")
	if err := os.WriteFile(path, []byte(`{"provider":{"mimo":{"apiKey":"tp-test-key","baseURL":"https://token-plan-cn.xiaomimimo.com/v1"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readOpenCodeConfigAPIKey(path); got != "tp-test-key" {
		t.Fatalf("API key = %q", got)
	}
}

func TestReadAPIKeyFileTrimsWhitespace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "xiaomi")
	if err := os.WriteFile(path, []byte("  tp-test-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readAPIKeyFile(path); got != "tp-test-key" {
		t.Fatalf("API key = %q", got)
	}
}

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
