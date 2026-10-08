package jobs

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/conversations"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/files"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/models"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/usage"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Job struct {
	TrainingResult       string         `json:"training_result,omitempty"`
	ExpectedOutputs      []OutputIntent `json:"expected_outputs,omitempty"`
	OutputPending        bool           `json:"output_pending,omitempty"`
	Training             Training       `json:"training"`
	Initialized          bool           `json:"initialized,omitempty"`
	CancelRequested      bool           `json:"cancel_requested,omitempty"`
	ParentID             string         `json:"parent_id,omitempty"`
	Project              string         `json:"project,omitempty"`
	BudgetReached        bool           `json:"budget_reached,omitempty"`
	BudgetUSD            float64        `json:"budget_usd,omitempty"`
	Usage                usage.Tokens   `json:"usage"`
	Memory               string         `json:"memory,omitempty"`
	Questions            []UserQuestion `json:"questions,omitempty"`
	ID                   string         `json:"id"`
	Input                string         `json:"input"`
	Model                string         `json:"model"`
	Effort               string         `json:"effort"`
	Owner                string         `json:"owner"`
	ReplyContext         string         `json:"reply_context"`
	Status               string         `json:"status"`
	Created              time.Time      `json:"created"`
	Lease                string         `json:"lease,omitempty"`
	LeaseUntil           time.Time      `json:"lease_until,omitempty"`
	Attempts             int            `json:"attempts"`
	Result               string         `json:"result,omitempty"`
	Progress             string         `json:"progress,omitempty"`
	ProgressSequence     uint64         `json:"progress_sequence,omitempty"`
	ProgressUpdated      time.Time      `json:"progress_updated,omitempty"`
	Error                string         `json:"error,omitempty"`
	ToolCount            int            `json:"tool_count"`
	Attachments          []files.Ref    `json:"attachments,omitempty"`
	ConversationID       string         `json:"conversation_id,omitempty"`
	LeaseRenewals        int            `json:"lease_renewals,omitempty"`
	Outputs              []files.Ref    `json:"outputs,omitempty"`
	DeliveryParts        []string       `json:"delivery_parts,omitempty"`
	DeliveryText         string         `json:"delivery_text,omitempty"`
	MediaRequested       bool           `json:"media_requested,omitempty"`
	MediaDeferred        bool           `json:"media_deferred,omitempty"`
	MediaReplyContext    string         `json:"media_reply_context,omitempty"`
	MediaPackage         files.Ref      `json:"media_package,omitempty"`
	MediaPackageRequired bool           `json:"media_package_required,omitempty"`
	VerificationOnly     bool           `json:"verification_only,omitempty"`
	Supplements          []Supplement   `json:"supplements,omitempty"`
}

type Task struct {
	Project        string      `json:"project,omitempty"`
	BudgetUSD      float64     `json:"budget_usd,omitempty"`
	Memory         string      `json:"memory,omitempty"`
	ID             string      `json:"id"`
	Input          string      `json:"input"`
	Model          string      `json:"model"`
	Effort         string      `json:"effort"`
	Lease          string      `json:"lease"`
	Attachments    []files.Ref `json:"attachments,omitempty"`
	ConversationID string      `json:"conversation_id,omitempty"`
}
type Completion struct {
	ExpectedOutputs []OutputIntent `json:"expected_outputs,omitempty"`
	OutputPending   bool           `json:"output_pending,omitempty"`
	Usage           usage.Tokens   `json:"usage"`
	ID              string         `json:"id"`
	Lease           string         `json:"lease"`
	Result          string         `json:"result"`
	Error           string         `json:"error,omitempty"`
	ToolCount       int            `json:"tool_count"`
	Outputs         []files.Ref    `json:"outputs,omitempty"`
	SteerReceipts   []SteerReceipt `json:"steer_receipts,omitempty"`
}
type Store struct {
	mu      sync.Mutex
	dir     string
	db      *sql.DB
	readErr error
}

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	db, err := metadb.Open(filepath.Join(dir, "tasks.sqlite"))
	if err != nil {
		return nil, err
	}
	metadb.KeepOpen(db)
	s := &Store{dir: dir, db: db}
	if err = s.initialize(); err != nil {
		db.Close()
		return nil, err
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
	if s.readErr != nil {
		return s.readErr
	}
	return s.put(s.db, j)
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
	if j, ok := s.lookup(id); ok {
		return j, nil
	}
	j := Job{ID: id, Input: input, Owner: owner, ReplyContext: replyContext, Model: choice.Model, Effort: choice.Effort, Status: "queued", Created: time.Now().UTC()}
	j.Attachments = append([]files.Ref(nil), refs...)
	j.ConversationID = conversationID
	return j, s.save(j)
}
func (s *Store) ordered() []Job { return s.query("1=1") }
func (s *Store) Claim(now time.Time) (*Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.query("status='running'") {
		if j.CancelRequested && !now.Before(j.LeaseUntil) {
			j.Status = "done"
			j.Error = "user_canceled"
			if e := s.save(j); e != nil {
				return nil, e
			}
		}
	}
	if s.maintenanceDrained(now) {
		return nil, nil
	}
	ordered := s.query("status IN ('queued','running')")
	if s.readErr != nil {
		return nil, s.readErr
	}
	active := map[string]bool{}
	for _, j := range ordered {
		if j.Status == "running" && now.Before(j.LeaseUntil) {
			active[j.Owner+"\x00"+j.ConversationID] = true
		}
	}
	for _, j := range ordered {
		if active[j.Owner+"\x00"+j.ConversationID] {
			continue
		}
		if j.Status != "queued" && (j.Status != "running" || now.Before(j.LeaseUntil)) {
			continue
		}
		cancelQuestions(&j)
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
		return &Task{Project: j.Project, BudgetUSD: j.BudgetUSD, Memory: j.Memory, ID: j.ID, Input: j.Input, Model: j.Model, Effort: j.Effort, Lease: j.Lease, Attachments: append([]files.Ref(nil), j.Attachments...), ConversationID: j.ConversationID}, nil
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
	for _, j := range s.query("status='running'") {
		if j.Status == "running" {
			return errors.New("ai_task_running")
		}
	}
	if s.readErr != nil {
		return s.readErr
	}
	return os.WriteFile(filepath.Join(filepath.Dir(s.dir), "maintenance-drain.flag"), []byte(strconv.FormatInt(until.Unix(), 10)), 0600)
}

func (s *Store) Attachment(jobID, lease, fileID string, now time.Time) (files.Ref, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.lookup(jobID)
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
	j, ok := s.lookup(c.ID)
	if !ok || j.Lease != c.Lease || c.Lease == "" {
		return errors.New("unknown or stale task lease")
	}
	if j.CancelRequested && (j.Status == "done" || j.Status == "delivered") && j.Error == "user_canceled" {
		return nil
	}
	if j.CancelRequested {
		c.Result = ""
		c.Error = "user_canceled"
		c.Outputs = nil
		c.ExpectedOutputs = nil
		c.OutputPending = false
	}
	if !validIntents(c.ExpectedOutputs) || (c.OutputPending && (len(c.ExpectedOutputs) == 0 || len(c.Outputs) > 0)) {
		return errors.New("invalid_output_intent")
	}
	if !c.Usage.Valid() {
		return errors.New("invalid_usage")
	}
	if j.Status == "done" || j.Status == "delivered" {
		if j.Result == c.Result && j.Error == c.Error && j.ToolCount == c.ToolCount && (sameOutputs(j.Outputs, c.Outputs) || (len(c.Outputs) == 0 && c.OutputPending && reflect.DeepEqual(j.ExpectedOutputs, c.ExpectedOutputs))) {
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
	if c.Error == "" && j.WaitingForUser() {
		return errors.New("user_answer_required")
	}
	cancelQuestions(&j)
	j.Result = c.Result
	j.Error = c.Error
	j.ToolCount = c.ToolCount
	j.Usage = c.Usage
	if j.BudgetUSD > 0 {
		_, byConversation, e := s.usageLocked(j.Owner)
		if e != nil {
			return e
		}
		total := byConversation[j.ConversationID].USD
		if v, ok := Cost(j); ok {
			total += v
		}
		j.BudgetReached = total >= j.BudgetUSD
	}
	j.Outputs = append([]files.Ref(nil), c.Outputs...)
	j.ExpectedOutputs = append([]OutputIntent(nil), c.ExpectedOutputs...)
	j.OutputPending = c.OutputPending
	j.MediaPackageRequired = len(j.Outputs) > 1
	j.Status = "done"
	if !j.CancelRequested {
		if err := s.finishSupplements(&j, c.SteerReceipts); err != nil {
			return err
		}
	}
	return s.save(j)
}

func (s *Store) Heartbeat(id, lease string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.lookup(id)
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
	for _, j := range s.query("status='done'") {
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
		out[i] = copyQuestions(out[i])
	}
	return out
}
func (s *Store) Snapshot(id string) (Job, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.lookup(id)
	j.Attachments = append([]files.Ref(nil), j.Attachments...)
	j.Outputs = append([]files.Ref(nil), j.Outputs...)
	j.DeliveryParts = append([]string(nil), j.DeliveryParts...)
	j.Supplements = append([]Supplement(nil), j.Supplements...)
	return copyQuestions(j), ok
}
func (s *Store) Delivered(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.lookup(id)
	if !ok || j.Status != "done" {
		return errors.New("task is not ready")
	}
	j.Status = "delivered"
	return s.save(j)
}
