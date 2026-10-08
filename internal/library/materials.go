package library

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
)

func Terms(text string) []string {
	set := map[string]bool{}
	rs := []rune(strings.ToLower(text))
	var word []rune
	flush := func() {
		if len(word) > 0 {
			set[string(word)] = true
			word = nil
		}
	}
	for i, r := range rs {
		if unicode.Is(unicode.Han, r) {
			flush()
			if i+1 < len(rs) && unicode.Is(unicode.Han, rs[i+1]) {
				set[string(rs[i:i+2])] = true
			}
		} else if unicode.IsLetter(r) || unicode.IsDigit(r) {
			word = append(word, r)
		} else {
			flush()
		}
	}
	flush()
	var out []string
	for t := range set {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}
func identity(m Material) string {
	for _, s := range m.Sources {
		if s.Verified && s.DOI != "" {
			return "doi:" + strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(s.DOI), "https://doi.org/"), "doi:"))
		}
	}
	for _, s := range m.Sources {
		if s.Verified && s.URL != "" {
			u, e := url.Parse(s.URL)
			if e == nil {
				u.Fragment = ""
				q := u.Query()
				for k := range q {
					if strings.HasPrefix(k, "utm_") {
						q.Del(k)
					}
				}
				u.RawQuery = q.Encode()
				return "url:" + u.String()
			}
		}
	}
	h := sha256.Sum256([]byte(m.Title + "\n" + m.Text))
	return "body:" + hex.EncodeToString(h[:])
}
func topics(m Material) []string {
	set := map[string]bool{}
	for _, v := range m.Topics {
		v = strings.TrimSpace(v)
		if validName(v) {
			set[v] = true
		}
	}
	if len(set) == 0 {
		v := m.Collection
		if !validName(v) {
			v = "待分类"
		}
		set[v] = true
	}
	out := []string{}
	for t := range set {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}
func dirty(tx *sql.Tx, owner, name string) error {
	_, e := tx.Exec(`INSERT INTO topics(owner,name,revision,dirty) VALUES(?,?,1,1) ON CONFLICT(owner,name) DO UPDATE SET revision=revision+1,dirty=1,error=''`, owner, name)
	return e
}
func (s *Store) SaveResearch(owner, iid string, r Research) ([]Material, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	in, e := s.Intake(owner, iid)
	if e != nil {
		return nil, e
	}
	if len(r.Materials) == 0 || len(r.Materials) > 12 {
		return nil, errors.New("invalid_research_material_count")
	}
	tx, e := s.db.Begin()
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	var cached []byte
	if e = tx.QueryRow("SELECT document FROM research WHERE owner=? AND intake=?", owner, iid).Scan(&cached); e == nil {
		var out []Material
		e = decode(cached, &out)
		return out, e
	} else if !errors.Is(e, sql.ErrNoRows) {
		return nil, e
	}
	out := []Material{}
	for _, m := range r.Materials {
		if !validName(m.Title) || len(m.Text) > 2<<20 || len(m.Summary) > 32<<10 {
			return nil, errors.New("invalid_material")
		}
		if e = ValidateMaterial(m); e != nil {
			return nil, e
		}
		m.Owner = owner
		m.IntakeID = iid
		m.Collection = in.Collection
		m.Assets = append(m.Assets, in.Assets...)
		m.Assets = uniqueAssets(m.Assets)
		m.Topics = topics(m)
		m.Updated = time.Now().UTC()
		m.Revision = 1
		m.ID = 0
		var oldRaw []byte
		var old Material
		e = tx.QueryRow("SELECT document FROM materials WHERE owner=? AND identity=?", owner, identity(m)).Scan(&oldRaw)
		if e == nil {
			if e = decode(oldRaw, &old); e != nil {
				return nil, e
			}
			m.ID = old.ID
			m.Assets = append(old.Assets, m.Assets...)
			m.Assets = uniqueAssets(m.Assets)
			m.Revision = old.Revision + 1
			// Repeated source adds topic relationships but keeps the richer verified reading.
			for _, t := range old.Topics {
				m.Topics = append(m.Topics, t)
			}
			m.Topics = topics(m)
			if readingScore(old) > readingScore(m) {
				m.Claims = old.Claims
				m.Sources = old.Sources
				m.Text = old.Text
				m.Summary = old.Summary
				m.Missing = old.Missing
			}
			if equivalent(old, m) {
				out = append(out, old)
				continue
			}
		} else if !errors.Is(e, sql.ErrNoRows) {
			return nil, e
		}
		if m.ID == 0 {
			res, e := tx.Exec("INSERT INTO materials(owner,identity,deleted,document) VALUES(?,?,0,'{}')", owner, identity(m))
			if e != nil {
				return nil, e
			}
			m.ID, _ = res.LastInsertId()
		}
		if e = writeMaterial(tx, m); e != nil {
			return nil, e
		}
		out = append(out, m)
	}
	b, _ := json.Marshal(out)
	if report, e := json.Marshal(r.Problems); e == nil {
		if _, e = tx.Exec("INSERT OR REPLACE INTO documents VALUES(?,?)", "research-problems:"+owner+":"+iid, report); e != nil {
			return nil, e
		}
	}
	if _, e = tx.Exec("INSERT INTO research VALUES(?,?,?)", owner, iid, b); e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	return out, nil
}

func readingScore(m Material) int {
	score := 0
	for _, s := range m.Sources {
		n := map[string]int{"metadata_only": 0, "abstract_only": 1, "screenshot": 1, "partial_text": 3, "full_text": 4}[s.ReadingScope]
		if n > score {
			score = n
		}
	}
	return score
}
func equivalent(a, b Material) bool {
	a.ID = 0
	b.ID = 0
	a.Revision = 0
	b.Revision = 0
	a.Updated = time.Time{}
	b.Updated = time.Time{}
	a.IntakeID = ""
	b.IntakeID = ""
	a.Assets = nil
	b.Assets = nil
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
func writeMaterial(tx *sql.Tx, m Material) error {
	// Capture old memberships before replacing them; ALL old and new topics become dirty.
	rows, e := tx.Query("SELECT topic FROM memberships WHERE owner=? AND material=?", m.Owner, m.ID)
	if e != nil {
		return e
	}
	affected := map[string]bool{}
	for rows.Next() {
		var t string
		if e = rows.Scan(&t); e != nil {
			rows.Close()
			return e
		}
		affected[t] = true
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, t := range m.Topics {
		affected[t] = true
	}
	raw, _ := json.Marshal(m)
	if _, e = tx.Exec("UPDATE materials SET deleted=?,document=? WHERE owner=? AND id=?", m.Deleted, raw, m.Owner, m.ID); e != nil {
		return e
	}
	if _, e = tx.Exec("INSERT INTO material_versions VALUES(?,?,?,?)", m.Owner, m.ID, m.Revision, raw); e != nil {
		return e
	}
	if _, e = tx.Exec("DELETE FROM memberships WHERE owner=? AND material=?", m.Owner, m.ID); e != nil {
		return e
	}
	if _, e = tx.Exec("DELETE FROM terms WHERE owner=? AND material=?", m.Owner, m.ID); e != nil {
		return e
	}
	if !m.Deleted {
		for _, t := range m.Topics {
			if _, e = tx.Exec("INSERT INTO memberships VALUES(?,?,?)", m.Owner, m.ID, t); e != nil {
				return e
			}
		}
		for _, t := range Terms(m.Title + " " + m.Summary + " " + m.Text + " " + strings.Join(m.Tags, " ")) {
			if _, e = tx.Exec("INSERT INTO terms VALUES(?,?,?)", m.Owner, m.ID, t); e != nil {
				return e
			}
		}
	}
	for t := range affected {
		if e = dirty(tx, m.Owner, t); e != nil {
			return e
		}
	}
	return nil
}
func (s *Store) Research(owner, iid string) ([]Material, error) {
	var b []byte
	e := s.db.QueryRow("SELECT document FROM research WHERE owner=? AND intake=?", owner, iid).Scan(&b)
	var m []Material
	if errors.Is(e, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if e == nil {
		e = decode(b, &m)
	}
	return m, e
}
func (s *Store) ResearchProblems(owner, iid string) []string {
	var b []byte
	var out []string
	if s.db.QueryRow("SELECT value FROM documents WHERE key=?", "research-problems:"+owner+":"+iid).Scan(&b) == nil {
		decode(b, &out)
	}
	return out
}
func (s *Store) Get(owner string, id int64) (Material, error) {
	var b []byte
	e := s.db.QueryRow("SELECT document FROM materials WHERE owner=? AND id=?", owner, id).Scan(&b)
	var m Material
	if errors.Is(e, sql.ErrNoRows) {
		return m, ErrNotFound
	}
	if e == nil {
		e = decode(b, &m)
	}
	return m, e
}
func (s *Store) Change(owner string, id int64, ts, tags []string, mode string) (Material, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, e := s.Get(owner, id)
	if e != nil {
		return m, e
	}
	switch mode {
	case "move":
		m.Topics = ts
		m.Topics = topics(m)
	case "tags":
		m.Tags = tags
	case "trash":
		m.Deleted = true
	case "restore":
		m.Deleted = false
	default:
		return m, errors.New("invalid_change")
	}
	m.Revision++
	m.Updated = time.Now().UTC()
	tx, e := s.db.Begin()
	if e != nil {
		return m, e
	}
	defer tx.Rollback()
	if e = writeMaterial(tx, m); e != nil {
		return m, e
	}
	return m, tx.Commit()
}
func (s *Store) Search(owner, q string) ([]Material, error) {
	terms := Terms(q)
	args := []any{owner}
	query := "SELECT document FROM materials WHERE owner=? AND deleted=0"
	if q != "" {
		if len(terms) == 0 {
			return []Material{}, nil
		}
		marks := []string{}
		for _, t := range terms {
			marks = append(marks, "?")
			args = append(args, t)
		}
		query += " AND id IN (SELECT material FROM terms WHERE owner=? AND term IN (" + strings.Join(marks, ",") + ") GROUP BY material HAVING count(*)>=?)"
		args = append([]any{owner, owner}, args[1:]...)
		args = append(args, len(terms))
	}
	query += " ORDER BY id DESC LIMIT 100"
	rows, e := s.db.Query(query, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Material{}
	for rows.Next() {
		var b []byte
		var m Material
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		if e = decode(b, &m); e != nil {
			return nil, e
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
func ValidateMaterial(m Material) error {
	for _, c := range m.Claims {
		if c.Text == "" {
			continue
		}
		if c.Attribution == "system_synthesis" || c.Attribution == "user_note" {
			continue
		}
		if c.Source < 0 || c.Source >= len(m.Sources) {
			return errors.New("claim_source_missing")
		}
		src := m.Sources[c.Source]
		if !src.Verified || c.Locator == "" || c.Excerpt == "" {
			return errors.New("claim_evidence_missing")
		}
		if c.Field == "result" {
			observed := map[string]bool{}
			for _, n := range resultNumbers(c.Excerpt) {
				observed[n] = true
			}
			for _, n := range resultNumbers(c.Text) {
				if !observed[n] {
					return errors.New("result_number_not_in_evidence")
				}
			}
		}
		if (src.ReadingScope == "abstract_only" || src.ReadingScope == "metadata_only") && (c.Field == "experiment" || c.Field == "algorithm" || c.Field == "result") {
			return fmt.Errorf("claim_exceeds_reading_scope")
		}
	}
	return nil
}

var claimNumbers = regexp.MustCompile(`\d+(?:\.\d+)?(?:[eE][+-]?\d+)?`)

func resultNumbers(text string) []string {
	out := []string{}
	latin := func(b byte) bool { return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' }
	for _, span := range claimNumbers.FindAllStringIndex(text, -1) {
		if span[0] > 0 && latin(text[span[0]-1]) || span[1] < len(text) && latin(text[span[1]]) {
			continue
		}
		n := text[span[0]:span[1]]
		if strings.Contains(n, ".") && !strings.ContainsAny(n, "eE") {
			n = strings.TrimRight(strings.TrimRight(n, "0"), ".")
		}
		out = append(out, n)
	}
	return out
}

func uniqueAssets(xs []Asset) []Asset {
	out := []Asset{}
	seen := map[string]bool{}
	for _, x := range xs {
		if !seen[x.SHA256] {
			out = append(out, x)
			seen[x.SHA256] = true
		}
	}
	return out
}
