package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"diting/internal/paragraph"
	"diting/internal/store"
)

func TestBuildMarkdownGroupsByDateAndEscapes(t *testing.T) {
	now := time.Date(2026, 11, 6, 22, 0, 0, 0, time.Local)
	segments := []store.Segment{
		{ID: "a", StartedAt: time.Date(2026, 11, 6, 23, 59, 0, 0, time.Local), Speaker: "说话人1", Text: "a*b_[c]`d`"},
		{ID: "b", StartedAt: time.Date(2026, 11, 7, 0, 1, 0, 0, time.Local), Text: ""},
	}

	document := buildMarkdown(segments, nil, 2, 0, now)
	for _, want := range []string{
		"# 谛听转录",
		"## 2026-11-06",
		"## 2026-11-07",
		"- **23:59:00** · 说话人1 · a\\*b\\_\\[c\\]\\`d\\`",
		"- **00:01:00** · （未识别出文字）",
	} {
		if !strings.Contains(document, want) {
			t.Fatalf("markdown missing %q:\n%s", want, document)
		}
	}
	if strings.Contains(document, "最近 2 条") {
		t.Fatalf("unexpected truncation note:\n%s", document)
	}
}

func TestBuildMarkdownMarksTruncatedPreview(t *testing.T) {
	now := time.Date(2026, 11, 6, 22, 0, 0, 0, time.Local)
	segments := []store.Segment{
		{ID: "b", StartedAt: time.Date(2026, 11, 6, 20, 0, 0, 0, time.Local), Text: "第二条"},
	}

	document := buildMarkdown(segments, nil, 5, 1, now)
	if !strings.Contains(document, "共 5 条，此处显示最近 1 条") {
		t.Fatalf("missing truncation note:\n%s", document)
	}
}

func TestBuildMarkdownEmpty(t *testing.T) {
	document := buildMarkdown(nil, nil, 0, 0, time.Now())
	if !strings.Contains(document, "暂无记录") {
		t.Fatalf("empty markdown = %q", document)
	}
}

func TestHandleMarkdownLimitAndDownload(t *testing.T) {
	segmentStore, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer segmentStore.Close()

	base := time.Date(2026, 11, 6, 14, 0, 0, 0, time.Local)
	if err := segmentStore.Rewrite([]store.Segment{
		{ID: "a", StartedAt: base, Text: "第一条"},
		{ID: "b", StartedAt: base.Add(time.Minute), Text: "第二条"},
		{ID: "c", StartedAt: base.Add(2 * time.Minute), Speaker: "说话人2", Text: "第三条"},
	}); err != nil {
		t.Fatal(err)
	}

	server := &Server{store: segmentStore}
	request := httptest.NewRequest(http.MethodGet, "/api/markdown?limit=2&download=1", nil)
	recorder := httptest.NewRecorder()
	server.handleMarkdown(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if contentType := recorder.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "text/markdown") {
		t.Fatalf("content type = %q", contentType)
	}
	if disposition := recorder.Header().Get("Content-Disposition"); !strings.Contains(disposition, "attachment") || !strings.HasSuffix(disposition, ".md\"") {
		t.Fatalf("content disposition = %q", disposition)
	}
	body := recorder.Body.String()
	if strings.Contains(body, "第一条") {
		t.Fatalf("limit did not trim oldest record:\n%s", body)
	}
	for _, want := range []string{"第二条", "第三条", "最近 2 条", "说话人2"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q:\n%s", want, body)
		}
	}
}

func TestHandleMarkdownRejectsInvalidLimit(t *testing.T) {
	server := &Server{store: nil}
	request := httptest.NewRequest(http.MethodGet, "/api/markdown?limit=0", nil)
	recorder := httptest.NewRecorder()
	server.handleMarkdown(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestBuildMarkdownRendersParagraphs(t *testing.T) {
	now := time.Date(2026, 11, 6, 22, 0, 0, 0, time.Local)
	base := time.Date(2026, 11, 6, 14, 0, 0, 0, time.Local)
	segments := []store.Segment{
		{ID: "a", StartedAt: base, EndedAt: base.Add(4 * time.Second), Speaker: "说话人1", Text: "第一句。"},
		{ID: "b", StartedAt: base.Add(5 * time.Second), EndedAt: base.Add(9 * time.Second), Speaker: "说话人1", Text: "第二句。"},
		{ID: "c", StartedAt: base.Add(10 * time.Second), EndedAt: base.Add(14 * time.Second), Speaker: "说话人1", Text: "第三句。"},
	}
	paragraphs := []paragraph.Paragraph{{
		ID:         "a",
		StartedAt:  segments[0].StartedAt,
		EndedAt:    segments[1].EndedAt,
		Speaker:    "说话人1",
		Text:       "第一句。第二句。",
		SegmentIDs: []string{"a", "b"},
		Source:     paragraph.SourceLLM,
	}}

	document := buildMarkdown(segments, paragraphs, 3, 0, now)
	if !strings.Contains(document, "- **14:00:00–14:00:09** · 说话人1 · 第一句。第二句。") {
		t.Fatalf("missing merged bullet:\n%s", document)
	}
	if strings.Contains(document, "- **14:00:05**") {
		t.Fatalf("covered segment rendered separately:\n%s", document)
	}
	if !strings.Contains(document, "- **14:00:10** · 说话人1 · 第三句。") {
		t.Fatalf("uncovered segment missing:\n%s", document)
	}
}

func TestBuildMarkdownKeepsBoundaryParagraphWhole(t *testing.T) {
	now := time.Date(2026, 11, 6, 22, 0, 0, 0, time.Local)
	base := time.Date(2026, 11, 6, 14, 0, 0, 0, time.Local)
	segments := []store.Segment{
		{ID: "a", StartedAt: base, EndedAt: base.Add(4 * time.Second), Text: "第一句。"},
		{ID: "b", StartedAt: base.Add(5 * time.Second), EndedAt: base.Add(9 * time.Second), Text: "第二句。"},
		{ID: "c", StartedAt: base.Add(10 * time.Second), EndedAt: base.Add(14 * time.Second), Text: "第三句。"},
	}
	paragraphs := []paragraph.Paragraph{{
		ID:         "a",
		StartedAt:  segments[0].StartedAt,
		EndedAt:    segments[2].EndedAt,
		Text:       "第一句。第二句。第三句。",
		SegmentIDs: []string{"a", "b", "c"},
	}}

	document := buildMarkdown(segments, paragraphs, 3, 2, now)
	if !strings.Contains(document, "- **14:00:00–14:00:14** · 第一句。第二句。第三句。") {
		t.Fatalf("boundary paragraph was split by limit:\n%s", document)
	}
	if !strings.Contains(document, "最近 2 条") {
		t.Fatalf("missing truncation note:\n%s", document)
	}
}

func TestBuildMarkdownParagraphEscapesAndEmpty(t *testing.T) {
	now := time.Date(2026, 11, 6, 22, 0, 0, 0, time.Local)
	base := time.Date(2026, 11, 6, 14, 0, 0, 0, time.Local)
	segments := []store.Segment{
		{ID: "a", StartedAt: base, Text: "a*b_[c]`d`"},
		{ID: "b", StartedAt: base.Add(time.Minute), Text: ""},
	}
	paragraphs := []paragraph.Paragraph{
		{ID: "a", StartedAt: base, EndedAt: base, Speaker: "说话人1", Text: "a*b_[c]`d`", SegmentIDs: []string{"a"}},
		{ID: "b", StartedAt: base.Add(time.Minute), EndedAt: base.Add(time.Minute), Text: "", SegmentIDs: []string{"b"}},
	}

	document := buildMarkdown(segments, paragraphs, 2, 0, now)
	if !strings.Contains(document, "- **14:00:00** · 说话人1 · a\\*b\\_\\[c\\]\\`d\\`") {
		t.Fatalf("paragraph not escaped:\n%s", document)
	}
	if !strings.Contains(document, "- **14:01:00** · （未识别出文字）") {
		t.Fatalf("empty paragraph missing placeholder:\n%s", document)
	}
}

func TestBuildMarkdownParagraphDateFromStart(t *testing.T) {
	now := time.Date(2026, 11, 6, 22, 0, 0, 0, time.Local)
	start := time.Date(2026, 11, 6, 23, 59, 0, 0, time.Local)
	end := time.Date(2026, 11, 7, 0, 1, 0, 0, time.Local)
	segments := []store.Segment{
		{ID: "a", StartedAt: start, EndedAt: start.Add(4 * time.Second), Text: "跨午夜。"},
		{ID: "b", StartedAt: end.Add(-4 * time.Second), EndedAt: end, Text: "继续。"},
	}
	paragraphs := []paragraph.Paragraph{{
		ID: "a", StartedAt: start, EndedAt: end, Text: "跨午夜。继续。", SegmentIDs: []string{"a", "b"},
	}}

	document := buildMarkdown(segments, paragraphs, 2, 0, now)
	if !strings.Contains(document, "## 2026-11-06") {
		t.Fatalf("missing start date heading:\n%s", document)
	}
	if strings.Contains(document, "## 2026-11-07") {
		t.Fatalf("paragraph drifted to end date:\n%s", document)
	}
	if !strings.Contains(document, "23:59:00–00:01:00") {
		t.Fatalf("missing cross-midnight range:\n%s", document)
	}
}

func TestHandleMarkdownUsesParagraphsOnlyWhenMerged(t *testing.T) {
	segmentStore, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer segmentStore.Close()
	base := time.Date(2026, 11, 6, 14, 0, 0, 0, time.Local)
	if err := segmentStore.Rewrite([]store.Segment{
		{ID: "a", StartedAt: base, EndedAt: base.Add(time.Second), Text: "第一条"},
		{ID: "b", StartedAt: base.Add(2 * time.Second), EndedAt: base.Add(3 * time.Second), Text: "第二条"},
	}); err != nil {
		t.Fatal(err)
	}
	paragraphStore, err := paragraph.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := paragraphStore.SaveMany([]paragraph.Paragraph{{
		ID: "a", StartedAt: base, EndedAt: base.Add(3 * time.Second), Text: "第一条第二条",
		SegmentIDs: []string{"a", "b"},
	}}); err != nil {
		t.Fatal(err)
	}

	merged := &Server{store: segmentStore, paragraphs: paragraphStore, merge: true}
	recorder := httptest.NewRecorder()
	merged.handleMarkdown(recorder, httptest.NewRequest(http.MethodGet, "/api/markdown", nil))
	if body := recorder.Body.String(); !strings.Contains(body, "第一条第二条") {
		t.Fatalf("paragraph not used:\n%s", body)
	}

	legacy := &Server{store: segmentStore, paragraphs: paragraphStore, merge: false}
	recorder = httptest.NewRecorder()
	legacy.handleMarkdown(recorder, httptest.NewRequest(http.MethodGet, "/api/markdown", nil))
	body := recorder.Body.String()
	if strings.Contains(body, "第一条第二条") {
		t.Fatalf("merge=false still used paragraphs:\n%s", body)
	}
	for _, want := range []string{"第一条", "第二条"} {
		if !strings.Contains(body, want) {
			t.Fatalf("legacy output missing %q:\n%s", want, body)
		}
	}
}
