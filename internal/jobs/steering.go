package jobs

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/models"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/steering"
	"strings"
	"time"
)

type Supplement struct {
	ID         string    `json:"id"`
	Input      string    `json:"input"`
	State      string    `json:"state"`
	Created    time.Time `json:"created"`
	TurnID     string    `json:"turn_id,omitempty"`
	FollowupID string    `json:"followup_id,omitempty"`
}
type SteerReceipt = steering.Receipt
type SteerRequest struct {
	ID           string `json:"id"`
	Lease        string `json:"lease"`
	SupplementID string `json:"supplement_id,omitempty"`
	State        string `json:"state,omitempty"`
	TurnID       string `json:"turn_id,omitempty"`
}

// SupplementMessage atomically snapshots the selected conversation. Its source
// receipt remains tied to the original parent even after that parent completes.
func (s *Store) SupplementMessage(source, input, owner, replyContext, cid string, choice models.Choice) (Job, Supplement, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if source == "" || strings.TrimSpace(input) == "" || len(input) > 8192 || owner == "" || replyContext == "" {
		return Job{}, Supplement{}, false, errors.New("invalid_supplement")
	}
	h := sha256.Sum256([]byte("supplement:" + source))
	id := hex.EncodeToString(h[:12])
	nextHash := sha256.Sum256([]byte("supplement-next:" + source))
	if j, ok := s.items[hex.EncodeToString(nextHash[:12])]; ok {
		if j.Owner != owner {
			return Job{}, Supplement{}, false, errors.New("supplement_owner_mismatch")
		}
		return j, Supplement{}, false, nil
	}
	for _, j := range s.items {
		for _, v := range j.Supplements {
			if v.ID == id {
				if j.Owner != owner {
					return Job{}, Supplement{}, false, errors.New("supplement_owner_mismatch")
				}
				return j, v, true, nil
			}
		}
	}
	for _, j := range s.ordered() {
		if j.Owner != owner || j.ConversationID != cid || j.Status != "running" {
			continue
		}
		if len(j.Supplements) >= 32 {
			return Job{}, Supplement{}, false, errors.New("too_many_supplements")
		}
		v := Supplement{ID: id, Input: input, State: "pending", Created: time.Now().UTC()}
		j.Supplements = append(append([]Supplement(nil), j.Supplements...), v)
		return j, v, true, s.save(j)
	}
	j, e := s.enqueue("supplement-next:"+source, input, owner, replyContext, choice, nil, cid)
	return j, Supplement{}, false, e
}
func (s *Store) PendingSupplements(id, lease string, now time.Time) ([]Supplement, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, e := s.steerJob(id, lease, now)
	if e != nil {
		return nil, e
	}
	out := []Supplement{}
	for _, v := range j.Supplements {
		if v.State == "pending" {
			out = append(out, v)
		}
	}
	return out, nil
}
func (s *Store) steerJob(id, lease string, now time.Time) (Job, error) {
	j, ok := s.items[id]
	if !ok || lease == "" || j.Lease != lease || j.Status != "running" || !now.Before(j.LeaseUntil) {
		return Job{}, errors.New("invalid_steer_lease")
	}
	return j, nil
}

// Reserve before writing RPC: a process crash or lost acknowledgement must not
// replay an instruction that may already have caused side effects.
func (s *Store) AckSupplement(r SteerRequest, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, e := s.steerJob(r.ID, r.Lease, now)
	if e != nil {
		return e
	}
	j.Supplements = append([]Supplement(nil), j.Supplements...)
	for i, v := range j.Supplements {
		if v.ID != r.SupplementID {
			continue
		}
		if r.State == "dispatching" {
			if v.State != "pending" || len(r.TurnID) > 80 || r.TurnID == "" {
				return errors.New("supplement_already_reserved")
			}
			v.State = r.State
			v.TurnID = r.TurnID
		} else {
			if r.State != "accepted" && r.State != "queued" && r.State != "uncertain" {
				return errors.New("invalid_steer_state")
			}
			if v.TurnID != r.TurnID || (v.State != "dispatching" && v.State != r.State) {
				return errors.New("conflicting_steer_receipt")
			}
			if r.State == "queued" {
				if e = s.queueSupplement(j, &v); e != nil {
					return e
				}
			} else {
				v.State = r.State
			}
		}
		j.Supplements[i] = v
		return s.save(j)
	}
	return errors.New("unknown_supplement")
}
func (s *Store) queueSupplement(parent Job, v *Supplement) error {
	child, e := s.enqueue("steer-followup:"+v.ID, v.Input, parent.Owner, parent.ReplyContext, models.Choice{Model: parent.Model, Effort: parent.Effort}, nil, parent.ConversationID)
	if e != nil {
		return e
	}
	if parent.VerificationOnly && !child.VerificationOnly {
		child.VerificationOnly = true
		if e = s.save(child); e != nil {
			return e
		}
	}
	v.State = "queued"
	v.FollowupID = child.ID
	return nil
}
func (s *Store) finishSupplements(j *Job, receipts []SteerReceipt) error {
	j.Supplements = append([]Supplement(nil), j.Supplements...)
	if len(receipts) > 32 {
		return errors.New("invalid_steer_receipts")
	}
	for _, r := range receipts {
		found := false
		for i, v := range j.Supplements {
			if v.ID != r.ID {
				continue
			}
			found = true
			if v.TurnID != r.TurnID || (r.State != "accepted" && r.State != "queued" && r.State != "uncertain") || (v.State != "dispatching" && v.State != r.State) {
				return errors.New("conflicting_steer_receipt")
			}
			v.State = r.State
			j.Supplements[i] = v
		}
		if !found {
			return errors.New("unknown_supplement")
		}
	}
	for i := range j.Supplements {
		v := &j.Supplements[i]
		if v.State == "pending" || v.State == "queued" {
			if e := s.queueSupplement(*j, v); e != nil {
				return e
			}
		} else if v.State == "dispatching" {
			v.State = "uncertain"
		}
	}
	return nil
}
