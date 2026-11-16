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

type fakeCorrectionSettings struct {
	result      paragraph.CorrectionResult
	err         error
	limit       int
	paragraphID string
	calls       int
	splitResult paragraph.RebuildStats
	splitErr    error
	splitLimit  int
	splitCalls  int
}

func (f *fakeCorrectionSettings) Snapshot() paragraph.Snapshot {
	return paragraph.Snapshot{Model: "m", Correct: false}
}

func (f *fakeCorrectionSettings) Update(string, bool, ...string) error { return nil }

func (f *fakeCorrectionSettings) AvailableModels(context.Context) ([]string, error) {
	return nil, nil
}

func (f *fakeCorrectionSettings) CorrectRecent(_ context.Context, limit int) (paragraph.CorrectionResult, error) {
	f.calls++
	f.limit = limit
	return f.result, f.err
}

func (f *fakeCorrectionSettings) CorrectParagraph(_ context.Context, id string) (paragraph.CorrectionResult, error) {
	f.calls++
	f.paragraphID = id
	return f.result, f.err
}

func (f *fakeCorrectionSettings) RetranscribeParagraph(_ context.Context, id string) (paragraph.ASRResult, error) {
	f.calls++
	f.paragraphID = id
	if f.err != nil {
		return paragraph.ASRResult{}, f.err
	}
	return paragraph.ASRResult{Paragraphs: 1, Segments: 2, Changed: 2}, nil
}

func (f *fakeCorrectionSettings) SummarizeParagraph(_ context.Context, id string) (paragraph.SummaryResult, error) {
	f.calls++
	f.paragraphID = id
	if f.err != nil {
		return paragraph.SummaryResult{}, f.err
	}
	return paragraph.SummaryResult{Paragraphs: 1, Summary: "测试总结"}, nil
}

func (f *fakeCorrectionSettings) SplitRecent(_ context.Context, limit int) (paragraph.RebuildStats, error) {
	f.splitCalls++
	f.splitLimit = limit
	return f.splitResult, f.splitErr
}

func TestHandleSplitParagraphs(t *testing.T) {
	settings := &fakeCorrectionSettings{splitResult: paragraph.RebuildStats{Runs: 2, LLM: 2, Paragraphs: 5}}
	server := &Server{settings: settings}
	request := httptest.NewRequest(http.MethodPost, "/api/paragraphs/split", strings.NewReader(`{"limit":250}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	server.handleSplitParagraphs(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if settings.splitCalls != 1 || settings.splitLimit != 250 {
		t.Fatalf("calls = %d, limit = %d", settings.splitCalls, settings.splitLimit)
	}
	var response paragraph.RebuildStats
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Runs != 2 || response.LLM != 2 || response.Paragraphs != 5 {
		t.Fatalf("response = %+v", response)
	}
}

func TestHandleSplitParagraphsValidation(t *testing.T) {
	settings := &fakeCorrectionSettings{}
	server := &Server{settings: settings}

	crossOrigin := httptest.NewRequest(http.MethodPost, "/api/paragraphs/split", strings.NewReader(`{}`))
	crossOrigin.Header.Set("Content-Type", "application/json")
	crossOrigin.Header.Set("Origin", "http://evil.example")
	recorder := httptest.NewRecorder()
	server.handleSplitParagraphs(recorder, crossOrigin)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("cross-origin status = %d", recorder.Code)
	}

	invalid := httptest.NewRequest(http.MethodPost, "/api/paragraphs/split", strings.NewReader(`{"limit":0}`))
	invalid.Header.Set("Content-Type", "application/json")
	recorder = httptest.NewRecorder()
	server.handleSplitParagraphs(recorder, invalid)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid limit status = %d", recorder.Code)
	}
	if settings.splitCalls != 0 {
		t.Fatalf("rejected requests reached splitter %d times", settings.splitCalls)
	}
}

func TestHandleSplitParagraphsErrors(t *testing.T) {
	server := &Server{}
	request := httptest.NewRequest(http.MethodPost, "/api/paragraphs/split", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	server.handleSplitParagraphs(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable status = %d", recorder.Code)
	}

	settings := &fakeCorrectionSettings{splitErr: errors.New("model unavailable")}
	server = &Server{settings: settings}
	request = httptest.NewRequest(http.MethodPost, "/api/paragraphs/split", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	recorder = httptest.NewRecorder()
	server.handleSplitParagraphs(recorder, request)
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("split failure status = %d", recorder.Code)
	}
	if settings.splitLimit != 500 {
		t.Fatalf("default limit = %d", settings.splitLimit)
	}
}

func TestHandleCorrectParagraphs(t *testing.T) {
	settings := &fakeCorrectionSettings{result: paragraph.CorrectionResult{Paragraphs: 3, Changed: 2}}
	server := &Server{settings: settings}
	request := httptest.NewRequest(http.MethodPost, "/api/paragraphs/correct", strings.NewReader(`{"limit":500}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	server.handleCorrectParagraphs(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if settings.calls != 1 || settings.limit != 500 {
		t.Fatalf("calls = %d, limit = %d", settings.calls, settings.limit)
	}
	var response paragraph.CorrectionResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Paragraphs != 3 || response.Changed != 2 {
		t.Fatalf("response = %+v", response)
	}
}

func TestHandleCorrectParagraphsValidation(t *testing.T) {
	settings := &fakeCorrectionSettings{}
	server := &Server{settings: settings}

	crossOrigin := httptest.NewRequest(http.MethodPost, "/api/paragraphs/correct", strings.NewReader(`{"limit":500}`))
	crossOrigin.Header.Set("Content-Type", "application/json")
	crossOrigin.Header.Set("Origin", "http://evil.example")
	recorder := httptest.NewRecorder()
	server.handleCorrectParagraphs(recorder, crossOrigin)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("cross-origin status = %d", recorder.Code)
	}

	invalid := httptest.NewRequest(http.MethodPost, "/api/paragraphs/correct", strings.NewReader(`{"limit":0}`))
	invalid.Header.Set("Content-Type", "application/json")
	recorder = httptest.NewRecorder()
	server.handleCorrectParagraphs(recorder, invalid)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid limit status = %d", recorder.Code)
	}
	if settings.calls != 0 {
		t.Fatalf("rejected requests reached corrector %d times", settings.calls)
	}
}

func TestHandleCorrectParagraphsErrors(t *testing.T) {
	server := &Server{}
	request := httptest.NewRequest(http.MethodPost, "/api/paragraphs/correct", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	server.handleCorrectParagraphs(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable status = %d", recorder.Code)
	}

	settings := &fakeCorrectionSettings{err: errors.New("model unavailable")}
	server = &Server{settings: settings}
	request = httptest.NewRequest(http.MethodPost, "/api/paragraphs/correct", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	recorder = httptest.NewRecorder()
	server.handleCorrectParagraphs(recorder, request)
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("correction failure status = %d", recorder.Code)
	}
	if settings.limit != 500 {
		t.Fatalf("default limit = %d", settings.limit)
	}
}
