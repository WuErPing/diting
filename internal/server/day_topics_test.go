package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"diting/internal/paragraph"
)

type fakeDayTopicsSettings struct {
	fakeSettings
	result paragraph.DayTopicsResult
	err    error
	date   string
}

func (f *fakeDayTopicsSettings) ClassifyDay(_ context.Context, date string) (paragraph.DayTopicsResult, error) {
	f.date = date
	return f.result, f.err
}

func TestHandleClassifyDayTopics(t *testing.T) {
	settings := &fakeDayTopicsSettings{result: paragraph.DayTopicsResult{
		Date: "2026-11-23", Segments: 12,
		RebuildStats: paragraph.RebuildStats{Runs: 2, LLM: 2, Paragraphs: 8},
	}}
	server := &Server{settings: settings}
	request := httptest.NewRequest(http.MethodPost, "/api/days/2026-11-23/topics", nil)
	request.SetPathValue("date", "2026-11-23")
	recorder := httptest.NewRecorder()
	server.handleClassifyDayTopics(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if settings.date != "2026-11-23" {
		t.Fatalf("date = %q", settings.date)
	}
	var response paragraph.DayTopicsResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Segments != 12 || response.Runs != 2 || response.Paragraphs != 8 {
		t.Fatalf("response = %+v", response)
	}
}

func TestHandleClassifyDayTopicsValidationAndErrors(t *testing.T) {
	settings := &fakeDayTopicsSettings{}
	server := &Server{settings: settings}
	crossOrigin := httptest.NewRequest(http.MethodPost, "/api/days/2026-11-23/topics", nil)
	crossOrigin.Header.Set("Origin", "http://evil.example")
	recorder := httptest.NewRecorder()
	server.handleClassifyDayTopics(recorder, crossOrigin)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("cross-origin status = %d", recorder.Code)
	}

	invalid := httptest.NewRequest(http.MethodPost, "/api/days/2026-02-30/topics", nil)
	invalid.SetPathValue("date", "2026-02-30")
	recorder = httptest.NewRecorder()
	server.handleClassifyDayTopics(recorder, invalid)
	if recorder.Code != http.StatusBadRequest || settings.date != "" {
		t.Fatalf("invalid date status = %d, model date = %q", recorder.Code, settings.date)
	}

	for _, test := range []struct {
		name     string
		settings *fakeDayTopicsSettings
		status   int
	}{
		{name: "unavailable", settings: &fakeDayTopicsSettings{err: paragraph.ErrDayTopicsUnavailable}, status: http.StatusServiceUnavailable},
		{name: "not found", settings: &fakeDayTopicsSettings{err: paragraph.ErrDayNotFound}, status: http.StatusNotFound},
		{name: "model failure", settings: &fakeDayTopicsSettings{err: errors.New("model unavailable")}, status: http.StatusBadGateway},
	} {
		t.Run(test.name, func(t *testing.T) {
			server = &Server{settings: test.settings}
			request := httptest.NewRequest(http.MethodPost, "/api/days/2026-11-23/topics", nil)
			request.SetPathValue("date", "2026-11-23")
			recorder = httptest.NewRecorder()
			server.handleClassifyDayTopics(recorder, request)
			if recorder.Code != test.status {
				t.Fatalf("status = %d, want %d", recorder.Code, test.status)
			}
		})
	}
}
