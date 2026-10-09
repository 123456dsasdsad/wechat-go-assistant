package accountupload

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/files"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/maintenance"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Receipt struct {
	ID         string   `json:"id"`
	Operation  string   `json:"operation,omitempty"`
	AccountIDs []string `json:"account_ids,omitempty"`
	Status     string   `json:"status"`
	Count      int      `json:"account_count"`
	Duplicates int      `json:"duplicates"`
	Summary
	Created   time.Time `json:"created"`
	UpdatedAt time.Time `json:"updated_at"`
	Error     string    `json:"error,omitempty"`
}
type batch struct {
	Receipt
	Owner    string    `json:"owner"`
	Accounts []Account `json:"credentials,omitempty"`
	Notified bool      `json:"notified,omitempty"`
}
type Store struct {
	dir       string
	grants    *files.Store
	mu        sync.Mutex
	processMu sync.Mutex
}

func Open(dir string) (*Store, error) {
	if e := os.MkdirAll(filepath.Join(dir, "batches"), 0700); e != nil {
		return nil, errors.New("account_upload_store_unavailable")
	}
	grants, e := files.Open(filepath.Join(dir, "grants"))
	if e != nil {
		return nil, e
	}
	return &Store{dir: dir, grants: grants}, nil
}
func (s *Store) Grant(owner, source string) (string, error) {
	return s.grants.Grant(owner, "account-upload:"+source)
}
func (s *Store) Authorize(token string) (string, bool) { return s.grants.Authorize(token) }
func (s *Store) readBatch(id string) (batch, error) {
	var b batch
	if !batchID(id) {
		return b, errors.New("invalid_batch")
	}
	data, e := os.ReadFile(filepath.Join(s.dir, "batches", id+".json"))
	if e != nil || decode(data, &b) != nil || b.ID != id {
		return b, errors.New("batch_unavailable")
	}
	return b, nil
}
func (s *Store) save(b batch) error {
	return maintenance.AtomicJSON(filepath.Join(s.dir, "batches", b.ID+".json"), b)
}
func (s *Store) Stage(owner, source string, data []byte) (Receipt, error) {
	if owner == "" || source == "" {
		return Receipt{}, errors.New("invalid_upload_identity")
	}
	accounts, duplicates, e := Parse(data)
	if e != nil {
		return Receipt{}, e
	}
	canonical, _ := json.Marshal(accounts)
	hash := sha256.Sum256(append([]byte(owner+"\x00"+source+"\x00"), canonical...))
	id := hex.EncodeToString(hash[:12])
	s.mu.Lock()
	defer s.mu.Unlock()
	if b, e := s.readBatch(id); e == nil {
		return b.Receipt, nil
	}
	now := time.Now().UTC()
	b := batch{Receipt: Receipt{ID: id, Status: "pending", Count: len(accounts), Duplicates: duplicates, Created: now, UpdatedAt: now}, Owner: owner, Accounts: accounts}
	if e = s.save(b); e != nil {
		return Receipt{}, errors.New("account_upload_save_failed")
	}
	return b.Receipt, nil
}
func (s *Store) Status(owner, id string) (Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, e := s.readBatch(id)
	if e != nil || b.Owner != owner {
		return Receipt{}, errors.New("batch_unavailable")
	}
	return b.Receipt, nil
}
func (s *Store) list() []batch {
	var batches []batch
	entries, e := os.ReadDir(filepath.Join(s.dir, "batches"))
	if e != nil {
		return nil
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		b, e := s.readBatch(strings.TrimSuffix(entry.Name(), ".json"))
		if e == nil {
			batches = append(batches, b)
		}
	}
	sort.Slice(batches, func(i, j int) bool { return batches[i].Created.Before(batches[j].Created) })
	return batches
}
func (s *Store) Pending() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ids []string
	for _, b := range s.list() {
		if b.Status == "pending" || b.Status == "applied" || b.Status == "active" && !b.Notified {
			ids = append(ids, b.ID)
		}
	}
	return ids
}
func (s *Store) Recent(owner string) []Receipt {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Receipt
	all := s.list()
	for i := len(all) - 1; i >= 0 && len(out) < 5; i-- {
		if all[i].Owner == owner {
			out = append(out, all[i].Receipt)
		}
	}
	return out
}
func (s *Store) Receipt(id string) (Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, e := s.readBatch(id)
	return b.Receipt, e
}
func (s *Store) MarkNotified(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, e := s.readBatch(id)
	if e != nil {
		return e
	}
	b.Notified = true
	return s.save(b)
}

// Process writes once, then retries only activation if a gateway reload fails.
// The caller owns the cross-process mutation lock and relay maintenance lease.
func (s *Store) Process(id, root string, reload func() error) error {
	s.processMu.Lock()
	defer s.processMu.Unlock()
	s.mu.Lock()
	b, e := s.readBatch(id)
	s.mu.Unlock()
	if e != nil {
		return e
	}
	if b.Status == "active" {
		return nil
	}
	if b.Status != "pending" && b.Status != "applied" {
		return errors.New("batch_not_pending")
	}
	if b.Status == "pending" {
		var sum Summary
		if b.Operation == "delete" {
			sum, e = Delete(root, id, b.AccountIDs)
		} else {
			sum, e = Merge(root, id, b.Accounts)
		}
		if e != nil {
			b.Status = "failed"
			b.Error = e.Error()
			b.UpdatedAt = time.Now().UTC()
			s.mu.Lock()
			saveErr := s.save(b)
			s.mu.Unlock()
			if saveErr != nil {
				return saveErr
			}
			return errors.New("account_import_failed")
		}
		b.Summary = sum
		b.Status = "applied"
		b.Error = ""
		b.UpdatedAt = time.Now().UTC()
		s.mu.Lock()
		e = s.save(b)
		s.mu.Unlock()
		if e != nil {
			return errors.New("import_receipt_save_failed")
		}
	}
	if reload == nil || reload() != nil {
		b.Error = "reload_pending"
		b.UpdatedAt = time.Now().UTC()
		s.mu.Lock()
		e = s.save(b)
		s.mu.Unlock()
		if e != nil {
			return e
		}
		return errors.New("gateway_reload_pending")
	}
	b.Status = "active"
	b.Error = ""
	b.Accounts = nil
	b.UpdatedAt = time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	if e = s.save(b); e != nil {
		return errors.New("activation_receipt_save_failed")
	}
	return nil
}
func (r Receipt) Text() string {
	if r.Operation == "delete" {
		label := "账号删除回执（程序处理，未调用 AI）\n"
		id := r.ID
		if len(id) > 8 {
			id = id[:8]
		}
		switch r.Status {
		case "pending":
			return label + fmt.Sprintf("账号删除 %s：已接收 %d 个账号的删除请求，等待任务结束后生效。", id, r.Count)
		case "applied":
			return label + fmt.Sprintf("账号删除 %s：已移除 %d 个账号，等待网关重新加载。", id, r.Deleted)
		case "active":
			return label + fmt.Sprintf("账号删除 %s：已删除 %d 个账号，账号池剩余 %d 个，已生效。", id, r.Deleted, r.Total)
		default:
			return label + fmt.Sprintf("账号删除 %s：删除未完成，请刷新账号列表后重试。", id)
		}
	}
	const label = "账号导入回执（程序处理，未调用 AI）\n"
	id := r.ID
	if len(id) > 8 {
		id = id[:8]
	}
	switch r.Status {
	case "pending":
		return label + fmt.Sprintf("账号上传 %s：已接收 %d 个账号，等待当前任务结束后自动合并并启用。", id, r.Count)
	case "applied":
		return label + fmt.Sprintf("账号上传 %s：新增 %d、更新 %d，已保存，正在等待网关启用。", id, r.Added, r.Updated)
	case "active":
		return label + fmt.Sprintf("账号上传 %s：新增 %d、更新 %d，账号池共 %d 个，已生效。", id, r.Added, r.Updated, r.Total)
	default:
		return label + fmt.Sprintf("账号上传 %s：导入失败，现有账号已保留。请检查账号身份是否重复、格式是否完整，再获取新链接上传。", id)
	}
}
