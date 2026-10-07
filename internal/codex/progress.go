package codex

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"unicode/utf8"
)

type progressMessage struct {
	phase, text string
	complete    bool
}
type progress struct {
	emit      func(string)
	messages  map[string]progressMessage
	order     []string
	truncated bool
}

func newProgress(emit func(string)) *progress {
	return &progress{emit: emit, messages: map[string]progressMessage{}}
}
func tailText(text string, max int) string {
	if len(text) <= max {
		return text
	}
	start := len(text) - max
	for start < len(text) && !utf8.RuneStart(text[start]) {
		start++
	}
	return text[start:]
}
func (p *progress) update(id, phase, text string, complete bool) {
	if p.emit == nil || (phase != "" && phase != "commentary" && phase != "final_answer") {
		return
	}
	if id == "" {
		id = fmt.Sprintf("legacy-%x", sha256.Sum256([]byte(phase+text)))
	}
	m, exists := p.messages[id]
	if !exists {
		if len(p.order) >= 128 {
			delete(p.messages, p.order[0])
			p.order = p.order[1:]
			p.truncated = true
		}
		p.order = append(p.order, id)
	}
	if m.complete && !complete {
		return
	}
	if complete {
		m.text = text
		m.complete = true
	} else {
		m.text += text
	}
	if len(m.text) > 64<<10 {
		p.truncated = true
	}
	m.text = tailText(m.text, 64<<10)
	if phase != "" {
		m.phase = phase
	}
	p.messages[id] = m
	parts := make([]string, 0, len(p.order))
	for _, key := range p.order {
		if s := p.messages[key].text; s != "" {
			parts = append(parts, s)
		}
	}
	result := strings.Join(parts, "\n\n")
	const notice = "[较早过程输出已省略]\n"
	if len(result) > 256<<10-len(notice) || p.truncated {
		result = notice + tailText(result, (256<<10)-len(notice))
	}
	p.emit(result)
}
