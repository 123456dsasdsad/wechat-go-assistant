package maintenance

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

type Rate struct {
	Input, Cached, Write, Output      float64
	LongThreshold                     int64
	LongInputFactor, LongOutputFactor float64
}

// Standard API-equivalent rates, USD / million tokens. Verified 2026-10-07.
// Subscription billing and included usage cannot be inferred from these rates.
var Rates = map[string]Rate{
	"gpt-6-sol":   {2, .2, 2.5, 10, 272000, 2, 1.5},
	"gpt-6.1-sol": {2, .1, 2.5, 10, 272000, 2, 1.5},
	"gpt-6-luna":  {.1, .01, .125, .5, 272000, 2, 1.5},
	"gpt-6-astra": {10, 1, 12.5, 50, 272000, 2, 1.5},
}

type Tokens struct {
	Input     int64 `json:"input"`
	Cached    int64 `json:"cached"`
	Write     int64 `json:"cache_write"`
	Output    int64 `json:"output"`
	Reasoning int64 `json:"reasoning"`
	Total     int64 `json:"total"`
}

func (t *Tokens) add(u Tokens) {
	t.Input += u.Input
	t.Cached += u.Cached
	t.Write += u.Write
	t.Output += u.Output
	t.Reasoning += u.Reasoning
	t.Total += u.Total
}

type ModelUsage struct {
	Tokens
	Requests       int     `json:"requests"`
	USD            float64 `json:"api_equivalent_usd"`
	UnpricedTokens int64   `json:"unpriced_tokens"`
}
type UsageSummary struct {
	Day string `json:"day"`
	Tokens
	Requests       int                    `json:"requests"`
	USD            float64                `json:"api_equivalent_usd"`
	Models         map[string]*ModelUsage `json:"models"`
	UnpricedTokens int64                  `json:"unpriced_tokens"`
	Malformed      int                    `json:"malformed_lines"`
}
type usageEvent struct {
	Type, RequestID, AuthID, Model, ServiceTier string
	RequestedAtMS                               int64
	LatencyMS                                   int64
	Usage                                       struct {
		InputTokens, OutputTokens, CachedTokens, ReasoningTokens, TotalTokens int64
		TokenBreakdown                                                        struct {
			Input struct {
				CacheWriteTokens int64 `json:"cache_write_tokens"`
			}
		}
	}
}

func Estimate(model, tier string, t Tokens) (float64, bool) {
	r, ok := Rates[model]
	if !ok {
		return 0, false
	}
	factor := 1.0
	switch strings.ToLower(tier) {
	case "", "auto", "default", "standard":
	case "fast", "priority":
		factor = 2
	case "batch", "flex":
		factor = .5
	case "ultrafast":
		return 0, false
	default:
		return 0, false
	}
	inFactor, outFactor := 1.0, 1.0
	if t.Input > r.LongThreshold {
		inFactor = r.LongInputFactor
		outFactor = r.LongOutputFactor
	}
	uncached := t.Input - t.Cached - t.Write
	if uncached < 0 {
		return 0, false
	}
	return ((float64(uncached)*r.Input+float64(t.Cached)*r.Cached+float64(t.Write)*r.Write)*inFactor + float64(t.Output)*r.Output*outFactor) * factor / 1e6, true
}
func Summarize(reader io.Reader, day string) (UsageSummary, error) {
	start, e := time.ParseInLocation("2006-01-02", day, Beijing)
	s := UsageSummary{Day: day, Models: map[string]*ModelUsage{}}
	if e != nil {
		return s, e
	}
	end := start.AddDate(0, 0, 1)
	seen := map[string]bool{}
	scan := bufio.NewScanner(reader)
	scan.Buffer(make([]byte, 65536), 2*1024*1024)
	for scan.Scan() {
		line := strings.TrimSpace(scan.Text())
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var u usageEvent
		if json.Unmarshal([]byte(line), &u) != nil {
			s.Malformed++
			continue
		}
		if u.Type != "usage" {
			continue
		}
		at := time.UnixMilli(u.RequestedAtMS + max(u.LatencyMS, 0))
		if at.Before(start) || !at.Before(end) {
			continue
		}
		key := fmt.Sprintf("%s/%s/%d/%s", u.RequestID, u.AuthID, u.RequestedAtMS, u.Model)
		if u.RequestID == "" {
			s.Malformed++
			continue
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		t := Tokens{u.Usage.InputTokens, u.Usage.CachedTokens, u.Usage.TokenBreakdown.Input.CacheWriteTokens, u.Usage.OutputTokens, u.Usage.ReasoningTokens, u.Usage.InputTokens + u.Usage.OutputTokens}
		if t.Input < 0 || t.Cached < 0 || t.Output < 0 || t.Write < 0 {
			s.Malformed++
			continue
		}
		m := s.Models[u.Model]
		if m == nil {
			m = &ModelUsage{}
			s.Models[u.Model] = m
		}
		m.add(t)
		s.add(t)
		m.Requests++
		s.Requests++
		if cost, ok := Estimate(u.Model, u.ServiceTier, t); ok {
			m.USD += cost
			s.USD += cost
		} else {
			m.UnpricedTokens += t.Total
			s.UnpricedTokens += t.Total
		}
	}
	return s, scan.Err()
}
func (s UsageSummary) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "【每日用量｜%s 北京时间】\n阿里云统一入口（含校园任务，只统计一次）\n请求：%d\n输入：%d tokens\n其中缓存读取：%d；缓存写入：%d\n输出：%d（含推理 %d）\n合计：%d tokens\nAPI 等价值：$%.6f USD", s.Day, s.Requests, s.Input, s.Cached, s.Write, s.Output, s.Reasoning, s.Total, s.USD)
	var models []string
	for k := range s.Models {
		models = append(models, k)
	}
	sort.Strings(models)
	for _, name := range models {
		m := s.Models[name]
		fmt.Fprintf(&b, "\n%s：%d tokens，$%.6f", name, m.Total, m.USD)
	}
	if s.UnpricedTokens > 0 {
		fmt.Fprintf(&b, "\n另有 %d tokens 未找到可核实的单价，未计入美元合计。", s.UnpricedTokens)
	}
	b.WriteString("\n美元为公开 API 单价折算，并非 Codex 订阅扣费；不含工具费、税费和地域加价。价格核对：2026-10-07。按请求完成时间归日；跨零点请求计入完成日。")
	if s.Malformed > 0 {
		fmt.Fprintf(&b, "\n日志异常/不完整行：%d，统计可能不完整。", s.Malformed)
	}
	return b.String()
}
