package paragraph

import (
	"reflect"
	"testing"
	"time"

	"diting/internal/store"
)

var testBase = time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)

func testSegment(id, speaker, text string, start time.Time, end time.Time) store.Segment {
	return store.Segment{ID: id, StartedAt: start, EndedAt: end, Speaker: speaker, Text: text}
}

func runIDs(runs [][]store.Segment) [][]string {
	ids := make([][]string, len(runs))
	for i, run := range runs {
		for _, segment := range run {
			ids[i] = append(ids[i], segment.ID)
		}
	}
	return ids
}

func TestBuildRunsSplitsOnSpeakerChange(t *testing.T) {
	a := testSegment("a", "说话人1", "一", testBase, testBase.Add(time.Second))
	b := testSegment("b", "说话人1", "二", testBase.Add(2*time.Second), testBase.Add(3*time.Second))
	c := testSegment("c", "说话人2", "三", testBase.Add(4*time.Second), testBase.Add(5*time.Second))

	runs := BuildRuns([]store.Segment{a, b, c}, RunOptions{})
	want := [][]string{{"a", "b"}, {"c"}}
	if got := runIDs(runs); !reflect.DeepEqual(got, want) {
		t.Fatalf("runs = %v, want %v", got, want)
	}
}

func TestBuildRunsIgnoresLongPauses(t *testing.T) {
	first := testSegment("a", "说话人1", "一", testBase, testBase.Add(time.Second))
	for _, gap := range []time.Duration{time.Minute, time.Hour, -2 * time.Second} {
		next := testSegment("b", "说话人1", "二", first.EndedAt.Add(gap), first.EndedAt.Add(gap+time.Second))
		runs := BuildRuns([]store.Segment{first, next}, RunOptions{})
		want := [][]string{{"a", "b"}}
		if got := runIDs(runs); !reflect.DeepEqual(got, want) {
			t.Fatalf("gap %s: runs = %v, want %v", gap, got, want)
		}
	}
}

func TestBuildRunsHonorsSegmentCap(t *testing.T) {
	var segments []store.Segment
	for i := 0; i < 3; i++ {
		start := testBase.Add(time.Duration(i*2) * time.Second)
		segments = append(segments, testSegment(string(rune('a'+i)), "说话人1", "字", start, start.Add(time.Second)))
	}
	runs := BuildRuns(segments, RunOptions{MaxSegments: 2})
	want := [][]string{{"a", "b"}, {"c"}}
	if got := runIDs(runs); !reflect.DeepEqual(got, want) {
		t.Fatalf("runs = %v, want %v", got, want)
	}
}

func TestBuildRunsHonorsCharCap(t *testing.T) {
	a := testSegment("a", "说话人1", "你好", testBase, testBase.Add(time.Second))
	b := testSegment("b", "说话人1", "世界", testBase.Add(2*time.Second), testBase.Add(3*time.Second))
	c := testSegment("c", "说话人1", "再见", testBase.Add(4*time.Second), testBase.Add(5*time.Second))

	runs := BuildRuns([]store.Segment{a, b, c}, RunOptions{MaxChars: 4})
	want := [][]string{{"a", "b"}, {"c"}}
	if got := runIDs(runs); !reflect.DeepEqual(got, want) {
		t.Fatalf("runs = %v, want %v", got, want)
	}
}

func TestBuildRunsAllowsOversizedSingleSegment(t *testing.T) {
	long := testSegment("a", "说话人1", "很长的一句话", testBase, testBase.Add(time.Second))
	runs := BuildRuns([]store.Segment{long}, RunOptions{MaxChars: 1})
	if len(runs) != 1 || len(runs[0]) != 1 {
		t.Fatalf("runs = %v", runIDs(runs))
	}
}

func TestBuildRunsDefaultCaps(t *testing.T) {
	var segments []store.Segment
	for i := 0; i < 21; i++ {
		start := testBase.Add(time.Duration(i*2) * time.Second)
		segments = append(segments, testSegment(string(rune('a'+i)), "说话人1", "字", start, start.Add(time.Second)))
	}
	runs := BuildRuns(segments, RunOptions{})
	if len(runs) != 2 || len(runs[0]) != 20 || len(runs[1]) != 1 {
		t.Fatalf("run sizes = %d/%d", len(runs[0]), len(runs[1]))
	}
}

func TestBuildRunsEmpty(t *testing.T) {
	if runs := BuildRuns(nil, RunOptions{}); len(runs) != 0 {
		t.Fatalf("runs = %v", runIDs(runs))
	}
}

func TestJoinTextsVerbatim(t *testing.T) {
	cases := []struct {
		name    string
		texts   []string
		speaker string
		want    string
	}{
		{"chinese continuous", []string{"你好", "世界"}, "说话人1", "你好世界"},
		{"ascii words spaced", []string{"hello", "world"}, "说话人1", "hello world"},
		{"ascii then chinese", []string{"hello", "世界"}, "说话人1", "hello世界"},
		{"chinese then ascii", []string{"你好", "world"}, "说话人1", "你好world"},
		{"numbers", []string{"2026", "year"}, "说话人1", "2026 year"},
		{"trims and skips empty", []string{"  你好 ", "", "世界"}, "说话人1", "你好世界"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var segments []store.Segment
			for i, text := range tc.texts {
				start := testBase.Add(time.Duration(i) * time.Second)
				segments = append(segments, testSegment("s", tc.speaker, text, start, start.Add(time.Second)))
			}
			if got := JoinTexts(segments); got != tc.want {
				t.Fatalf("JoinTexts = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNormalizeGroups(t *testing.T) {
	cases := []struct {
		name    string
		groups  []Group
		n       int
		want    []Group
		changed bool
		ok      bool
	}{
		{"single full", []Group{{1, 3}}, 3, []Group{{1, 3}}, false, true},
		{"split", []Group{{1, 2}, {3, 3}}, 3, []Group{{1, 2}, {3, 3}}, false, true},
		{"gap between groups", []Group{{1, 1}, {3, 3}}, 3, []Group{{1, 1}, {2, 3}}, true, true},
		{"leading gap", []Group{{3, 5}}, 5, []Group{{1, 5}}, true, true},
		{"trailing gap", []Group{{1, 2}}, 5, []Group{{1, 5}}, true, true},
		{"empty groups", nil, 3, nil, false, false},
		{"no sentences", []Group{{1, 1}}, 0, nil, false, false},
		{"overlap", []Group{{1, 2}, {2, 3}}, 3, nil, false, false},
		{"out of range", []Group{{1, 4}}, 3, nil, false, false},
		{"reversed", []Group{{1, 0}}, 3, nil, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, changed, err := NormalizeGroups(tc.groups, tc.n)
			if tc.ok && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.ok {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if changed != tc.changed {
				t.Fatalf("changed = %v, want %v", changed, tc.changed)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("groups = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestComposeParagraphsKeepsVerbatimTextAndIDs(t *testing.T) {
	a := testSegment("a", "说话人1", "你好", testBase, testBase.Add(time.Second))
	b := testSegment("b", "说话人1", " world ", testBase.Add(2*time.Second), testBase.Add(3*time.Second))
	c := testSegment("c", "说话人1", "", testBase.Add(4*time.Second), testBase.Add(5*time.Second))
	now := testBase.Add(time.Hour)

	paragraphs := ComposeParagraphs([]store.Segment{a, b, c}, []Group{{1, 2}, {3, 3}}, SourceLLM, "model-x", now, nil)
	if len(paragraphs) != 2 {
		t.Fatalf("paragraphs = %d", len(paragraphs))
	}
	first := paragraphs[0]
	if first.ID != "a" || !first.StartedAt.Equal(a.StartedAt) || !first.EndedAt.Equal(b.EndedAt) {
		t.Fatalf("first span = %s %s %s", first.ID, first.StartedAt, first.EndedAt)
	}
	if first.Text != "你好world" {
		t.Fatalf("first text = %q", first.Text)
	}
	if first.Speaker != "说话人1" || first.Source != SourceLLM || first.Model != "model-x" {
		t.Fatalf("first metadata = %+v", first)
	}
	if !reflect.DeepEqual(first.SegmentIDs, []string{"a", "b"}) || !first.UpdatedAt.Equal(now) {
		t.Fatalf("first ids/time = %v %s", first.SegmentIDs, first.UpdatedAt)
	}
	second := paragraphs[1]
	if second.ID != "c" || second.Text != "" || second.SegmentCount() != 1 {
		t.Fatalf("second = %+v", second)
	}
}

func TestFallbackGroups(t *testing.T) {
	if groups := FallbackGroups(0); groups != nil {
		t.Fatalf("groups = %v", groups)
	}
	groups := FallbackGroups(3)
	if len(groups) != 1 || groups[0] != (Group{Start: 1, End: 3}) {
		t.Fatalf("groups = %v", groups)
	}
}
