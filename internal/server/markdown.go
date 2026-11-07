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

func (s *Server) handleMarkdown(w http.ResponseWriter, r *http.Request) {
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
	for i := range segments {
		segments[i].Speaker = s.speakerName(segments[i].Speaker)
	}

	var paragraphs []paragraph.Paragraph
	if s.merge && s.paragraphs != nil {
		paragraphs = s.paragraphs.All()
		for i := range paragraphs {
			paragraphs[i].Speaker = s.speakerName(paragraphs[i].Speaker)
		}
	}

	now := time.Now()
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", "diting-"+now.Format("20060102-150405")+".md"))
	}
	_, _ = w.Write([]byte(buildMarkdown(segments, paragraphs, len(segments), limit, now)))
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
	var b strings.Builder
	b.WriteString("# 谛听转录\n\n")
	if total == 0 {
		b.WriteString("> 暂无记录。\n")
		return b.String()
	}

	items := windowItems(segments, paragraphs, limit)
	if len(items) == 0 {
		b.WriteString("> 暂无记录。\n")
		return b.String()
	}

	first := items[0].start().Local()
	last := items[len(items)-1].start().Local()
	fmt.Fprintf(&b, "> %s 至 %s · 共 %d 条", first.Format("2006-01-02 15:04"), last.Format("2006-01-02 15:04"), total)
	if limit > 0 && limit < total {
		fmt.Fprintf(&b, "，此处显示最近 %d 条", limit)
	}
	fmt.Fprintf(&b, " · 导出于 %s\n", now.Local().Format("2006-01-02 15:04:05"))

	currentDate := ""
	for _, item := range items {
		date := item.start().Local().Format("2006-01-02")
		if date != currentDate {
			fmt.Fprintf(&b, "\n## %s\n\n", date)
			currentDate = date
		}
		if item.paragraph != nil {
			writeParagraph(&b, item.paragraph)
			continue
		}
		writeSegment(&b, item.segment)
	}
	return b.String()
}

func writeParagraph(b *strings.Builder, p *paragraph.Paragraph) {
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
		text = "（未识别出文字）"
	}
	b.WriteString(" · " + escapeMarkdown(text) + "\n")
}

func writeSegment(b *strings.Builder, segment *store.Segment) {
	text := strings.TrimSpace(segment.Text)
	if text == "" {
		text = "（未识别出文字）"
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
