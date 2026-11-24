package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"diting/internal/paragraph"
	"diting/internal/store"
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

func TestHandleSummarizeDayPersistsMinutes(t *testing.T) {
	server, segmentStore := newTestServerWithSegments(t)
	server.settings = &fakeDayMinutesSettings{result: paragraph.DayMinutesResult{
		Date: "2026-11-07", Segments: 1, Markdown: "# 2026-11-07 会议纪要\n\n## 决策事项\n",
	}}

	request := httptest.NewRequest(http.MethodPost, "/api/days/2026-11-07/minutes", nil)
	request.SetPathValue("date", "2026-11-07")
	recorder := httptest.NewRecorder()
	server.handleSummarizeDay(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var generated store.Minutes
	if err := json.Unmarshal(recorder.Body.Bytes(), &generated); err != nil {
		t.Fatal(err)
	}
	if generated.GeneratedAt.IsZero() {
		t.Fatalf("generatedAt missing: %+v", generated)
	}

	saved, err := segmentStore.DayMinutes("2026-11-07")
	if err != nil {
		t.Fatalf("minutes not persisted: %v", err)
	}
	if saved.Markdown != generated.Markdown || saved.Segments != 1 {
		t.Fatalf("saved = %+v", saved)
	}

	get := httptest.NewRequest(http.MethodGet, "/api/days/2026-11-07/minutes", nil)
	get.SetPathValue("date", "2026-11-07")
	recorder = httptest.NewRecorder()
	server.handleGetDayMinutes(recorder, get)
	if recorder.Code != http.StatusOK {
		t.Fatalf("get status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var loaded store.Minutes
	if err := json.Unmarshal(recorder.Body.Bytes(), &loaded); err != nil {
		t.Fatal(err)
	}
	if loaded.Markdown != saved.Markdown || !loaded.GeneratedAt.Equal(saved.GeneratedAt) {
		t.Fatalf("loaded = %+v, saved = %+v", loaded, saved)
	}
}

func TestHandleGetDayMinutesValidationAndMissing(t *testing.T) {
	server, _ := newTestServerWithSegments(t)

	invalid := httptest.NewRequest(http.MethodGet, "/api/days/2026-02-30/minutes", nil)
	invalid.SetPathValue("date", "2026-02-30")
	recorder := httptest.NewRecorder()
	server.handleGetDayMinutes(recorder, invalid)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid date status = %d", recorder.Code)
	}

	missing := httptest.NewRequest(http.MethodGet, "/api/days/2026-11-07/minutes", nil)
	missing.SetPathValue("date", "2026-11-07")
	recorder = httptest.NewRecorder()
	server.handleGetDayMinutes(recorder, missing)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("missing status = %d, body = %s", recorder.Code, recorder.Body)
	}

	unavailable := &Server{}
	recorder = httptest.NewRecorder()
	unavailable.handleGetDayMinutes(recorder, missing)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable status = %d", recorder.Code)
	}
}

func TestHandleGetDayMinutesRejectsCorruptData(t *testing.T) {
	server, segmentStore := newTestServerWithSegments(t)
	if _, err := segmentStore.SaveMinutes("2026-11-07", 1, "# 纪要"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(segmentStore.Root(), "minutes", "2026-11-07.json")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/days/2026-11-07/minutes", nil)
	request.SetPathValue("date", "2026-11-07")
	recorder := httptest.NewRecorder()
	server.handleGetDayMinutes(recorder, request)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("corrupt data status = %d, body = %s", recorder.Code, recorder.Body)
	}
}

func TestHandleDaysIncludesMinutesFlag(t *testing.T) {
	server, segmentStore := newTestServerWithSegments(t)
	if _, err := segmentStore.SaveMinutes("2026-11-07", 1, "# 2026-11-07 会议纪要\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := segmentStore.SaveMinutes("2026-11-09", 2, "# 2026-11-09 会议纪要\n"); err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	server.handleDays(recorder, httptest.NewRequest(http.MethodGet, "/api/days", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var response daysResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Days) != 3 {
		t.Fatalf("days = %+v", response.Days)
	}
	byDate := map[string]dayView{}
	for _, day := range response.Days {
		byDate[day.Date] = day
	}
	if byDate["2026-11-06"].Minutes {
		t.Fatal("2026-11-06 should not have minutes")
	}
	if !byDate["2026-11-07"].Minutes || byDate["2026-11-07"].Segments != 1 {
		t.Fatalf("2026-11-07 = %+v", byDate["2026-11-07"])
	}
	if !byDate["2026-11-09"].Minutes || byDate["2026-11-09"].Segments != 0 {
		t.Fatalf("minutes-only day = %+v", byDate["2026-11-09"])
	}
}

func TestHandleDeleteDayRemovesMinutes(t *testing.T) {
	server, segmentStore := newTestServerWithSegments(t)
	t.Setenv("DITING_TRASH_DIR", t.TempDir())
	if _, err := segmentStore.SaveMinutes("2026-11-07", 1, "# 2026-11-07 会议纪要\n"); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodDelete, "/api/days/2026-11-07", nil)
	request.SetPathValue("date", "2026-11-07")
	recorder := httptest.NewRecorder()
	server.handleDeleteDay(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if _, err := segmentStore.DayMinutes("2026-11-07"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted minutes error = %v", err)
	}

	dryRun := httptest.NewRequest(http.MethodDelete, "/api/days/2026-11-06?dryRun=1", nil)
	dryRun.SetPathValue("date", "2026-11-06")
	if _, err := segmentStore.SaveMinutes("2026-11-06", 1, "# 2026-11-06 会议纪要\n"); err != nil {
		t.Fatal(err)
	}
	recorder = httptest.NewRecorder()
	server.handleDeleteDay(recorder, dryRun)
	if recorder.Code != http.StatusOK {
		t.Fatalf("dry-run status = %d", recorder.Code)
	}
	if _, err := segmentStore.DayMinutes("2026-11-06"); err != nil {
		t.Fatalf("dry-run removed minutes: %v", err)
	}
}
