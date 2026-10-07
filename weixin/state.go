package weixin

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// State belongs to one bot account and one process. Use a separate state path
// for each account. Relay integrations should use their transactional inbox.
type State struct {
	Version     int               `json:"version"`
	Account     Account           `json:"account"`
	Cursor      string            `json:"cursor"`
	Contexts    map[string]string `json:"contexts"`
	BatchActive bool              `json:"batch_active,omitempty"`
	NextCursor  string            `json:"next_cursor,omitempty"`
	Pending     []Message         `json:"pending,omitempty"`
	Processed   []string          `json:"processed,omitempty"`
}

func NewState(account Account) *State {
	return &State{Version: 1, Account: account, Contexts: map[string]string{}}
}

func LoadState(path string) (*State, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := readBounded(f, 16*1024*1024)
	if err != nil {
		return nil, err
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, errors.New("invalid state JSON")
	}
	if state.Version != 1 || state.Account.BotToken == "" || state.Account.BotID == "" || state.Account.OwnerID == "" || state.Account.BaseURL == "" {
		return nil, errors.New("incomplete or unsupported account state")
	}
	if _, err := New(Options{BaseURL: state.Account.BaseURL, Token: state.Account.BotToken}); err != nil {
		return nil, err
	}
	if state.Contexts == nil {
		state.Contexts = map[string]string{}
	}
	return &state, nil
}

// SaveState uses a same-directory temporary file, fsync and atomic replacement.
// Unix permissions are 0600. On Windows protect the directory with NTFS ACLs.
func SaveState(path string, state *State) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if len(data) > 16*1024*1024 {
		return errors.New("account state exceeds 16 MiB")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".weixin-state-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(data); err != nil {
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
	return os.Rename(tmp, path)
}

type Handler func(context.Context, Message) error
type Commit func(*State) error

// Drain persists a received batch before processing it. A failed handler keeps
// the message pending. Handlers must be idempotent using Message.Key(): a crash
// between external work and commit can replay that work (at-least-once).
func (c *Client) Drain(ctx context.Context, state *State, handler Handler, commit Commit) error {
	if state == nil || handler == nil || commit == nil || state.Account.OwnerID == "" {
		return errors.New("drain requires account state, handler and persistence")
	}
	if state.Account.BotToken != c.opts.Token || state.Account.BaseURL != c.opts.BaseURL {
		return errors.New("client and account state do not match")
	}
	if state.Contexts == nil {
		state.Contexts = map[string]string{}
	}
	if !state.BatchActive {
		updates, err := c.GetUpdates(ctx, state.Cursor)
		if err != nil {
			return err
		}
		state.Pending = updates.Messages
		state.NextCursor = updates.Cursor
		if state.NextCursor == "" {
			state.NextCursor = state.Cursor
		}
		state.BatchActive = true
		if err := commit(state); err != nil {
			return err
		}
	}
	for len(state.Pending) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		msg := state.Pending[0]
		if msg.Type == 1 && msg.GroupID == "" && msg.FromUserID == state.Account.OwnerID {
			key := msg.Key()
			if key == "" {
				return errors.New("inbound message has no stable identifier")
			}
			seen := false
			for _, id := range state.Processed {
				if id == key {
					seen = true
					break
				}
			}
			if !seen {
				if msg.ContextToken == "" {
					return errors.New("inbound message is missing reply context")
				}
				state.Contexts[msg.FromUserID] = msg.ContextToken
				if err := commit(state); err != nil {
					return err
				}
				if err := handler(ctx, msg); err != nil {
					return err
				}
				state.Processed = append(state.Processed, key)
				if len(state.Processed) > 1024 {
					state.Processed = state.Processed[len(state.Processed)-1024:]
				}
			}
		}
		state.Pending = state.Pending[1:]
		if err := commit(state); err != nil {
			return err
		}
	}
	state.Cursor = state.NextCursor
	state.NextCursor = ""
	state.BatchActive = false
	return commit(state)
}
