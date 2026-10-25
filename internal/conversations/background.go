package conversations

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

// CreateBackground creates a user-requested research task conversation without
// changing the conversation selected on their phone. Source makes retries safe.
func (s *Store) CreateBackground(source, title string) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if source == "" {
		return Session{}, errors.New("conversation_command_id_required")
	}
	h := sha256.Sum256([]byte("background:" + source))
	key := hex.EncodeToString(h[:12])
	for _, v := range s.state.Sessions {
		if v.BackgroundSource == key {
			return v, nil
		}
	}
	for _, r := range s.state.Receipts {
		if r.ID == key {
			v, ok := s.state.Sessions[r.Reply]
			if !ok {
				return Session{}, errors.New("conversation_unavailable")
			}
			return v, nil
		}
	}
	if len(s.state.Sessions) >= 64 {
		return Session{}, errors.New("conversation_limit_reached")
	}
	v, e := newSession(title, s.state.Sessions)
	if e != nil {
		return v, e
	}
	v.BackgroundSource = key
	next := s.state
	next.Sessions = map[string]Session{}
	for k, x := range s.state.Sessions {
		next.Sessions[k] = x
	}
	next.Sessions[v.ID] = v
	next.Receipts = append(append([]receipt(nil), s.state.Receipts...), receipt{key, v.ID})
	trimReceipts(&next)
	if e = writeJSON(s.path, next); e != nil {
		return v, e
	}
	s.state = next
	return v, nil
}
