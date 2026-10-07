package conversations

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/files"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/steering"
	"os"
	"reflect"
	"sync"
)

type Turn struct {
	ConversationID string             `json:"conversation_id"`
	JobID          string             `json:"job_id"`
	ThreadID       string             `json:"thread_id"`
	Text           string             `json:"text"`
	Error          string             `json:"error"`
	ToolCount      int                `json:"tool_count"`
	Outputs        []files.Ref        `json:"outputs,omitempty"`
	SteerReceipts  []steering.Receipt `json:"steer_receipts,omitempty"`
}
type threadsState struct {
	Version int               `json:"version"`
	Threads map[string]string `json:"threads"`
	Turns   []Turn            `json:"turns"`
}
type Threads struct {
	mu    sync.Mutex
	path  string
	state threadsState
}

func OpenThreads(path string) (*Threads, error) {
	if path == "" {
		return nil, errors.New("thread_store_path_required")
	}
	s := &Threads{path: path, state: threadsState{Version: 1, Threads: map[string]string{}}}
	b, err := os.ReadFile(path)
	if err == nil {
		if len(b) > 8<<20 || json.Unmarshal(b, &s.state) != nil || s.state.Version != 1 || s.state.Threads == nil || len(s.state.Turns) > 64 {
			return nil, errors.New("invalid_thread_store")
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	for id, thread := range s.state.Threads {
		if !ValidID(id) || !ValidThread(thread) {
			return nil, errors.New("invalid_saved_thread")
		}
	}
	for _, turn := range s.state.Turns {
		if !validTurn(turn) {
			return nil, errors.New("invalid_saved_turn")
		}
	}
	if err = writeJSON(path, s.state); err != nil {
		return nil, err
	}
	return s, nil
}
func validTurn(turn Turn) bool {
	_, err := hex.DecodeString(turn.JobID)
	return ValidID(turn.ConversationID) && len(turn.JobID) == 24 && err == nil && (turn.ThreadID == "" || ValidThread(turn.ThreadID)) && len(turn.Text) <= 64<<10 && len(turn.Error) <= 80 && turn.ToolCount >= 0 && files.ValidResults(turn.Outputs) && (turn.Error != "" || (turn.Text != "" && turn.ThreadID != ""))
}
func (s *Threads) Thread(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.Threads[id]
}
func (s *Threads) Cached(conversationID, jobID string) (Turn, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, turn := range s.state.Turns {
		if turn.ConversationID == conversationID && turn.JobID == jobID {
			return turn, true
		}
	}
	return Turn{}, false
}
func (s *Threads) Save(turn Turn) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validTurn(turn) {
		return errors.New("invalid_turn")
	}
	if existing := s.state.Threads[turn.ConversationID]; existing != "" && turn.ThreadID != "" && existing != turn.ThreadID {
		return errors.New("codex_thread_mismatch")
	}
	next := s.state
	next.Threads = map[string]string{}
	for k, v := range s.state.Threads {
		next.Threads[k] = v
	}
	if turn.ThreadID != "" {
		next.Threads[turn.ConversationID] = turn.ThreadID
	}
	for _, existing := range s.state.Turns {
		if existing.JobID == turn.JobID {
			if reflect.DeepEqual(existing, turn) {
				return nil
			}
			return errors.New("conflicting_turn_cache")
		}
	}
	next.Turns = append(append([]Turn(nil), s.state.Turns...), turn)
	if len(next.Turns) > 64 {
		next.Turns = next.Turns[len(next.Turns)-64:]
	}
	if err := writeJSON(s.path, next); err != nil {
		return err
	}
	s.state = next
	return nil
}
