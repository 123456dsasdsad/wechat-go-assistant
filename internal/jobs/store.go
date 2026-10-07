package jobs

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/conversations"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/files"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/models"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Job struct {
	ID                   string       `json:"id"`
	Input                string       `json:"input"`
	Model                string       `json:"model"`
	Effort               string       `json:"effort"`
	Owner                string       `json:"owner"`
	ReplyContext         string       `json:"reply_context"`
	Status               string       `json:"status"`
	Created              time.Time    `json:"created"`
	Lease                string       `json:"lease,omitempty"`
	LeaseUntil           time.Time    `json:"lease_until,omitempty"`
	Attempts             int          `json:"attempts"`
	Result               string       `json:"result,omitempty"`
	Progress             string       `json:"progress,omitempty"`
	ProgressSequence     uint64       `json:"progress_sequence,omitempty"`
	ProgressUpdated      time.Time    `json:"progress_updated,omitempty"`
	Error                string       `json:"error,omitempty"`
	ToolCount            int          `json:"tool_count"`
	Attachments          []files.Ref  `json:"attachments,omitempty"`
	ConversationID       string       `json:"conversation_id,omitempty"`
	LeaseRenewals        int          `json:"lease_renewals,omitempty"`
	Outputs              []files.Ref  `json:"outputs,omitempty"`
	DeliveryParts        []string     `json:"delivery_parts,omitempty"`
	DeliveryText         string       `json:"delivery_text,omitempty"`
	MediaRequested       bool         `json:"media_requested,omitempty"`
	MediaDeferred        bool         `json:"media_deferred,omitempty"`
	MediaReplyContext    string       `json:"media_reply_context,omitempty"`
	MediaPackage         files.Ref    `json:"media_package,omitempty"`
	MediaPackageRequired bool         `json:"media_package_required,omitempty"`
	VerificationOnly     bool         `json:"verification_only,omitempty"`
	Supplements          []Supplement `json:"supplements,omitempty"`
}

type Task struct {
	ID             string      `json:"id"`
	Input          string      `json:"input"`
	Model          string      `json:"model"`
	Effort         string      `json:"effort"`
	Lease          string      `json:"lease"`
	Attachments    []files.Ref `json:"attachments,omitempty"`
	ConversationID string      `json:"conversation_id,omitempty"`
}
type Completion struct {
	ID            string         `json:"id"`
	Lease         string         `json:"lease"`
	Result        string         `json:"result"`
	Error         string         `json:"error,omitempty"`
	ToolCount     int            `json:"tool_count"`
	Outputs       []files.Ref    `json:"outputs,omitempty"`
	SteerReceipts []SteerReceipt `json:"steer_receipts,omitempty"`
}
type Store struct {
	mu    sync.Mutex
	dir   string
	items map[string]Job
}

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	s := &Store{dir: dir, items: map[string]Job{}}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		var j Job
		if err = json.Unmarshal(b, &j); err != nil {
			return nil, errors.New("invalid saved job")
		}
		if j.Effort == "" {
			j.Effort = "high"
		}
		if len(j.Progress) > 256<<10 {
			return nil, errors.New("invalid_saved_progress")
		}
		if !validID(j.ID) || e.Name() != j.ID+".json" || !models.ValidID(j.Model) || !models.ValidEffort(j.Effort) {
			return nil, errors.New("unsupported saved job")
		}
		if !validAttachments(j.Attachments) || !files.ValidResults(j.Outputs) {
			return nil, errors.New("invalid_saved_attachments")
		}
		if j.ConversationID != "" && !conversations.ValidID(j.ConversationID) {
			return nil, errors.New("invalid_saved_conversation")
		}
		s.items[j.ID] = j
	}
	return s, nil
}
func validID(id string) bool {
	if len(id) != 24 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}
func (s *Store) save(j Job) error {
	b, err := json.Marshal(j)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(s.dir, ".job-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmp, filepath.Join(s.dir, j.ID+".json")); err != nil {
		return err
	}
	s.items[j.ID] = j
	return nil
}
func (s *Store) Enqueue(source, input, owner, replyContext string) (Job, error) {
	return s.EnqueueSelected(source, input, owner, replyContext, models.Choice{Model: "gpt-6-sol", Effort: "high"})
}
func (s *Store) EnqueueSelected(source, input, owner, replyContext string, choice models.Choice) (Job, error) {
	return s.EnqueueFiles(source, input, owner, replyContext, choice, nil)
}
func validAttachments(refs []files.Ref) bool {
	if len(refs) > 4 {
		return false
	}
	seen := map[string]bool{}
	for _, ref := range refs {
		if !files.ValidRef(ref) || seen[ref.ID] {
			return false
		}
		seen[ref.ID] = true
	}
	return true
}
func (s *Store) EnqueueFiles(source, input, owner, replyContext string, choice models.Choice, refs []files.Ref) (Job, error) {
	return s.EnqueueConversation(source, input, owner, replyContext, choice, refs, "")
}
func (s *Store) EnqueueConversation(source, input, owner, replyContext string, choice models.Choice, refs []files.Ref, conversationID string) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.enqueue(source, input, owner, replyContext, choice, refs, conversationID)
}
func (s *Store) enqueue(source, input, owner, replyContext string, choice models.Choice, refs []files.Ref, conversationID string) (Job, error) {
	if source == "" || strings.TrimSpace(input) == "" || len(input) > 8192 || owner == "" || replyContext == "" || !models.ValidID(choice.Model) || !models.ValidEffort(choice.Effort) || !validAttachments(refs) || (conversationID != "" && !conversations.ValidID(conversationID)) {
		return Job{}, errors.New("invalid task input")
	}
	h := sha256.Sum256([]byte(source))
	id := hex.EncodeToString(h[:12])
	if j, ok := s.items[id]; ok {
		return j, nil
	}
	j := Job{ID: id, Input: input, Owner: owner, ReplyContext: replyContext, Model: choice.Model, Effort: choice.Effort, Status: "queued", Created: time.Now().UTC()}
	j.Attachments = append([]files.Ref(nil), refs...)
	j.ConversationID = conversationID
	return j, s.save(j)
}
func (s *Store) ordered() []Job {
	out := make([]Job, 0, len(s.items))
	for _, j := range s.items {
		out = append(out, j)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.Before(out[j].Created) })
	return out
}
func (s *Store) Claim(now time.Time) (*Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.maintenanceDrained(now) {
		return nil, nil
	}
	for _, j := range s.ordered() {
		if j.Status == "running" && now.Before(j.LeaseUntil) {
			return nil, nil
		}
	}
	for _, j := range s.ordered() {
		if j.Status != "queued" && (j.Status != "running" || now.Before(j.LeaseUntil)) {
			continue
		}
		if j.Attempts >= 2 {
			j.Status = "done"
			j.Error = "worker_lease_expired"
			if err := s.finishSupplements(&j, nil); err != nil {
				return nil, err
			}
			if err := s.save(j); err != nil {
				return nil, err
			}
			continue
		}
		var b [24]byte
		if _, err := rand.Read(b[:]); err != nil {
			return nil, err
		}
		j.Lease = hex.EncodeToString(b[:])
		j.LeaseUntil = now.Add(4 * time.Minute)
		if len(j.Attachments) > 0 {
			j.LeaseUntil = now.Add(10 * time.Minute)
		}
		j.Status = "running"
		j.Progress = ""
		j.ProgressSequence = 0
		j.ProgressUpdated = time.Time{}
		j.Attempts++
		if err := s.save(j); err != nil {
			return nil, err
		}
		return &Task{ID: j.ID, Input: j.Input, Model: j.Model, Effort: j.Effort, Lease: j.Lease, Attachments: append([]files.Ref(nil), j.Attachments...), ConversationID: j.ConversationID}, nil
	}
	return nil, nil
}

func (s *Store) maintenanceDrained(now time.Time) bool {
	raw, e := os.ReadFile(filepath.Join(filepath.Dir(s.dir), "maintenance-drain.flag"))
	if e != nil {
		return false
	}
	until, e := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	return e == nil && until > now.Unix() && until < now.Add(20*time.Minute).Unix()
}

// BeginMaintenance is atomic with Claim: no task can slip through the idle check.
func (s *Store) BeginMaintenance(until time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.maintenanceDrained(time.Now()) {
		return errors.New("maintenance_busy")
	}
	for _, j := range s.items {
		if j.Status == "running" {
			return errors.New("ai_task_running")
		}
	}
	return os.WriteFile(filepath.Join(filepath.Dir(s.dir), "maintenance-drain.flag"), []byte(strconv.FormatInt(until.Unix(), 10)), 0600)
}

func (s *Store) Attachment(jobID, lease, fileID string, now time.Time) (files.Ref, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.items[jobID]
	if !ok || j.Status != "running" || lease == "" || j.Lease != lease || !now.Before(j.LeaseUntil) {
		return files.Ref{}, errors.New("invalid_attachment_lease")
	}
	for _, ref := range j.Attachments {
		if ref.ID == fileID {
			return ref, nil
		}
	}
	return files.Ref{}, errors.New("attachment_not_in_task")
}
func (s *Store) Complete(c Completion, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.items[c.ID]
	if !ok || j.Lease != c.Lease || c.Lease == "" {
		return errors.New("unknown or stale task lease")
	}
	if j.Status == "done" || j.Status == "delivered" {
		if j.Result == c.Result && j.Error == c.Error && j.ToolCount == c.ToolCount && sameOutputs(j.Outputs, c.Outputs) {
			return nil
		}
		return errors.New("conflicting task result")
	}
	if j.Status != "running" || now.After(j.LeaseUntil) {
		return errors.New("expired task lease")
	}
	if len(c.Result) > 64*1024 || len(c.Error) > 80 || c.ToolCount < 0 || !files.ValidResults(c.Outputs) || (strings.TrimSpace(c.Result) == "" && c.Error == "") {
		return errors.New("invalid task result")
	}
	j.Result = c.Result
	j.Error = c.Error
	j.ToolCount = c.ToolCount
	j.Outputs = append([]files.Ref(nil), c.Outputs...)
	j.MediaPackageRequired = len(j.Outputs) > 1
	j.Status = "done"
	if err := s.finishSupplements(&j, c.SteerReceipts); err != nil {
		return err
	}
	return s.save(j)
}

func (s *Store) Heartbeat(id, lease string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.items[id]
	if !ok || lease == "" || j.Lease != lease || j.Status != "running" || !now.Before(j.LeaseUntil) {
		return errors.New("invalid_task_lease")
	}
	duration := 4 * time.Minute
	if len(j.Attachments) > 0 {
		duration = 10 * time.Minute
	}
	j.LeaseUntil = now.Add(duration)
	j.LeaseRenewals++
	return s.save(j)
}
func (s *Store) Ready() []Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Job{}
	for _, j := range s.ordered() {
		if j.Status == "done" {
			out = append(out, j)
		}
	}
	return out
}

func (s *Store) History() []Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.ordered()
	for i := range out {
		out[i].Attachments = append([]files.Ref(nil), out[i].Attachments...)
		out[i].Outputs = append([]files.Ref(nil), out[i].Outputs...)
		out[i].DeliveryParts = append([]string(nil), out[i].DeliveryParts...)
		out[i].Supplements = append([]Supplement(nil), out[i].Supplements...)
	}
	return out
}
func (s *Store) Snapshot(id string) (Job, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.items[id]
	j.Attachments = append([]files.Ref(nil), j.Attachments...)
	j.Outputs = append([]files.Ref(nil), j.Outputs...)
	j.DeliveryParts = append([]string(nil), j.DeliveryParts...)
	j.Supplements = append([]Supplement(nil), j.Supplements...)
	return j, ok
}
func (s *Store) Delivered(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.items[id]
	if !ok || j.Status != "done" {
		return errors.New("task is not ready")
	}
	j.Status = "delivered"
	return s.save(j)
}
