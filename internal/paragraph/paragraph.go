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
	defaultMaxSilence  = 15 * time.Minute
)

type Paragraph struct {
	ID            string    `json:"id"`
	StartedAt     time.Time `json:"startedAt"`
	EndedAt       time.Time `json:"endedAt"`
	Speaker       string    `json:"speaker,omitempty"`
	Text          string    `json:"text"`
	Category      string    `json:"category,omitempty"`
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

// TopicCategory, TopicTitle and TopicSummary also provide useful labels for
// paragraphs saved before topic metadata was introduced.
func (p Paragraph) TopicCategory() string {
	if !p.HasTopic() {
		return ""
	}
	return truncateTopicText(compactTopicText(p.Category), 8)
}

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

// IsFillerOnly reports whether text contains only non-lexical backchanneling.
func IsFillerOnly(text string) bool {
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
		return true
	}
	words := strings.Fields(text)
	englishFiller := len(words) > 0
	for _, word := range words {
		word = strings.ToLower(strings.TrimFunc(word, func(r rune) bool {
			return !unicode.IsLetter(r)
		}))
		switch word {
		case "um", "umm", "uh", "uhh", "hm", "hmm", "hmmm", "er", "err", "ah", "oh":
		default:
			englishFiller = false
		}
	}
	return englishFiller
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
	return truncateTopicText(title, 12)
}

func topicSummary(summary, text string) string {
	summary = compactTopicText(summary)
	if summary == "" {
		summary = compactTopicText(text)
	}
	if summary == "" {
		return "该段没有可识别的文字。"
	}
	return truncateTopicText(summary, 50)
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
	Category      string
	Title         string
	Summary       string
	TopicExcluded bool
}

type RunOptions struct {
	MaxSegments int
	MaxChars    int
	MaxSilence  time.Duration
}

func (o RunOptions) withDefaults() RunOptions {
	if o.MaxSegments <= 0 {
		o.MaxSegments = defaultMaxSegments
	}
	if o.MaxChars <= 0 {
		o.MaxChars = defaultMaxChars
	}
	if o.MaxSilence <= 0 {
		o.MaxSilence = defaultMaxSilence
	}
	return o
}

// BuildRuns keeps speaker turns together so the model can identify complete
// meeting topics. Long silences start a new large time period. Segment and
// character caps bound the prompt size; an oversized single segment still forms a run.
func BuildRuns(segments []store.Segment, opts RunOptions) [][]store.Segment {
	opts = opts.withDefaults()
	var runs [][]store.Segment
	var current []store.Segment
	chars := 0
	for _, segment := range segments {
		length := len([]rune(strings.TrimSpace(segment.Text)))
		if len(current) > 0 {
			previous := current[len(current)-1]
			if segment.StartedAt.Sub(previous.EndedAt) >= opts.MaxSilence ||
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

// NormalizeGroups repairs gaps and repeated coverage in model output. Each
// remaining group starts at the first uncovered sentence; a trailing gap extends
// the last group. Reversed or out-of-range groups are still errors.
func NormalizeGroups(groups []Group, n int) ([]Group, bool, error) {
	if n <= 0 {
		return nil, false, fmt.Errorf("no sentences to group")
	}
	if len(groups) == 0 {
		return nil, false, fmt.Errorf("grouping is empty")
	}
	repaired := make([]Group, 0, len(groups))
	expected := 1
	changed := false
	for i, group := range groups {
		if group.Start < 1 || group.End > n {
			return nil, false, fmt.Errorf("paragraph %d has invalid range %d-%d", i+1, group.Start, group.End)
		}
		if group.End < expected {
			changed = true
			continue
		}
		if group.End < group.Start {
			return nil, false, fmt.Errorf("paragraph %d has invalid range %d-%d", i+1, group.Start, group.End)
		}
		if group.Start != expected {
			group.Start = expected
			changed = true
		}
		repaired = append(repaired, group)
		expected = group.End + 1
	}
	if len(repaired) == 0 {
		return nil, false, fmt.Errorf("grouping has no uncovered sentences")
	}
	if expected <= n {
		repaired[len(repaired)-1].End = n
		changed = true
	}
	return repaired, changed, nil
}

// ComposeParagraphs turns validated groups into single-speaker paragraphs without altering text.
func ComposeParagraphs(run []store.Segment, groups []Group, source, model string, now time.Time, resolve func(string) string) []Paragraph {
	paragraphs := make([]Paragraph, 0, len(groups))
	resolveSpeaker := func(label string) string {
		if resolve != nil {
			return resolve(label)
		}
		return label
	}
	for _, group := range groups {
		members := run[group.Start-1 : group.End]
		for start := 0; start < len(members); {
			speaker := resolveSpeaker(members[start].Speaker)
			end := start + 1
			for end < len(members) && resolveSpeaker(members[end].Speaker) == speaker {
				end++
			}
			turn := members[start:end]
			ids := make([]string, len(turn))
			for i, member := range turn {
				ids[i] = member.ID
			}
			text := JoinTexts(turn)
			category, title, summary := "", "", ""
			if !group.TopicExcluded && hasTopicText(text) {
				category = truncateTopicText(compactTopicText(group.Category), 8)
				title = topicTitle(group.Title, text)
				summary = topicSummary(group.Summary, text)
			}
			paragraphs = append(paragraphs, Paragraph{
				ID:            turn[0].ID,
				StartedAt:     turn[0].StartedAt,
				EndedAt:       turn[len(turn)-1].EndedAt,
				Speaker:       speaker,
				Text:          text,
				Category:      category,
				Title:         title,
				Summary:       summary,
				TopicExcluded: group.TopicExcluded,
				SegmentIDs:    ids,
				Source:        source,
				Model:         model,
				UpdatedAt:     now,
			})
			start = end
		}
	}
	return paragraphs
}

func FallbackGroups(n int) []Group {
	if n <= 0 {
		return nil
	}
	return []Group{{Start: 1, End: n, TopicExcluded: true}}
}
