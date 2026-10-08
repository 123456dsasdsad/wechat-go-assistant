package jobs

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/123456dsasdsad/wechat-go-assistant/internal/conversations"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/files"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/models"
)

type executor interface {
	Exec(string, ...any) (sql.Result, error)
}

func validateSaved(j Job, name string) error {
	if !validID(j.ID) || name != j.ID+".json" || !models.ValidID(j.Model) || !models.ValidEffort(j.Effort) {
		return errors.New("unsupported_saved_job")
	}
	if !validSavedQuestions(j) || len(j.Progress) > 256<<10 {
		return errors.New("invalid_saved_questions_or_progress")
	}
	if !validAttachments(j.Attachments) || !files.ValidResults(j.Outputs) {
		return errors.New("invalid_saved_attachments")
	}
	if j.ConversationID != "" && !conversations.ValidID(j.ConversationID) {
		return errors.New("invalid_saved_conversation")
	}
	if j.Status != "queued" && j.Status != "running" && j.Status != "done" && j.Status != "delivered" {
		return errors.New("invalid_saved_status")
	}
	return nil
}

func (s *Store) initialize() error {
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS tasks (id TEXT PRIMARY KEY, owner TEXT NOT NULL, conversation TEXT NOT NULL, status TEXT NOT NULL, created INTEGER NOT NULL, outputs INTEGER NOT NULL, verification INTEGER NOT NULL, document BLOB NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS tasks_status_created ON tasks(status,created,id)`,
		`CREATE INDEX IF NOT EXISTS tasks_owner_created ON tasks(owner,created,id)`,
		`CREATE INDEX IF NOT EXISTS tasks_conversation_created ON tasks(owner,conversation,outputs,created)`,
		`CREATE TABLE IF NOT EXISTS supplement_links (token TEXT PRIMARY KEY, job TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS question_links (kind TEXT NOT NULL, owner TEXT NOT NULL, token TEXT NOT NULL, job TEXT NOT NULL, PRIMARY KEY(kind,owner,token,job))`,
	} {
		if _, err := s.db.Exec(statement); err != nil {
			return err
		}
	}
	var marker []byte
	err := s.db.QueryRow("SELECT value FROM documents WHERE key='legacy_import_v1'").Scan(&marker)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		raw, e := os.ReadFile(filepath.Join(s.dir, entry.Name()))
		if e != nil {
			return e
		}
		var j Job
		if json.Unmarshal(raw, &j) != nil {
			return errors.New("invalid_saved_job")
		}
		if j.Effort == "" {
			j.Effort = "high"
		}
		if e = validateSaved(j, entry.Name()); e != nil {
			return e
		}
		if e = s.put(tx, j); e != nil {
			return e
		}
	}
	if _, err = tx.Exec("INSERT INTO documents(key,value) VALUES('legacy_import_v1','complete')"); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) put(exec executor, j Job) error {
	raw, err := json.Marshal(j)
	if err != nil {
		return err
	}
	// A transaction keeps each question index and its task document consistent.
	if db, ok := exec.(*sql.DB); ok {
		tx, e := db.Begin()
		if e != nil {
			return e
		}
		defer tx.Rollback()
		if e = s.put(tx, j); e != nil {
			return e
		}
		return tx.Commit()
	}
	_, err = exec.Exec(`INSERT INTO tasks(id,owner,conversation,status,created,outputs,verification,document) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET owner=excluded.owner,conversation=excluded.conversation,status=excluded.status,created=excluded.created,outputs=excluded.outputs,verification=excluded.verification,document=excluded.document`, j.ID, j.Owner, j.ConversationID, j.Status, j.Created.UnixNano(), len(j.Outputs), j.VerificationOnly, raw)
	if err != nil {
		return err
	}
	if _, err = exec.Exec("DELETE FROM question_links WHERE job=?", j.ID); err != nil {
		return err
	}
	for _, v := range j.Supplements {
		if _, err = exec.Exec("INSERT OR REPLACE INTO supplement_links VALUES(?,?)", v.ID, j.ID); err != nil {
			return err
		}
	}
	for _, q := range j.Questions {
		for i, item := range q.Request.Questions {
			if _, err = exec.Exec("INSERT OR IGNORE INTO question_links VALUES('code',?,?,?)", j.Owner, QuestionCode(q, i), j.ID); err != nil {
				return err
			}
			for _, id := range q.Messages[item.ID] {
				if _, err = exec.Exec("INSERT OR IGNORE INTO question_links VALUES('message',?,?,?)", j.Owner, id, j.ID); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (s *Store) lookup(id string) (Job, bool) {
	var raw []byte
	err := s.db.QueryRow("SELECT document FROM tasks WHERE id=?", id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, false
	}
	if err != nil {
		s.readErr = err
		return Job{}, false
	}
	var j Job
	if err = json.Unmarshal(raw, &j); err != nil {
		s.readErr = err
		return Job{}, false
	}
	return j, true
}

func (s *Store) query(where string, args ...any) []Job {
	rows, err := s.db.Query("SELECT document FROM tasks WHERE "+where+" ORDER BY created,id", args...)
	if err != nil {
		s.readErr = err
		return nil
	}
	defer rows.Close()
	out := []Job{}
	for rows.Next() {
		var raw []byte
		var j Job
		if err = rows.Scan(&raw); err != nil {
			s.readErr = err
			return nil
		}
		if err = json.Unmarshal(raw, &j); err != nil {
			s.readErr = err
			return nil
		}
		out = append(out, j)
	}
	if err = rows.Err(); err != nil {
		s.readErr = err
		return nil
	}
	return out
}

func (s *Store) Recent(owner string, limit int) []Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit < 1 {
		return nil
	}
	if limit > 100 {
		limit = 100
	}
	return s.query("id IN (SELECT id FROM tasks WHERE owner=? AND verification=0 ORDER BY created DESC,id DESC LIMIT ?)", owner, limit)
}

func (s *Store) Active() []Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.query("status IN ('queued','running')")
}
func (s *Store) DeliveryHistory() []Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Include delivered media metadata to preserve newer-version suppression.
	return s.query("status='done' OR id IN (SELECT id FROM tasks WHERE outputs>0 AND status='delivered' GROUP BY owner,conversation HAVING created=MAX(created))")
}
func (s *Store) Superseded(j Job) bool {
	if j.MediaRequested || j.ConversationID == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var exists bool
	err := s.db.QueryRow("SELECT EXISTS(SELECT 1 FROM tasks WHERE owner=? AND conversation=? AND outputs>0 AND status IN ('done','delivered') AND created>?)", j.Owner, j.ConversationID, j.Created.UnixNano()).Scan(&exists)
	if err != nil {
		s.readErr = err
	}
	return exists
}
func (s *Store) Before(j Job) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM tasks WHERE owner=? AND id<>? AND status IN ('queued','running') AND created<?", j.Owner, j.ID, j.Created.UnixNano()).Scan(&count); err != nil {
		s.readErr = err
	}
	return count
}

// Each visits history in bounded pages and never retains the whole archive.
func (s *Store) Each(fn func(Job) error) error {
	var created int64 = -1 << 63
	id := ""
	for {
		s.mu.Lock()
		page := s.query("id IN (SELECT id FROM tasks WHERE created>? OR (created=? AND id>?) ORDER BY created,id LIMIT 64)", created, created, id)
		err := s.readErr
		s.mu.Unlock()
		if err != nil {
			return err
		}
		if len(page) == 0 {
			return nil
		}
		for _, j := range page {
			if err := fn(j); err != nil {
				return err
			}
			created = j.Created.UnixNano()
			id = j.ID
		}
	}
}

func (s *Store) Close() error { _, err := s.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); return err }

func (s *Store) SetMemory(id, memory string) error {
	if len(memory) > 80<<10 {
		return errors.New("memory_too_large")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.lookup(id)
	if !ok {
		return errors.New("unknown_memory_task")
	}
	if j.Status != "queued" || j.Memory != "" {
		return nil
	}
	j.Memory = memory
	return s.save(j)
}

func (s *Store) EnqueuePersonalized(source, input, owner, replyContext string, choice models.Choice, refs []files.Ref, cid, memory string) (Job, error) {
	if len(memory) > 80<<10 {
		return Job{}, errors.New("memory_too_large")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	j, e := s.enqueue(source, input, owner, replyContext, choice, refs, cid)
	if e != nil {
		return Job{}, e
	}
	if j.Status == "queued" && j.Memory == "" && memory != "" {
		j.Memory = memory
		e = s.save(j)
	}
	return j, e
}
