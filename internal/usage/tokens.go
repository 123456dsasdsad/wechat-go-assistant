package usage

type Tokens struct {
	Available  bool  `json:"available"`
	Input      int64 `json:"input"`
	Cached     int64 `json:"cached"`
	CacheWrite int64 `json:"cache_write,omitempty"`
	Output     int64 `json:"output"`
	Reasoning  int64 `json:"reasoning"`
	Total      int64 `json:"total"`
}

func (u Tokens) Valid() bool {
	return u.Input >= 0 && u.Cached >= 0 && u.Cached <= u.Input && u.CacheWrite >= 0 && u.Output >= 0 && u.Reasoning >= 0 && u.Total >= 0
}
func Difference(a, b Tokens) Tokens {
	if !a.Available || !b.Available {
		return Tokens{}
	}
	v := Tokens{Available: true, Input: a.Input - b.Input, Cached: a.Cached - b.Cached, CacheWrite: a.CacheWrite - b.CacheWrite, Output: a.Output - b.Output, Reasoning: a.Reasoning - b.Reasoning, Total: a.Total - b.Total}
	if !v.Valid() {
		return Tokens{}
	}
	return v
}
