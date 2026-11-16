package paragraph

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"diting/internal/store"
)

const (
	SourceLLM      = "llm"
	SourceSingle   = "single"
	SourceFallback = "fallback"
)

const (
	defaultMaxSegments = 20
	defaultMaxChars    = 3000
)

type Paragraph struct {
	ID            string    `json:"id"`
	StartedAt     time.Time `json:"startedAt"`
	EndedAt       time.Time `json:"endedAt"`
	Speaker       string    `json:"speaker,omitempty"`
	Text          string    `json:"text"`
	Title         string    `json:"title,omitempty"`
	Summary       string    `json:"summary,omitempty"`
	Digest        string    `json:"digest,omitempty"`
	TopicExcluded bool      `json:"topicExcluded,omitempty"`
	SegmentIDs    []string  `json:"segmentIds"`
	Source        string    `json:"source"`
	Model         string    `json:"model,omitempty"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

func (p Paragraph) SegmentCount() int {
	return len(p.SegmentIDs)
}

func (p Paragraph) HasTopic() bool {
	return !p.TopicExcluded && hasTopicText(p.Text)
}

// TopicTitle and TopicSummary also provide useful labels for paragraphs saved
// before topic metadata was introduced.
func (p Paragraph) TopicTitle() string {
	if !p.HasTopic() {
		return ""
	}
	return topicTitle(p.Title, p.Text)
}

func (p Paragraph) TopicSummary() string {
	if !p.HasTopic() {
		return ""
	}
	return topicSummary(p.Summary, p.Text)
}

func hasTopicText(text string) bool {
	text = compactTopicText(text)
	if text == "" || text == "（未识别出文字）" || text == "(未识别出文字)" {
		return false
	}
	var plain strings.Builder
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			plain.WriteRune(r)
		}
	}
	normalized := strings.ToLower(plain.String())
	if normalized == "" || normalized == "未识别出文字" {
		return false
	}
	chineseFiller := true
	for _, r := range normalized {
		if !strings.ContainsRune("嗯啊呃哦噢哎唉诶欸嘿哈喂", r) {
			chineseFiller = false
			break
		}
	}
	if chineseFiller {
		return false
	}
	words := strings.Fields(text)
	englishFiller := len(words) > 0
	for _, word := range words {
		word = strings.ToLower(strings.TrimFunc(word, func(r rune) bool {
			return !unicode.IsLetter(r)
		}))
		switch word {
		case "um", "umm", "uh", "uhh", "hm", "hmm", "hmmm", "er", "err", "ah", "oh", "hi", "hey", "hello":
		default:
			englishFiller = false
		}
	}
	return !englishFiller
}

func topicTitle(title, text string) string {
	title = compactTopicText(title)
	if title == "" {
		title = compactTopicText(text)
	}
	if title == "" {
		return "无文字段落"
	}
	return truncateTopicText(title, 20)
}

func topicSummary(summary, text string) string {
	summary = compactTopicText(summary)
	if summary == "" {
		summary = compactTopicText(text)
	}
	if summary == "" {
		return "该段没有可识别的文字。"
	}
	return truncateTopicText(summary, 100)
}

func compactTopicText(text string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
}

func truncateTopicText(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit-1]) + "…"
}

type Group struct {
	Start         int
	End           int
	Title         string
	Summary       string
	TopicExcluded bool
}

type RunOptions struct {
	MaxSegments int
	MaxChars    int
}

func (o RunOptions) withDefaults() RunOptions {
	if o.MaxSegments <= 0 {
		o.MaxSegments = defaultMaxSegments
	}
	if o.MaxChars <= 0 {
		o.MaxChars = defaultMaxChars
	}
	return o
}

// BuildRuns keeps speaker turns together so the model can identify complete
// meeting topics. Segment and character caps bound the prompt size; an oversized
// single segment still forms a run.
func BuildRuns(segments []store.Segment, opts RunOptions) [][]store.Segment {
	opts = opts.withDefaults()
	var runs [][]store.Segment
	var current []store.Segment
	chars := 0
	for _, segment := range segments {
		length := len([]rune(strings.TrimSpace(segment.Text)))
		if len(current) > 0 {
			if len(current)+1 > opts.MaxSegments ||
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

// NormalizeGroups repairs gaps in model output by pulling each group's start
// back to the first uncovered sentence; a trailing gap extends the last group.
// Overlapping, reversed or out-of-range groups are still errors.
func NormalizeGroups(groups []Group, n int) ([]Group, bool, error) {
	if n <= 0 {
		return nil, false, fmt.Errorf("没有可划分的语句")
	}
	if len(groups) == 0 {
		return nil, false, fmt.Errorf("段落划分为空")
	}
	repaired := make([]Group, len(groups))
	expected := 1
	changed := false
	for i, group := range groups {
		if group.End < group.Start || group.End > n {
			return nil, false, fmt.Errorf("段落 %d 范围 %d-%d 无效", i+1, group.Start, group.End)
		}
		if group.Start < expected {
			return nil, false, fmt.Errorf("段落 %d 起点为 %d，与已覆盖范围重叠", i+1, group.Start)
		}
		if group.Start > expected {
			group.Start = expected
			changed = true
		}
		repaired[i] = group
		expected = group.End + 1
	}
	if expected <= n {
		repaired[len(repaired)-1].End = n
		changed = true
	}
	return repaired, changed, nil
}

// ComposeParagraphs turns validated groups into paragraphs without altering text.
func ComposeParagraphs(run []store.Segment, groups []Group, source, model string, now time.Time, resolve func(string) string) []Paragraph {
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
		speaker := members[0].Speaker
		if resolve != nil {
			speaker = resolve(speaker)
		}
		for _, member := range members[1:] {
			other := member.Speaker
			if resolve != nil {
				other = resolve(other)
			}
			if other != speaker {
				speaker = ""
				break
			}
		}
		text := JoinTexts(members)
		title, summary := "", ""
		if !group.TopicExcluded && hasTopicText(text) {
			title = topicTitle(group.Title, text)
			summary = topicSummary(group.Summary, text)
		}
		paragraphs = append(paragraphs, Paragraph{
			ID:            members[0].ID,
			StartedAt:     members[0].StartedAt,
			EndedAt:       members[len(members)-1].EndedAt,
			Speaker:       speaker,
			Text:          text,
			Title:         title,
			Summary:       summary,
			TopicExcluded: group.TopicExcluded,
			SegmentIDs:    ids,
			Source:        source,
			Model:         model,
			UpdatedAt:     now,
		})
	}
	return paragraphs
}

func FallbackGroups(n int) []Group {
	if n <= 0 {
		return nil
	}
	return []Group{{Start: 1, End: n, TopicExcluded: true}}
}
