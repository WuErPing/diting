package server

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"diting/internal/paragraph"
	"diting/internal/store"
)

const maxMarkdownLimit = 5000

var markdownEscaper = strings.NewReplacer(
	`\`, `\\`,
	"`", "\\`",
	"*", `\*`,
	"_", `\_`,
	"[", `\[`,
	"]", `\]`,
	"<", `\<`,
	">", `\>`,
)

func requestedDate(r *http.Request) (string, bool) {
	date := r.URL.Query().Get("date")
	if date == "" {
		return "", true
	}
	parsed, err := time.ParseInLocation("2006-01-02", date, time.Local)
	return date, err == nil && parsed.Format("2006-01-02") == date
}

func (s *Server) handleMarkdown(w http.ResponseWriter, r *http.Request) {
	date, ok := requestedDate(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_DATE", "日期必须是 YYYY-MM-DD")
		return
	}
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > maxMarkdownLimit {
			writeError(w, http.StatusBadRequest, "INVALID_LIMIT", "limit 必须在 1 到 5000 之间")
			return
		}
		limit = parsed
	}

	segments, err := s.store.All()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "MARKDOWN_FAILED", "无法生成合并文稿")
		return
	}
	locale := r.URL.Query().Get("lang")
	for i := range segments {
		segments[i].Speaker = localizedSpeakerName(s.speakerName(segments[i].Speaker), locale)
	}

	var paragraphs []paragraph.Paragraph
	if s.merge && s.paragraphs != nil {
		paragraphs = s.paragraphs.All()
		for i := range paragraphs {
			paragraphs[i].Speaker = localizedSpeakerName(s.speakerName(paragraphs[i].Speaker), locale)
		}
	}

	now := time.Now()
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", "diting-"+now.Format("20060102-150405")+".md"))
	}
	_, _ = w.Write([]byte(buildMarkdownOnDate(segments, paragraphs, len(segments), limit, now, date, locale)))
}

func localizedSpeakerName(name, locale string) string {
	if locale == "en" && speakerLabelPattern.MatchString(name) {
		return "Speaker " + strings.TrimPrefix(name, "说话人")
	}
	return name
}

type markdownItem struct {
	paragraph *paragraph.Paragraph
	segment   *store.Segment
}

func (item markdownItem) start() time.Time {
	if item.paragraph != nil {
		return item.paragraph.StartedAt
	}
	return item.segment.StartedAt
}

func buildMarkdown(segments []store.Segment, paragraphs []paragraph.Paragraph, total, limit int, now time.Time) string {
	return buildMarkdownOnDate(segments, paragraphs, total, limit, now, "")
}

type markdownText struct {
	title    string
	empty    string
	rangeTo  string
	count    string
	latest   string
	exported string
	noText   string
	summary  string
}

func localizedMarkdownText(locale string) markdownText {
	if locale == "en" {
		return markdownText{
			title: "# Diting transcript\n\n", empty: "> No records yet.\n",
			rangeTo: " to ", count: " · %d records", latest: " · Showing the latest %d records",
			exported: " · Exported at %s\n", noText: "(No text recognized)", summary: "> Summary: ",
		}
	}
	return markdownText{
		title: "# 谛听转录\n\n", empty: "> 暂无记录。\n",
		rangeTo: " 至 ", count: " · 共 %d 条", latest: "，此处显示最近 %d 条",
		exported: " · 导出于 %s\n", noText: "（未识别出文字）", summary: "> 总结：",
	}
}

func buildMarkdownOnDate(segments []store.Segment, paragraphs []paragraph.Paragraph, total, limit int, now time.Time, date string, locales ...string) string {
	locale := ""
	if len(locales) > 0 {
		locale = locales[0]
	}
	text := localizedMarkdownText(locale)
	var b strings.Builder
	b.WriteString(text.title)
	if total == 0 {
		b.WriteString(text.empty)
		return b.String()
	}

	if date != "" {
		limit = 0
	}
	items := windowItems(segments, paragraphs, limit)
	if date != "" {
		selected := items[:0]
		for _, item := range items {
			if item.start().Local().Format("2006-01-02") == date {
				selected = append(selected, item)
			}
		}
		items = selected
		total = len(items)
	}
	if len(items) == 0 {
		b.WriteString(text.empty)
		return b.String()
	}

	first := items[0].start().Local()
	last := items[len(items)-1].start().Local()
	fmt.Fprintf(&b, "> %s%s%s", first.Format("2006-01-02 15:04"), text.rangeTo, last.Format("2006-01-02 15:04"))
	fmt.Fprintf(&b, text.count, total)
	if limit > 0 && limit < total {
		fmt.Fprintf(&b, text.latest, limit)
	}
	fmt.Fprintf(&b, text.exported, now.Local().Format("2006-01-02 15:04:05"))

	currentDate := ""
	for _, item := range items {
		date := item.start().Local().Format("2006-01-02")
		if date != currentDate {
			fmt.Fprintf(&b, "\n## %s\n\n", date)
			currentDate = date
		}
		if item.paragraph != nil {
			writeParagraph(&b, item.paragraph, text)
			continue
		}
		writeSegment(&b, item.segment, text)
	}
	return b.String()
}

func writeParagraph(b *strings.Builder, p *paragraph.Paragraph, localized markdownText) {
	start := p.StartedAt.Local().Format("15:04:05")
	end := p.EndedAt.Local().Format("15:04:05")
	if start == end {
		fmt.Fprintf(b, "- **%s**", start)
	} else {
		fmt.Fprintf(b, "- **%s–%s**", start, end)
	}
	if p.Speaker != "" {
		b.WriteString(" · " + escapeMarkdown(p.Speaker))
	}
	text := strings.TrimSpace(p.Text)
	if text == "" {
		text = localized.noText
	}
	b.WriteString(" · " + escapeMarkdown(text) + "\n")
	if digest := strings.TrimSpace(p.Digest); digest != "" {
		b.WriteString(localized.summary + escapeMarkdown(digest) + "\n")
	}
}

func writeSegment(b *strings.Builder, segment *store.Segment, localized markdownText) {
	text := strings.TrimSpace(segment.Text)
	if text == "" {
		text = localized.noText
	}
	fmt.Fprintf(b, "- **%s**", segment.StartedAt.Local().Format("15:04:05"))
	if segment.Speaker != "" {
		b.WriteString(" · " + escapeMarkdown(segment.Speaker))
	}
	b.WriteString(" · " + escapeMarkdown(text) + "\n")
}

// windowItems limits the document to the most recent segments while keeping any
// paragraph that intersects the window intact.
func windowItems(segments []store.Segment, paragraphs []paragraph.Paragraph, limit int) []markdownItem {
	start := 0
	if limit > 0 && limit < len(segments) {
		start = len(segments) - limit
	}
	inWindow := make(map[string]struct{}, len(segments)-start)
	for _, segment := range segments[start:] {
		inWindow[segment.ID] = struct{}{}
	}

	owner := make(map[string]*paragraph.Paragraph)
	for i := range paragraphs {
		for _, id := range paragraphs[i].SegmentIDs {
			owner[id] = &paragraphs[i]
		}
	}

	items := make([]markdownItem, 0, len(segments))
	emitted := make(map[string]struct{})
	for i := range segments {
		segment := &segments[i]
		if p, ok := owner[segment.ID]; ok {
			if _, done := emitted[p.ID]; done {
				continue
			}
			included := false
			for _, id := range p.SegmentIDs {
				if _, ok := inWindow[id]; ok {
					included = true
					break
				}
			}
			if !included {
				continue
			}
			emitted[p.ID] = struct{}{}
			items = append(items, markdownItem{paragraph: p})
			continue
		}
		if _, ok := inWindow[segment.ID]; !ok {
			continue
		}
		items = append(items, markdownItem{segment: segment})
	}
	return items
}

func escapeMarkdown(value string) string {
	value = strings.ReplaceAll(value, "\r\n", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	return markdownEscaper.Replace(value)
}
