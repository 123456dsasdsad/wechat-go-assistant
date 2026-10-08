package conversations

import (
	"errors"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/usage"
	"math"
	"path"
	"sort"
	"strings"
)

type Profile struct {
	Project   string  `json:"project"`
	Notes     string  `json:"notes"`
	BudgetUSD float64 `json:"budget_usd"`
}

func ValidProject(p string) bool {
	return p == "" || (len(p) <= 180 && !strings.ContainsAny(p, "\\:\x00") && !strings.HasPrefix(p, "/") && path.Clean(p) == p && p != ".." && !strings.HasPrefix(p, "../") && p != ".")
}
func (s *Store) List(query string, archived bool) []Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Session{}
	query = strings.ToLower(strings.TrimSpace(query))
	for _, v := range s.state.Sessions {
		if v.Archived == archived && strings.Contains(strings.ToLower(v.Title+" "+v.FirstTask+" "+v.LastTask), query) {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Pinned != out[j].Pinned {
			return out[i].Pinned
		}
		return out[i].Number < out[j].Number
	})
	return out
}
func (s *Store) Resolve(value string) (Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return findSession(s.state.Sessions, value)
}
func (s *Store) UpdateProfile(id string, p Profile) error {
	if !ValidProject(p.Project) || len(p.Notes) > 8192 || p.BudgetUSD < 0 || p.BudgetUSD > 1e6 || math.IsNaN(p.BudgetUSD) || math.IsInf(p.BudgetUSD, 0) {
		return errors.New("invalid_profile")
	}
	return s.update(id, func(v *Session) { v.Profile = p })
}
func (s *Store) Mark(id string, pinned, archived bool) error {
	return s.update(id, func(v *Session) { v.Pinned = pinned; v.Archived = archived })
}
func (s *Store) update(id string, fn func(*Session)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.state.Sessions[id]
	if !ok {
		return errors.New("conversation_not_found")
	}
	next := s.state
	next.Sessions = map[string]Session{}
	for k, v := range s.state.Sessions {
		next.Sessions[k] = v
	}
	fn(&v)
	if v.Archived && id == next.Current {
		return errors.New("switch_conversation_before_archiving_current")
	}
	next.Sessions[id] = v
	if e := writeJSON(s.path, next); e != nil {
		return e
	}
	s.state = next
	return nil
}
func (s *Threads) Usage(id string) usage.Tokens {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.state.Turns) - 1; i >= 0; i-- {
		v := s.state.Turns[i]
		if v.ConversationID == id {
			return v.Cumulative
		}
	}
	return usage.Tokens{}
}
