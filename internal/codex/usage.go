package codex

import "github.com/123456dsasdsad/wechat-go-assistant/internal/usage"

type tokenCounts struct {
	Input      int64 `json:"inputTokens"`
	Cached     int64 `json:"cachedInputTokens"`
	CacheWrite int64 `json:"cacheWriteInputTokens"`
	Output     int64 `json:"outputTokens"`
	Reasoning  int64 `json:"reasoningOutputTokens"`
	Total      int64 `json:"totalTokens"`
}

func (v tokenCounts) tokens() usage.Tokens {
	u := usage.Tokens{Available: true, Input: v.Input, Cached: v.Cached, CacheWrite: v.CacheWrite, Output: v.Output, Reasoning: v.Reasoning, Total: v.Total}
	if !u.Valid() {
		return usage.Tokens{}
	}
	return u
}

type tokenUsageEvent struct {
	ThreadID   string `json:"threadId"`
	TurnID     string `json:"turnId"`
	TokenUsage struct {
		Total tokenCounts `json:"total"`
	} `json:"tokenUsage"`
}
