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

type fakeDayMinutesSettings struct {
	fakeSettings
	result paragraph.DayMinutesResult
	err    error
	date   string
}

func (f *fakeDayMinutesSettings) SummarizeDay(_ context.Context, date string) (paragraph.DayMinutesResult, error) {
	f.date = date
	return f.result, f.err
}

func TestHandleSummarizeDayReturnsMarkdown(t *testing.T) {
	settings := &fakeDayMinutesSettings{result: paragraph.DayMinutesResult{
		Date: "2026-11-07", Segments: 3, Markdown: "# 2026-11-07 会议纪要\n\n## 决策事项\n",
	}}
	server := &Server{settings: settings}
	request := httptest.NewRequest(http.MethodPost, "/api/days/2026-11-07/minutes", nil)
	request.SetPathValue("date", "2026-11-07")
	recorder := httptest.NewRecorder()
	server.handleSummarizeDay(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if settings.date != "2026-11-07" {
		t.Fatalf("date = %q", settings.date)
	}
	var response paragraph.DayMinutesResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Markdown != settings.result.Markdown || response.Segments != 3 {
		t.Fatalf("response = %+v", response)
	}
}

func TestHandleSummarizeDayValidationAndErrors(t *testing.T) {
	settings := &fakeDayMinutesSettings{}
	server := &Server{settings: settings}

	crossOrigin := httptest.NewRequest(http.MethodPost, "/api/days/2026-11-07/minutes", nil)
	crossOrigin.Header.Set("Origin", "http://evil.example")
	recorder := httptest.NewRecorder()
	server.handleSummarizeDay(recorder, crossOrigin)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("cross-origin status = %d", recorder.Code)
	}

	invalid := httptest.NewRequest(http.MethodPost, "/api/days/2026-02-30/minutes", nil)
	invalid.SetPathValue("date", "2026-02-30")
	recorder = httptest.NewRecorder()
	server.handleSummarizeDay(recorder, invalid)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid date status = %d", recorder.Code)
	}
	if settings.date != "" {
		t.Fatalf("rejected request reached model with date %q", settings.date)
	}

	server = &Server{}
	request := httptest.NewRequest(http.MethodPost, "/api/days/2026-11-07/minutes", nil)
	request.SetPathValue("date", "2026-11-07")
	recorder = httptest.NewRecorder()
	server.handleSummarizeDay(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable status = %d", recorder.Code)
	}

	for _, test := range []struct {
		err    error
		status int
	}{
		{err: paragraph.ErrDayNotFound, status: http.StatusNotFound},
		{err: paragraph.ErrDaySummaryUnavailable, status: http.StatusServiceUnavailable},
		{err: errors.New("model unavailable"), status: http.StatusBadGateway},
	} {
		server = &Server{settings: &fakeDayMinutesSettings{err: test.err}}
		request = httptest.NewRequest(http.MethodPost, "/api/days/2026-11-07/minutes", nil)
		request.SetPathValue("date", "2026-11-07")
		recorder = httptest.NewRecorder()
		server.handleSummarizeDay(recorder, request)
		if recorder.Code != test.status {
			t.Fatalf("error %v: status = %d, want %d", test.err, recorder.Code, test.status)
		}
	}
}
