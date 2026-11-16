package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"diting/internal/paragraph"
)

type fakeSettings struct {
	snapshot    paragraph.Snapshot
	models      []string
	modelErr    error
	updateErr   error
	updated     bool
	lastModel   string
	lastCorrect bool
	lastASR     string
}

func (f *fakeSettings) Snapshot() paragraph.Snapshot { return f.snapshot }

func (f *fakeSettings) Update(model string, correct bool, providers ...string) error {
	f.updated = true
	f.lastModel, f.lastCorrect = model, correct
	if len(providers) > 0 {
		f.lastASR = providers[0]
	}
	if f.updateErr != nil {
		return f.updateErr
	}
	f.snapshot = paragraph.Snapshot{Model: model, Correct: correct, ASR: f.lastASR}
	return nil
}

func (f *fakeSettings) AvailableModels(context.Context) ([]string, error) {
	if f.modelErr != nil {
		return nil, f.modelErr
	}
	return f.models, nil
}

func (f *fakeSettings) CorrectRecent(context.Context, int) (paragraph.CorrectionResult, error) {
	return paragraph.CorrectionResult{}, nil
}

func (f *fakeSettings) CorrectParagraph(context.Context, string) (paragraph.CorrectionResult, error) {
	return paragraph.CorrectionResult{}, nil
}

func (f *fakeSettings) RetranscribeParagraph(context.Context, string) (paragraph.ASRResult, error) {
	return paragraph.ASRResult{}, nil
}

func (f *fakeSettings) SummarizeParagraph(context.Context, string) (paragraph.SummaryResult, error) {
	return paragraph.SummaryResult{}, nil
}

func (f *fakeSettings) SplitRecent(context.Context, int) (paragraph.RebuildStats, error) {
	return paragraph.RebuildStats{}, nil
}

func TestHandleSettings(t *testing.T) {
	settings := &fakeSettings{snapshot: paragraph.Snapshot{Model: "m", Correct: true, ASR: "mimo"}}
	server := &Server{settings: settings}

	recorder := httptest.NewRecorder()
	server.handleSettings(recorder, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var response struct {
		Available bool   `json:"available"`
		Model     string `json:"model"`
		Correct   bool   `json:"correct"`
		ASR       string `json:"asr"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.Available || response.Model != "m" || !response.Correct || response.ASR != "mimo" {
		t.Fatalf("response = %+v", response)
	}
}

func TestHandleSettingsUnavailable(t *testing.T) {
	server := &Server{}

	recorder := httptest.NewRecorder()
	server.handleSettings(recorder, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET status = %d", recorder.Code)
	}

	recorder = httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(`{"model":"m","correct":true}`))
	request.Header.Set("Content-Type", "application/json")
	server.handleSettingsUpdate(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("PUT status = %d", recorder.Code)
	}

	recorder = httptest.NewRecorder()
	server.handleModels(recorder, httptest.NewRequest(http.MethodGet, "/api/models", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("models status = %d", recorder.Code)
	}
}

func TestHandleSettingsUpdate(t *testing.T) {
	settings := &fakeSettings{snapshot: paragraph.Snapshot{Model: "old", Correct: true}}
	server := &Server{settings: settings}

	request := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(`{"model":"new","correct":false,"asr":"mimo"}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	server.handleSettingsUpdate(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if !settings.updated || settings.lastModel != "new" || settings.lastCorrect || settings.lastASR != "mimo" {
		t.Fatalf("update = %+v", settings)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `"model":"new"`) || !strings.Contains(body, `"correct":false`) {
		t.Fatalf("body = %s", body)
	}

	crossOrigin := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(`{"model":"x","correct":true}`))
	crossOrigin.Header.Set("Content-Type", "application/json")
	crossOrigin.Header.Set("Origin", "http://evil.example")
	recorder = httptest.NewRecorder()
	server.handleSettingsUpdate(recorder, crossOrigin)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("cross-origin status = %d", recorder.Code)
	}

	unknownField := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(`{"model":"x","correct":true,"extra":1}`))
	unknownField.Header.Set("Content-Type", "application/json")
	recorder = httptest.NewRecorder()
	server.handleSettingsUpdate(recorder, unknownField)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status = %d", recorder.Code)
	}

	badJSON := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(`{`))
	badJSON.Header.Set("Content-Type", "application/json")
	recorder = httptest.NewRecorder()
	server.handleSettingsUpdate(recorder, badJSON)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("bad json status = %d", recorder.Code)
	}

	wrongType := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(`{"model":"x"}`))
	recorder = httptest.NewRecorder()
	server.handleSettingsUpdate(recorder, wrongType)
	if recorder.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("wrong content type status = %d", recorder.Code)
	}
}

func TestHandleSettingsUpdateErrors(t *testing.T) {
	invalid := &fakeSettings{snapshot: paragraph.Snapshot{Model: "old"}, updateErr: paragraph.ErrInvalidSettings}
	server := &Server{settings: invalid}
	request := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(`{"model":"","correct":true}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	server.handleSettingsUpdate(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid status = %d", recorder.Code)
	}

	internal := &fakeSettings{snapshot: paragraph.Snapshot{Model: "old"}, updateErr: errors.New("disk full")}
	server = &Server{settings: internal}
	request = httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(`{"model":"new","correct":true}`))
	request.Header.Set("Content-Type", "application/json")
	recorder = httptest.NewRecorder()
	server.handleSettingsUpdate(recorder, request)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("internal status = %d", recorder.Code)
	}
}

func TestHandleModels(t *testing.T) {
	settings := &fakeSettings{snapshot: paragraph.Snapshot{Model: "a"}, models: []string{"a", "b"}}
	server := &Server{settings: settings}
	recorder := httptest.NewRecorder()
	server.handleModels(recorder, httptest.NewRequest(http.MethodGet, "/api/models", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var response struct {
		Models  []string `json:"models"`
		Current string   `json:"current"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Models) != 2 || response.Models[0] != "a" || response.Current != "a" {
		t.Fatalf("response = %+v", response)
	}

	down := &fakeSettings{snapshot: paragraph.Snapshot{Model: "a"}, modelErr: errors.New("connection refused")}
	server = &Server{settings: down}
	recorder = httptest.NewRecorder()
	server.handleModels(recorder, httptest.NewRequest(http.MethodGet, "/api/models", nil))
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("down status = %d", recorder.Code)
	}
}
