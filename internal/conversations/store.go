package conversations

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

type Session struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Number      int       `json:"number"`
	Created     time.Time `json:"created"`
	TitleMode   string    `json:"title_mode,omitempty"`
	FirstTask   string    `json:"first_task,omitempty"`
	FirstTaskAt time.Time `json:"first_task_at,omitempty"`
	LastTask    string    `json:"last_task,omitempty"`
	LastTaskAt  time.Time `json:"last_task_at,omitempty"`
}
type receipt struct{ ID, Reply string }
type selection struct {
	Version     int                `json:"version"`
	Current     string             `json:"current"`
	Sessions    map[string]Session `json:"sessions"`
	Receipts    []receipt          `json:"receipts"`
	MenuExpires time.Time          `json:"menu_expires"`
}
type Store struct {
	mu    sync.Mutex
	path  string
	state selection
}

func ValidID(id string) bool {
	if len(id) != 8 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil && id == strings.ToLower(id)
}
func ValidThread(id string) bool {
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		return false
	}
	_, err := hex.DecodeString(strings.ReplaceAll(id, "-", ""))
	return err == nil && id == strings.ToLower(id)
}
func writeJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".conversations-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("conversation_path_required")
	}
	s := &Store{path: path, state: selection{Version: 1, Sessions: map[string]Session{}}}
	b, err := os.ReadFile(path)
	if err == nil {
		if len(b) > 1<<20 || json.Unmarshal(b, &s.state) != nil || s.state.Version != 1 || s.state.Sessions == nil || len(s.state.Sessions) > 64 || len(s.state.Receipts) > 128 {
			return nil, errors.New("invalid_conversations")
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	seenNumbers := map[int]bool{}
	for id, session := range s.state.Sessions {
		if session.TitleMode == "" {
			session.TitleMode = "manual"
			if defaultTitle.MatchString(session.Title) {
				session.TitleMode = "auto"
			}
			s.state.Sessions[id] = session
		}
		if !ValidID(id) || id != session.ID || !validTitle(session.Title) || !validPreview(session.FirstTask) || !validPreview(session.LastTask) || (session.TitleMode != "auto" && session.TitleMode != "manual") || session.Number < 1 || session.Number > 64 || seenNumbers[session.Number] {
			return nil, errors.New("invalid_conversation")
		}
		seenNumbers[session.Number] = true
	}
	if len(s.state.Sessions) == 0 {
		session, err := newSession("微信会话", s.state.Sessions)
		if err != nil {
			return nil, err
		}
		session.TitleMode = "auto"
		s.state.Sessions[session.ID] = session
		s.state.Current = session.ID
	}
	if _, ok := s.state.Sessions[s.state.Current]; !ok {
		return nil, errors.New("current_conversation_missing")
	}
	trimReceipts(&s.state)
	if err = writeJSON(path, s.state); err != nil {
		return nil, err
	}
	return s, nil
}
func validTitle(title string) bool {
	if strings.TrimSpace(title) == "" || len(title) > 128 {
		return false
	}
	for _, c := range title {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}
func newSession(title string, existing map[string]Session) (Session, error) {
	if !validTitle(title) {
		return Session{}, errors.New("invalid_conversation_title")
	}
	number := 1
	for _, session := range existing {
		if session.Number >= number {
			number = session.Number + 1
		}
	}
	for n := 0; n < 10; n++ {
		var random [4]byte
		if _, err := rand.Read(random[:]); err != nil {
			return Session{}, err
		}
		id := hex.EncodeToString(random[:])
		if _, ok := existing[id]; !ok {
			return Session{ID: id, Title: title, TitleMode: "manual", Number: number, Created: time.Now().UTC()}, nil
		}
	}
	return Session{}, errors.New("conversation_id_collision")
}
func (s *Store) Current() Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.Sessions[s.state.Current]
}
func (s *Store) Get(id string) (Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.state.Sessions[id]
	return session, ok
}
func (s *Store) Handle(source, input string) (bool, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	input = strings.TrimSpace(input)
	var commandID string
	if source != "" {
		sum := sha256.Sum256([]byte(source))
		commandID = hex.EncodeToString(sum[:12])
		for _, r := range s.state.Receipts {
			if r.ID == commandID {
				return true, r.Reply, nil
			}
		}
	}
	kind := ""
	argument := ""
	switch input {
	case "当前会话", "会话列表", "会话帮助":
		kind = input
	}
	for _, prefix := range []string{"新建会话", "继续会话", "切换会话", "选择会话", "重命名会话", "会话命名"} {
		if strings.HasPrefix(input, prefix) {
			kind = prefix
			argument = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(strings.TrimPrefix(input, prefix)), ":："))
			break
		}
	}
	if kind == "" && time.Now().Before(s.state.MenuExpires) && len(input) <= 2 {
		if number, err := strconv.Atoi(input); err == nil && number >= 0 {
			kind = "继续会话"
			argument = input
		}
	}
	if kind == "" {
		if !s.state.MenuExpires.IsZero() {
			next := s.state
			next.MenuExpires = time.Time{}
			if err := writeJSON(s.path, next); err != nil {
				return false, "", err
			}
			s.state = next
		}
		return false, "", nil
	}
	if source == "" {
		return true, "", errors.New("conversation_command_id_required")
	}
	id := commandID
	next := s.state
	next.Sessions = map[string]Session{}
	for k, v := range s.state.Sessions {
		next.Sessions[k] = v
	}
	reply := ""
	switch kind {
	case "当前会话":
		session := next.Sessions[next.Current]
		reply = fmt.Sprintf("当前会话：%d. %s\n%s\n后续消息会继续这个会话的上下文。", session.Number, session.DisplayName(), session.Activity())
	case "会话列表":
		var sessions []Session
		for _, session := range next.Sessions {
			sessions = append(sessions, session)
		}
		sort.Slice(sessions, func(i, j int) bool { return sessions[i].Number < sessions[j].Number })
		var b strings.Builder
		b.WriteString("会话列表：\n")
		for _, session := range sessions {
			marker := ""
			if session.ID == next.Current {
				marker = "（当前）"
			}
			fmt.Fprintf(&b, "%d. %s%s\n  %s\n", session.Number, session.DisplayName(), marker, session.Activity())
		}
		b.WriteString("直接回复编号即可选中（10 分钟内有效）。也可发送“继续会话 <编号>”。\n新建会话 名称\n重命名会话 <编号> <新名称>")
		reply = b.String()
		next.MenuExpires = time.Now().Add(10 * time.Minute)
	case "会话帮助":
		reply = "新建会话 名称（省略名称会按任务自动取名）\n会话列表（再直接回复编号）\n当前会话\n继续会话 <编号或ID>\n重命名会话 <编号> <名称>\n会话命名 名称（当前会话）\n切换仅影响新任务，已排队任务继续原会话。"
	case "重命名会话", "会话命名":
		session := next.Sessions[next.Current]
		name := argument
		if kind == "重命名会话" {
			parts := strings.Fields(argument)
			if len(parts) < 2 {
				reply = "请发送“重命名会话 <编号> <新名称>”，或“会话命名 <新名称>”修改当前会话。"
				break
			}
			var ok bool
			session, ok = findSession(next.Sessions, parts[0])
			if !ok {
				reply = "未找到这个会话。发送“会话列表”查看编号。"
				break
			}
			name = strings.Join(parts[1:], " ")
		}
		if !validTitle(name) {
			reply = "会话名请控制在 128 字节内，不要包含换行。"
			break
		}
		session.Title = name
		session.TitleMode = "manual"
		next.Sessions[session.ID] = session
		next.MenuExpires = time.Time{}
		reply = fmt.Sprintf("已重命名：%d. %s\n编号和上下文已保留。", session.Number, session.Title)
	case "新建会话":
		if len(next.Sessions) >= 64 {
			reply = "会话数量已达到 64 个，请继续已有会话。"
			break
		}
		automatic := argument == ""
		if automatic {
			argument = "微信会话 " + time.Now().In(time.FixedZone("Asia/Shanghai", 8*3600)).Format("01-02 15:04")
		}
		session, err := newSession(argument, next.Sessions)
		if err != nil {
			reply = "会话名请控制在 128 字节内，不要包含换行。"
			break
		}
		if automatic {
			session.TitleMode = "auto"
		}
		next.Sessions[session.ID] = session
		next.Current = session.ID
		next.MenuExpires = time.Time{}
		reply = fmt.Sprintf("已新建并选中：%d. %s\n会话 ID：%s\n后续消息会保留该会话上下文。", session.Number, session.Title, session.ID)
	default:
		session, ok := findSession(next.Sessions, argument)
		if !ok {
			reply = "未找到这个会话。发送“会话列表”查看编号。"
		} else {
			next.Current = session.ID
			next.MenuExpires = time.Time{}
			reply = fmt.Sprintf("已继续会话：%d. %s\n%s\n新任务将恢复它的原有上下文。", session.Number, session.DisplayName(), session.Activity())
		}
	}
	next.Receipts = append(append([]receipt(nil), s.state.Receipts...), receipt{id, reply})
	trimReceipts(&next)
	if err := writeJSON(s.path, next); err != nil {
		return true, "", err
	}
	s.state = next
	return true, reply, nil
}
