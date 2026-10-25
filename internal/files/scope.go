package files

import (
	"errors"
	"io"
	"sort"
)

// Scope is immutable: explicit cross-conversation reuse must create a new copy.
func (s *Store) BindConversation(owner, id, conversation string) error {
	if conversation == "" {
		return nil
	}
	if !validHex(conversation, 8) {
		return errors.New("invalid_file_conversation")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.state.Files[id]
	if !ok || r.Owner != owner {
		return errors.New("file_not_found")
	}
	if r.Conversation != "" && r.Conversation != conversation {
		return errors.New("file_conversation_conflict")
	}
	if r.Conversation == conversation {
		return nil
	}
	next := s.copyState()
	r.Conversation = conversation
	next.Files[id] = r
	return s.save(next)
}
func (s *Store) SaveScoped(owner, source, name string, reader io.Reader, conversation string) (Ref, error) {
	r, err := s.Save(owner, source, name, reader)
	if err == nil {
		err = s.BindConversation(owner, r.ID, conversation)
	}
	return r, err
}
func (s *Store) Conversation(owner, id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.state.Files[id]
	if !ok || r.Owner != owner {
		return ""
	}
	return r.Conversation
}
func (s *Store) ListConversation(owner, conversation string) []Ref {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows := []record{}
	for _, r := range s.state.Files {
		if r.Owner == owner && r.Conversation == conversation && s.alive(r) {
			rows = append(rows, r)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Created.After(rows[j].Created) })
	if len(rows) > 10 {
		rows = rows[:10]
	}
	out := []Ref{}
	for _, r := range rows {
		out = append(out, r.Ref)
	}
	return out
}
func (s *Store) GrantConversation(owner, source, conversation string) (string, error) {
	if !validHex(conversation, 8) {
		return "", errors.New("invalid_grant_conversation")
	}
	token, err := s.Grant(owner, source)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id := digest(owner + "\x00" + source)
	g := s.state.Grants[id]
	if g.Conversation != "" && g.Conversation != conversation {
		return "", errors.New("grant_conversation_conflict")
	}
	next := s.copyState()
	g.Conversation = conversation
	next.Grants[id] = g
	return token, s.save(next)
}
func (s *Store) GrantScope(token string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	hash := digest(token)
	for _, g := range s.state.Grants {
		if g.Hash == hash && s.now().Before(g.Expires) {
			return g.Conversation
		}
	}
	return ""
}
