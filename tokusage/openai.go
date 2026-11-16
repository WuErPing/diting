package tokusage

import "encoding/json"

// ParseOpenAIUsage maps an OpenAI-compatible usage object to Usage. It assumes
// OpenAI semantics: prompt_tokens includes cached_tokens and completion_tokens
// includes reasoning_tokens. Both are subtracted so the five tokscale fields
// sum without double counting. Returns false when usage is absent or all zero.
func ParseOpenAIUsage(raw []byte) (Usage, bool) {
	var u struct {
		PromptTokens     int64 `json:"prompt_tokens"`
		CompletionTokens int64 `json:"completion_tokens"`
		PromptTokensDetails struct {
			CachedTokens int64 `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
		CompletionTokensDetails struct {
			ReasoningTokens int64 `json:"reasoning_tokens"`
		} `json:"completion_tokens_details"`
	}
	if err := json.Unmarshal(raw, &u); err != nil {
		return Usage{}, false
	}
	usage := Usage{
		Input:     max(0, u.PromptTokens-u.PromptTokensDetails.CachedTokens),
		Output:    max(0, u.CompletionTokens-u.CompletionTokensDetails.ReasoningTokens),
		CacheRead: u.PromptTokensDetails.CachedTokens,
		Reasoning: u.CompletionTokensDetails.ReasoningTokens,
	}
	if usage.total() == 0 {
		return Usage{}, false
	}
	return usage, true
}