package paragraph

import (
	"fmt"
	"strings"
	"time"

	"diting/internal/store"
)

const (
	SourceLLM      = "llm"
	SourceSingle   = "single"
	SourceFallback = "fallback"
)

const (
	defaultIdleTimeout = 60 * time.Second
	defaultMaxSegments = 20
	defaultMaxChars    = 3000
)

type Paragraph struct {
	ID         string    `json:"id"`
	StartedAt  time.Time `json:"startedAt"`
	EndedAt    time.Time `json:"endedAt"`
	Speaker    string    `json:"speaker,omitempty"`
	Text       string    `json:"text"`
	SegmentIDs []string  `json:"segmentIds"`
	Source     string    `json:"source"`
	Model      string    `json:"model,omitempty"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

func (p Paragraph) SegmentCount() int {
	return len(p.SegmentIDs)
}

type Group struct {
	Start int
	End   int
}

type RunOptions struct {
	IdleTimeout time.Duration
	MaxSegments int
	MaxChars    int
}

func (o RunOptions) withDefaults() RunOptions {
	if o.IdleTimeout <= 0 {
		o.IdleTimeout = defaultIdleTimeout
	}
	if o.MaxSegments <= 0 {
		o.MaxSegments = defaultMaxSegments
	}
	if o.MaxChars <= 0 {
		o.MaxChars = defaultMaxChars
	}
	return o
}

// BuildRuns splits chronologically ordered segments into runs that share a
// speaker and contain no pause longer than IdleTimeout. Segment and character
// caps bound the prompt size; an oversized single segment still forms a run.
func BuildRuns(segments []store.Segment, opts RunOptions) [][]store.Segment {
	opts = opts.withDefaults()
	var runs [][]store.Segment
	var current []store.Segment
	chars := 0
	for _, segment := range segments {
		length := len([]rune(strings.TrimSpace(segment.Text)))
		if len(current) > 0 {
			prev := current[len(current)-1]
			gap := segment.StartedAt.Sub(prev.EndedAt)
			if segment.Speaker != prev.Speaker ||
				gap > opts.IdleTimeout ||
				len(current)+1 > opts.MaxSegments ||
				chars+length > opts.MaxChars {
				runs = append(runs, current)
				current = nil
				chars = 0
			}
		}
		current = append(current, segment)
		chars += length
	}
	if len(current) > 0 {
		runs = append(runs, current)
	}
	return runs
}

// JoinTexts concatenates the non-empty segment texts verbatim. A space is
// inserted only between adjacent ASCII word characters so English words do not
// run together, while Chinese text stays continuous.
func JoinTexts(segments []store.Segment) string {
	var b strings.Builder
	var prev rune
	hasPrev := false
	for _, segment := range segments {
		text := strings.TrimSpace(segment.Text)
		if text == "" {
			continue
		}
		first := firstRune(text)
		if hasPrev && isASCIIWord(prev) && isASCIIWord(first) {
			b.WriteByte(' ')
		}
		b.WriteString(text)
		prev = lastRune(text)
		hasPrev = true
	}
	return b.String()
}

func firstRune(s string) rune {
	for _, r := range s {
		return r
	}
	return 0
}

func lastRune(s string) rune {
	var last rune
	for _, r := range s {
		last = r
	}
	return last
}

func isASCIIWord(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

// ValidateGroups requires the groups to cover 1..n exactly once, in order.
func ValidateGroups(groups []Group, n int) error {
	if n <= 0 {
		return fmt.Errorf("没有可划分的语句")
	}
	if len(groups) == 0 {
		return fmt.Errorf("段落划分为空")
	}
	expected := 1
	for i, group := range groups {
		if group.Start != expected {
			return fmt.Errorf("段落 %d 起点为 %d，应为 %d", i+1, group.Start, expected)
		}
		if group.End < group.Start || group.End > n {
			return fmt.Errorf("段落 %d 范围 %d-%d 无效", i+1, group.Start, group.End)
		}
		expected = group.End + 1
	}
	if expected != n+1 {
		return fmt.Errorf("段落划分只覆盖到 %d，共 %d 句", expected-1, n)
	}
	return nil
}

// ComposeParagraphs turns validated groups into paragraphs without altering text.
func ComposeParagraphs(run []store.Segment, groups []Group, source, model string, now time.Time) []Paragraph {
	paragraphs := make([]Paragraph, 0, len(groups))
	for _, group := range groups {
		members := run[group.Start-1 : group.End]
		if len(members) == 0 {
			continue
		}
		ids := make([]string, len(members))
		for i, member := range members {
			ids[i] = member.ID
		}
		paragraphs = append(paragraphs, Paragraph{
			ID:         members[0].ID,
			StartedAt:  members[0].StartedAt,
			EndedAt:    members[len(members)-1].EndedAt,
			Speaker:    members[0].Speaker,
			Text:       JoinTexts(members),
			SegmentIDs: ids,
			Source:     source,
			Model:      model,
			UpdatedAt:  now,
		})
	}
	return paragraphs
}

func FallbackGroups(n int) []Group {
	if n <= 0 {
		return nil
	}
	return []Group{{Start: 1, End: n}}
}
