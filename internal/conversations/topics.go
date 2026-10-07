package conversations

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

var defaultTitle = regexp.MustCompile(`^微信会话(?: \d{2}-\d{2} \d{2}:\d{2})?$`)
var credentialText = regexp.MustCompile(`(?i)(api[ _-]?key|access[ _-]?token|refresh[ _-]?token|bearer\s|password|secret|密码|私钥|凭据)`)
var taskLinks = regexp.MustCompile(`https?://\S+`)
var phoneZone = time.FixedZone("Asia/Shanghai", 8*3600)

func findSession(sessions map[string]Session, argument string) (Session, bool) {
	if session, ok := sessions[argument]; ok {
		return session, true
	}
	number, err := strconv.Atoi(argument)
	if err != nil {
		return Session{}, false
	}
	for _, session := range sessions {
		if session.Number == number {
			return session, true
		}
	}
	return Session{}, false
}
func validPreview(text string) bool {
	if len(text) > 256 {
		return false
	}
	for _, r := range text {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func brief(text string, limit int) string {
	runes := []rune(text)
	if len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return text
}
func taskPreview(input string, names []string) string {
	if credentialText.MatchString(input) {
		return "凭据相关任务（内容已省略）"
	}
	text := taskLinks.ReplaceAllString(input, "[链接]")
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, text)
	text = strings.Join(strings.Fields(text), " ")
	for _, prefix := range []string{"请帮我", "帮我", "请", "我想"} {
		if strings.HasPrefix(text, prefix) {
			text = strings.TrimSpace(strings.TrimPrefix(text, prefix))
			break
		}
	}
	if len(names) > 0 {
		name := names[0]
		if credentialText.MatchString(name) {
			name = "凭据文件"
		}
		name = brief(strings.Join(strings.Fields(name), " "), 24)
		text = name + "：" + text
	}
	return brief(text, 44)
}

// QuestionPreview is bounded display text, never the raw task or its credentials.
func QuestionPreview(input string) string { return taskPreview(input, nil) }
func substantive(text string) bool {
	switch strings.Trim(text, " 。！!？?") {
	case "", "你好", "您好", "好的", "谢谢", "测试", "在吗", "hi", "hello":
		return false
	}
	return true
}
func (session Session) DisplayName() string {
	if session.TitleMode == "manual" && session.FirstTask != "" {
		topic := brief(session.FirstTask, 18)
		if topic != session.Title {
			return session.Title + " · " + topic
		}
	}
	return session.Title
}
func (session Session) Activity() string {
	if session.LastTask == "" {
		return "尚无任务 · 创建 " + session.Created.In(phoneZone).Format("01-02 15:04")
	}
	return fmt.Sprintf("最近：%s · %s", session.LastTask, session.LastTaskAt.In(phoneZone).Format("01-02 15:04"))
}

// RecordTask changes display metadata only. It never selects a session or changes
// the underlying ID, native thread, model snapshot or queued task.
func (s *Store) RecordTask(id, input string, names []string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.state.Sessions[id]
	if !ok {
		return nil
	} // Legacy ephemeral jobs have no selectable session.
	if at.IsZero() {
		at = time.Now()
	}
	at = at.UTC()
	preview := taskPreview(input, names)
	previous := session
	if substantive(preview) && (session.FirstTask == "" || at.Before(session.FirstTaskAt)) {
		session.FirstTask = preview
		session.FirstTaskAt = at
		if session.TitleMode == "auto" {
			session.Title = brief(preview, 18)
		}
	}
	if session.LastTask == "" || !at.Before(session.LastTaskAt) {
		session.LastTask = preview
		session.LastTaskAt = at
	}
	if session == previous {
		return nil
	}
	next := s.state
	next.Sessions = map[string]Session{}
	for k, v := range s.state.Sessions {
		next.Sessions[k] = v
	}
	next.Sessions[id] = session
	if err := writeJSON(s.path, next); err != nil {
		return err
	}
	s.state = next
	return nil
}
func trimReceipts(state *selection) {
	total := 0
	for _, r := range state.Receipts {
		total += len(r.Reply)
	}
	for len(state.Receipts) > 128 || (total > 256<<10 && len(state.Receipts) > 1) {
		total -= len(state.Receipts[0].Reply)
		state.Receipts = state.Receipts[1:]
	}
}
