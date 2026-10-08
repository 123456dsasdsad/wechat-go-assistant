package jobs

import (
	"errors"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/files"
	"strconv"
	"strings"
	"time"
)

func sameOutputs(a, b []files.Ref) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// OutputOwner binds a result upload/completion to this active lease. Completed
// retries may validate ownership without reopening the job for execution.
func (s *Store) OutputOwner(id, lease string, now time.Time, completed bool) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.lookup(id)
	if !ok || lease == "" || j.Lease != lease {
		return "", errors.New("invalid_output_lease")
	}
	if completed && (j.Status == "done" || j.Status == "delivered") {
		return j.Owner, nil
	}
	if j.Status != "running" || !now.Before(j.LeaseUntil) {
		return "", errors.New("invalid_output_lease")
	}
	return j.Owner, nil
}
func (j Job) PartDelivered(part string) bool {
	for _, p := range j.DeliveryParts {
		if p == part {
			return true
		}
	}
	return false
}
func (s *Store) CommitPart(id, part string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.lookup(id)
	if !ok || j.Status != "done" {
		return errors.New("job_not_ready")
	}
	valid := part == "text"
	if strings.HasPrefix(part, "text:") && j.DeliveryText != "" {
		n, e := strconv.Atoi(strings.TrimPrefix(part, "text:"))
		count := (len([]rune(j.DeliveryText)) + 1999) / 2000
		valid = e == nil && n >= 0 && count > 1 && n < count && part == "text:"+strconv.Itoa(n)
	}
	for _, ref := range j.Outputs {
		if part == ref.ID {
			valid = true
		}
	}
	if j.MediaPackage.ID != "" && (part == j.MediaPackage.ID || part == "package-link:"+j.MediaPackage.ID) {
		valid = true
	}
	if !valid {
		return errors.New("invalid_delivery_part")
	}
	if j.PartDelivered(part) {
		return nil
	}
	j.DeliveryParts = append(append([]string(nil), j.DeliveryParts...), part)
	return s.save(j)
}

// Freeze exactly what is chunked before any send; retries cannot change the
// original question, title, signed link or chunk boundaries after a partial send.
func (s *Store) PrepareDeliveryText(id, text string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.lookup(id)
	if !ok || j.Status != "done" {
		return "", errors.New("job_not_ready")
	}
	if j.DeliveryText != "" {
		return j.DeliveryText, nil
	}
	if strings.TrimSpace(text) == "" || len(text) > 96*1024 {
		return "", errors.New("invalid_delivery_text")
	}
	j.DeliveryText = text
	if e := s.save(j); e != nil {
		return "", e
	}
	return text, nil
}

// RequestMedia only schedules remaining attachments; accepted parts and the AI
// result remain immutable, and this does not select or rerun a conversation.
func (s *Store) RequestMedia(owner, prefix string, replyContext ...string) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(prefix) != 8 && len(prefix) != 24 {
		return Job{}, errors.New("invalid_job_prefix")
	}
	var found *Job
	for _, j := range s.query("owner=? AND id LIKE ?", owner, prefix+"%") {
		if j.Owner == owner && strings.HasPrefix(j.ID, prefix) {
			if found != nil {
				return Job{}, errors.New("ambiguous_job_prefix")
			}
			copy := j
			found = &copy
		}
	}
	if found == nil || (found.Status != "done" && found.Status != "delivered") || len(found.Outputs) == 0 {
		return Job{}, errors.New("result_not_ready")
	}
	found.MediaRequested = true
	found.MediaDeferred = false
	if len(replyContext) > 0 {
		found.MediaReplyContext = replyContext[0]
	}
	for _, j := range s.query("owner=? AND status='done' AND outputs>0", owner) {
		if j.Owner == owner && j.ID != found.ID && j.Status == "done" && len(j.Outputs) > 0 {
			j.MediaDeferred = true
			j.MediaRequested = false
			if e := s.save(j); e != nil {
				return Job{}, e
			}
		}
	}
	return *found, s.save(*found)
}

func (j Job) MediaContext() string {
	if j.MediaReplyContext != "" {
		return j.MediaReplyContext
	}
	return j.ReplyContext
}

func hasPendingMedia(j Job) bool {
	if j.PackageDelivered() {
		return false
	}
	for _, ref := range j.Outputs {
		if !j.PartDelivered(ref.ID) {
			return true
		}
	}
	return false
}

func (j Job) PackageDelivered() bool {
	return j.MediaPackage.ID != "" && (j.PartDelivered(j.MediaPackage.ID) || j.PartDelivered("package-link:"+j.MediaPackage.ID))
}

// Relay-generated package does not mutate the Worker output snapshot or receipts.
func (s *Store) SetMediaPackage(id string, ref files.Ref) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.lookup(id)
	if !ok || (j.Status != "done" && j.Status != "delivered") || !files.ValidRef(ref) {
		return errors.New("invalid_media_package")
	}
	if j.MediaPackage.ID != "" {
		if j.MediaPackage != ref {
			return errors.New("media_package_conflict")
		}
		return nil
	}
	j.MediaPackage = ref
	return s.save(j)
}

// A new inbound message is not permission to resume an older attachment batch.
// Accepted receipts remain intact; running tasks can still publish new results.
func (s *Store) PauseOwnerMedia(owner string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.query("owner=? AND status='done' AND outputs>0", owner) {
		if j.Owner != owner || j.Status != "done" || !hasPendingMedia(j) || j.MediaDeferred {
			continue
		}
		j.MediaDeferred, j.MediaRequested = true, false
		if e := s.save(j); e != nil {
			return e
		}
	}
	return nil
}

// Do not let a failed, stale send pause a newer explicit replay request.
func (s *Store) DeferMedia(id, expectedContext string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.lookup(id)
	if !ok {
		return errors.New("unknown_media_job")
	}
	if j.Status != "done" || j.MediaContext() != expectedContext {
		return nil
	}
	j.MediaDeferred, j.MediaRequested = true, false
	return s.save(j)
}

// Admin-only acceptance fixtures keep real model results and receipts while
// withholding test notifications from the owner's normal conversation.
func (s *Store) MarkVerification(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.lookup(id)
	if !ok {
		return errors.New("unknown_verification_job")
	}
	j.VerificationOnly = true
	return s.save(j)
}
