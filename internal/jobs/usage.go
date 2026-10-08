package jobs

import (
	"encoding/json"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/maintenance"
)

type UsageSummary struct {
	Tasks    int     `json:"tasks"`
	Measured int     `json:"measured"`
	Unpriced int     `json:"unpriced"`
	Input    int64   `json:"input"`
	Cached   int64   `json:"cached"`
	Output   int64   `json:"output"`
	Total    int64   `json:"total"`
	USD      float64 `json:"api_equivalent_usd"`
}

func Cost(j Job) (float64, bool) {
	if !j.Usage.Available || j.Usage.CacheWrite > 0 {
		return 0, false
	}
	return maintenance.Estimate(j.Model, "standard", maintenance.Tokens{Input: j.Usage.Input, Cached: j.Usage.Cached, Output: j.Usage.Output, Reasoning: j.Usage.Reasoning, Total: j.Usage.Total})
}
func (s *Store) Usage(owner, cid string) (UsageSummary, error) {
	total, byConversation, e := s.UsageByConversation(owner)
	if cid == "" {
		return total, e
	}
	return byConversation[cid], e
}

// Only read the small accounting fields; progress and historical answers stay on disk.
func (s *Store) UsageByConversation(owner string) (UsageSummary, map[string]UsageSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.usageLocked(owner)
}
func (s *Store) usageLocked(owner string) (UsageSummary, map[string]UsageSummary, error) {
	total := UsageSummary{}
	by := map[string]UsageSummary{}
	rows, e := s.db.Query("SELECT conversation,json_extract(document,'$.model'),COALESCE(json_extract(document,'$.usage'),'{}') FROM tasks WHERE owner=? AND status IN ('done','delivered')", owner)
	if e != nil {
		return total, by, e
	}
	defer rows.Close()
	for rows.Next() {
		var cid, model, raw string
		if e = rows.Scan(&cid, &model, &raw); e != nil {
			return total, by, e
		}
		j := Job{Model: model}
		if e = json.Unmarshal([]byte(raw), &j.Usage); e != nil {
			return total, by, e
		}
		v := by[cid]
		v.add(j)
		by[cid] = v
		total.add(j)
	}
	return total, by, rows.Err()
}
func (out *UsageSummary) add(j Job) {
	out.Tasks++
	if !j.Usage.Available {
		return
	}
	out.Measured++
	out.Input += j.Usage.Input
	out.Cached += j.Usage.Cached
	out.Output += j.Usage.Output
	out.Total += j.Usage.Total
	if cost, ok := Cost(j); ok {
		out.USD += cost
	} else {
		out.Unpriced++
	}
}
