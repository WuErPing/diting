package paragraph

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSettingsPersistAndApply(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	client := NewClient(ClientConfig{BaseURL: "http://127.0.0.1:1234/v1", Model: "init"})
	worker := NewWorker(WorkerOptions{Store: openTestStore(t), Corrector: &fakeCorrector{}, Model: "init", Logf: quietLogf})

	settings, err := OpenSettings(path, client, worker, Snapshot{Model: "default-model", Correct: true}, quietLogf)
	if err != nil {
		t.Fatal(err)
	}
	if got := settings.Snapshot(); got.Model != "default-model" || !got.Correct {
		t.Fatalf("snapshot = %+v", got)
	}
	if client.Model() != "default-model" || worker.currentModel() != "default-model" || !worker.correctionEnabled() {
		t.Fatalf("defaults not applied: client=%q worker=%q correction=%v", client.Model(), worker.currentModel(), worker.correctionEnabled())
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("OpenSettings should not write the file: err=%v", err)
	}

	if err := settings.Update("new-model", false); err != nil {
		t.Fatal(err)
	}
	if client.Model() != "new-model" || worker.currentModel() != "new-model" || worker.correctionEnabled() {
		t.Fatalf("update not applied: client=%q worker=%q correction=%v", client.Model(), worker.currentModel(), worker.correctionEnabled())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Model   string `json:"model"`
		Correct bool   `json:"correct"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	if file.Model != "new-model" || file.Correct {
		t.Fatalf("persisted = %+v", file)
	}

	// A fresh instance reads the persisted state and ignores the new defaults.
	client2 := NewClient(ClientConfig{Model: "init2"})
	worker2 := NewWorker(WorkerOptions{Store: openTestStore(t), Corrector: &fakeCorrector{}, Model: "init2", Logf: quietLogf})
	reopened, err := OpenSettings(path, client2, worker2, Snapshot{Model: "fallback", Correct: true}, quietLogf)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.Snapshot(); got.Model != "new-model" || got.Correct {
		t.Fatalf("reopened snapshot = %+v", got)
	}
	if client2.Model() != "new-model" || worker2.correctionEnabled() {
		t.Fatalf("reopened not applied: client=%q correction=%v", client2.Model(), worker2.correctionEnabled())
	}
}

func TestSettingsUpdateRejectsInvalidModel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	settings, err := OpenSettings(path, nil, nil, Snapshot{Model: "m", Correct: true}, quietLogf)
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range []string{"", "   ", strings.Repeat("x", maxModelRunes+1), "bad\x00name"} {
		if err := settings.Update(model, true); !errors.Is(err, ErrInvalidSettings) {
			t.Fatalf("Update(%q) error = %v, want ErrInvalidSettings", model, err)
		}
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected updates must not write the file: err=%v", err)
	}
}

type fakeASRController struct {
	provider string
}

func (f *fakeASRController) SetASRProvider(provider string) error {
	f.provider = provider
	return nil
}

func TestSettingsPersistASRProvider(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	controller := &fakeASRController{}
	settings, err := OpenSettings(path, nil, nil, Snapshot{Model: "m", ASR: "local"}, quietLogf, controller)
	if err != nil {
		t.Fatal(err)
	}
	if err := settings.Update("m", false, "mimo"); err != nil {
		t.Fatal(err)
	}
	if got := settings.Snapshot().ASR; got != "mimo" || controller.provider != "mimo" {
		t.Fatalf("ASR = %q, controller = %q", got, controller.provider)
	}
	reopened, err := OpenSettings(path, nil, nil, Snapshot{Model: "m", ASR: "local"}, quietLogf)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.Snapshot().ASR; got != "mimo" {
		t.Fatalf("reopened ASR = %q", got)
	}
	if err := settings.Update("m", false, "other"); !errors.Is(err, ErrInvalidSettings) {
		t.Fatalf("invalid ASR error = %v", err)
	}
}

func TestSettingsAvailableModelsRequiresClient(t *testing.T) {
	settings, err := OpenSettings(filepath.Join(t.TempDir(), "settings.json"), nil, nil, Snapshot{Model: "m", Correct: true}, quietLogf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := settings.AvailableModels(context.Background()); err == nil {
		t.Fatal("expected an error without a client")
	}
}
