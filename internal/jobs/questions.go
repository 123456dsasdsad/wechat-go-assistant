package jobs

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/123456dsasdsad/wechat-go-assistant/internal/userinput"
)

type UserQuestion struct {
	ID       string                      `json:"id"`
	Attempt  int                         `json:"attempt"`
	Request  userinput.Request           `json:"request"`
	State    string                      `json:"state"`
	Answers  map[string]userinput.Answer `json:"answers"`
	Sources  map[string]string           `json:"sources"`  // hashed inbound message ID -> question ID
	Messages map[string][]string         `json:"messages"` // question ID -> accepted outbound IDs
	Created  time.Time                   `json:"created"`
}
type QuestionRequest struct {
	ID         string            `json:"id"`
	Lease      string            `json:"lease"`
	QuestionID string            `json:"question_id,omitempty"`
	Request    userinput.Request `json:"request,omitempty"`
}

func copyQuestions(j Job) Job {
	if len(j.Questions) == 0 {
		return j
	}
	raw, _ := json.Marshal(j.Questions)
	j.Questions = nil
	_ = json.Unmarshal(raw, &j.Questions)
	return j
}
func (j Job) WaitingForUser() bool {
	if j.Status != "running" {
		return false
	}
	for _, q := range j.Questions {
		if q.Attempt == j.Attempts && q.State == "pending" {
			return true
		}
	}
	return false
}
func (s *Store) PublishQuestion(r QuestionRequest, now time.Time) (UserQuestion, error) {
	if e := userinput.Validate(r.Request); e != nil {
		return UserQuestion{}, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	j, e := s.steerJob(r.ID, r.Lease, now)
	if e != nil {
		return UserQuestion{}, e
	}
	j = copyQuestions(j)
	h := sha256.Sum256([]byte(j.ID + "\x00" + r.Lease + "\x00" + r.Request.ID))
	id := hex.EncodeToString(h[:12])
	for _, q := range j.Questions {
		if q.ID == id {
			a, _ := json.Marshal(q.Request)
			b, _ := json.Marshal(r.Request)
			if string(a) != string(b) {
				return UserQuestion{}, errors.New("conflicting_user_question")
			}
			return q, nil
		}
	}
	if len(j.Questions) >= 64 {
		return UserQuestion{}, errors.New("too_many_user_questions")
	}
	q := UserQuestion{ID: id, Attempt: j.Attempts, Request: r.Request, State: "pending", Created: now.UTC(), Answers: map[string]userinput.Answer{}, Sources: map[string]string{}, Messages: map[string][]string{}}
	j.Questions = append(j.Questions, q)
	return q, s.save(j)
}
func (s *Store) PollQuestion(r QuestionRequest, now time.Time) (UserQuestion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, e := s.steerJob(r.ID, r.Lease, now)
	if e != nil {
		return UserQuestion{}, e
	}
	for _, q := range copyQuestions(j).Questions {
		if q.ID == r.QuestionID && q.Attempt == j.Attempts && (q.State == "pending" || q.State == "answered" || q.State == "resolved") {
			return q, nil
		}
	}
	return UserQuestion{}, errors.New("stale_user_question")
}
func (s *Store) ResolveQuestion(r QuestionRequest, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, e := s.steerJob(r.ID, r.Lease, now)
	if e != nil {
		return e
	}
	j = copyQuestions(j)
	for i, q := range j.Questions {
		if q.ID == r.QuestionID && q.Attempt == j.Attempts {
			if q.State != "answered" && q.State != "resolved" {
				return errors.New("unanswered_user_question")
			}
			j.Questions[i].State = "resolved"
			return s.save(j)
		}
	}
	return errors.New("stale_user_question")
}
func QuestionCode(batch UserQuestion, index int) string {
	return fmt.Sprintf("%s-%d", batch.ID[:8], index+1)
}

// FindQuotedQuestion resolves only IDs actually returned by WeChat for this owner.
// Completed questions are also returned so quoting an old prompt cannot create a new task.
func (s *Store) FindQuotedQuestion(owner, messageID string) (Job, UserQuestion, int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if messageID == "" {
		return Job{}, UserQuestion{}, 0, false
	}
	for _, j := range s.query("id IN (SELECT job FROM question_links WHERE kind='message' AND owner=? AND token=?)", owner, messageID) {
		if j.Owner != owner {
			continue
		}
		for _, q := range j.Questions {
			for i, item := range q.Request.Questions {
				for _, id := range q.Messages[item.ID] {
					if id == messageID {
						return copyQuestions(j), q, i, true
					}
				}
			}
		}
	}
	return Job{}, UserQuestion{}, 0, false
}
func (s *Store) FindQuestionCode(owner, code string) (Job, UserQuestion, int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var job Job
	var batch UserQuestion
	index := 0
	found := false
	for _, j := range s.query("id IN (SELECT job FROM question_links WHERE kind='code' AND owner=? AND token=?)", owner, code) {
		if j.Owner != owner {
			continue
		}
		for _, q := range j.Questions {
			for i := range q.Request.Questions {
				if QuestionCode(q, i) == code {
					if found {
						return Job{}, UserQuestion{}, 0, false
					}
					job = copyQuestions(j)
					batch = q
					index = i
					found = true
				}
			}
		}
	}
	return job, batch, index, found
}
func (s *Store) RecordQuestionMessage(jobID, batchID, qid, messageID string) error {
	if messageID == "" || len(messageID) > 256 {
		return errors.New("missing_question_message_id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.lookup(jobID)
	if !ok {
		return errors.New("unknown_question_task")
	}
	j = copyQuestions(j)
	for i, q := range j.Questions {
		if q.ID != batchID {
			continue
		}
		if q.Attempt != j.Attempts {
			return errors.New("stale_question_attempt")
		}
		for _, item := range q.Request.Questions {
			if item.ID == qid {
				for _, id := range q.Messages[qid] {
					if id == messageID {
						return nil
					}
				}
				if len(q.Messages[qid]) >= 8 {
					return errors.New("too_many_question_deliveries")
				}
				j.Questions[i].Messages[qid] = append(q.Messages[qid], messageID)
				return s.save(j)
			}
		}
	}
	return errors.New("unknown_user_question")
}
func (s *Store) AnswerQuestion(owner, jobID, batchID, qid, source, text string, now time.Time) (bool, error) {
	if source == "" || len(source) > 512 {
		return false, errors.New("invalid_answer_source")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.lookup(jobID)
	if !ok || j.Owner != owner {
		return false, errors.New("unknown_user_question")
	}
	j = copyQuestions(j)
	h := sha256.Sum256([]byte(source))
	receipt := hex.EncodeToString(h[:])
	for i, q := range j.Questions {
		if q.ID != batchID {
			continue
		}
		for _, item := range q.Request.Questions {
			if item.ID != qid {
				continue
			}
			value, e := userinput.Normalize(item, text)
			if e != nil {
				return false, e
			}
			if old, exists := q.Sources[receipt]; exists {
				if old == qid && len(q.Answers[qid].Answers) == 1 && q.Answers[qid].Answers[0] == value {
					return q.State != "pending", nil
				}
				return false, errors.New("conflicting_question_answer")
			}
			if j.Status != "running" || !now.Before(j.LeaseUntil) || q.Attempt != j.Attempts || q.State != "pending" {
				return false, errors.New("question_no_longer_waiting")
			}
			if _, exists := q.Answers[qid]; exists {
				return false, errors.New("question_already_answered")
			}
			q.Answers[qid] = userinput.Answer{Answers: []string{value}}
			q.Sources[receipt] = qid
			complete := len(q.Answers) == len(q.Request.Questions)
			if complete {
				q.State = "answered"
			}
			j.Questions[i] = q
			return complete, s.save(j)
		}
	}
	return false, errors.New("unknown_user_question")
}
func cancelQuestions(j *Job) {
	*j = copyQuestions(*j)
	for i := range j.Questions {
		if j.Questions[i].State == "pending" || j.Questions[i].State == "answered" {
			j.Questions[i].State = "canceled"
		}
	}
}
func validSavedQuestions(j Job) bool {
	if len(j.Questions) > 64 {
		return false
	}
	for _, q := range j.Questions {
		if len(q.ID) != 24 || q.Attempt < 1 || userinput.Validate(q.Request) != nil || q.Answers == nil || q.Messages == nil || q.Sources == nil {
			return false
		}
		if !strings.Contains("|pending|answered|resolved|canceled|", "|"+q.State+"|") {
			return false
		}
	}
	return true
}
