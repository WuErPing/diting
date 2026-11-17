package tokusage

import "testing"

func TestParseOpenAIUsage(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want Usage
		ok   bool
	}{
		{
			name: "full details",
			raw:  `{"prompt_tokens":100,"completion_tokens":50,"prompt_tokens_details":{"cached_tokens":30},"completion_tokens_details":{"reasoning_tokens":20}}`,
			want: Usage{Input: 70, Output: 30, CacheRead: 30, Reasoning: 20},
			ok:   true,
		},
		{
			name: "no details",
			raw:  `{"prompt_tokens":100,"completion_tokens":50}`,
			want: Usage{Input: 100, Output: 50},
			ok:   true,
		},
		{
			name: "cached exceeds prompt clamps to zero",
			raw:  `{"prompt_tokens":10,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":20}}`,
			want: Usage{Input: 0, Output: 5, CacheRead: 20},
			ok:   true,
		},
		{name: "empty object", raw: `{}`, ok: false},
		{name: "null", raw: `null`, ok: false},
		{name: "invalid json", raw: `{`, ok: false},
		{name: "all zero", raw: `{"prompt_tokens":0,"completion_tokens":0}`, ok: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ParseOpenAIUsage([]byte(tc.raw))
			if ok != tc.ok || got != tc.want {
				t.Fatalf("got %+v ok=%v, want %+v ok=%v", got, ok, tc.want, tc.ok)
			}
		})
	}
}
