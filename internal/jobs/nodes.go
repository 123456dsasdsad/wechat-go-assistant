package jobs

import (
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
)

type NodePolicy struct {
	ID       string `json:"id"`
	Priority int    `json:"priority"`
	Enabled  bool   `json:"enabled"`
}
type NodeHeartbeat struct {
	ID           string    `json:"id"`
	Instance     string    `json:"instance"`
	Version      string    `json:"version"`
	Ready        bool      `json:"ready"`
	Models       []string  `json:"models"`
	Capabilities []string  `json:"capabilities"`
	Seen         time.Time `json:"seen"`
}
type Claimant struct {
	Node     string `json:"node"`
	Instance string `json:"instance"`
}

func (s *Store) ConfigureNodes(policies []NodePolicy, legacyNode string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	allowed := map[string]NodePolicy{}
	for _, p := range policies {
		if p.ID == "" || len(p.ID) > 80 || strings.ContainsAny(p.ID, "/\\\x00\r\n ") || p.Priority < 0 {
			return errors.New("invalid_node_policy")
		}
		if _, exists := allowed[p.ID]; exists {
			return errors.New("duplicate_node_id")
		}
		allowed[p.ID] = p
	}
	if legacyNode != "" {
		if _, ok := allowed[legacyNode]; !ok {
			return errors.New("unknown_legacy_node")
		}
	}
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS worker_nodes (id TEXT PRIMARY KEY, document BLOB NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS conversation_nodes (owner TEXT NOT NULL, conversation TEXT NOT NULL, node TEXT NOT NULL, PRIMARY KEY(owner,conversation))`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			return err
		}
	}
	if legacyNode != "" {
		if _, err := s.db.Exec(`INSERT OR IGNORE INTO conversation_nodes(owner,conversation,node) SELECT DISTINCT owner,conversation,? FROM tasks WHERE conversation<>''`, legacyNode); err != nil {
			return err
		}
	}
	s.nodes = allowed
	return nil
}
func (s *Store) RegisterNode(v NodeHeartbeat, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.nodes[v.ID]
	if !ok || !p.Enabled || !validID(v.Instance) || len(v.Models) > 100 || len(v.Capabilities) > 32 || len(v.Version) > 80 {
		return errors.New("node_not_enabled")
	}
	v.Seen = now.UTC()
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO worker_nodes VALUES(?,?) ON CONFLICT(id) DO UPDATE SET document=excluded.document`, v.ID, raw)
	return err
}
func (s *Store) nodeRows() ([]NodeHeartbeat, error) {
	rows, err := s.db.Query(`SELECT document FROM worker_nodes`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []NodeHeartbeat{}
	for rows.Next() {
		var raw []byte
		var n NodeHeartbeat
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &n); err != nil {
			return nil, err
		}
		result = append(result, n)
	}
	return result, rows.Err()
}
func (s *Store) Nodes() ([]NodeHeartbeat, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.nodeRows()
}
func containsValue(values []string, v string) bool {
	for _, x := range values {
		if x == v {
			return true
		}
	}
	return false
}
func nodeSupports(n NodeHeartbeat, j Job) bool {
	if !containsValue(n.Models, j.Model) {
		return false
	}
	if (j.Kind == "library_intake" || j.Kind == "review_update") && !containsValue(n.Capabilities, "library") {
		return false
	}
	for _, f := range j.Attachments {
		name := strings.ToLower(f.Name)
		for _, ext := range []string{".wav", ".mp3", ".m4a", ".ogg", ".silk", ".amr", ".mp4"} {
			if strings.HasSuffix(name, ext) && !containsValue(n.Capabilities, "transcription") {
				return false
			}
		}
	}
	return true
}

// chooseNode preserves conversation affinity. Switching an existing conversation
// requires a verified data restore and an explicit transfer, never just a timeout.
func (s *Store) chooseNode(j Job, nodes []NodeHeartbeat, now time.Time) string {
	var affinity string
	if j.ConversationID != "" {
		err := s.db.QueryRow(`SELECT node FROM conversation_nodes WHERE owner=? AND conversation=?`, j.Owner, j.ConversationID).Scan(&affinity)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			s.readErr = err
			return ""
		}
	}
	candidates := []NodeHeartbeat{}
	for _, n := range nodes {
		p, ok := s.nodes[n.ID]
		if ok && p.Enabled && n.Ready && now.Sub(n.Seen) >= 0 && now.Sub(n.Seen) < 45*time.Second && (affinity == "" || affinity == n.ID) && nodeSupports(n, j) {
			candidates = append(candidates, n)
		}
	}
	sort.Slice(candidates, func(i, k int) bool {
		a, b := s.nodes[candidates[i].ID], s.nodes[candidates[k].ID]
		if a.Priority != b.Priority {
			return a.Priority < b.Priority
		}
		return a.ID < b.ID
	})
	if len(candidates) == 0 {
		return ""
	}
	return candidates[0].ID
}
