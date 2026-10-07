// Package userinput implements blocking user decisions shared by Codex and WeChat.
package userinput

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

type Option struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}
type Question struct {
	ID       string   `json:"id"`
	Header   string   `json:"header"`
	Question string   `json:"question"`
	Options  []Option `json:"options,omitempty"`
	IsSecret bool     `json:"isSecret,omitempty"`
}
type Request struct {
	ID        string     `json:"request_id"`
	Questions []Question `json:"questions"`
}
type Answer struct {
	Answers []string `json:"answers"`
}
type Response struct {
	Answers map[string]Answer `json:"answers"`
}

func Validate(r Request) error {
	if r.ID == "" || len(r.ID) > 256 || len(r.Questions) < 1 || len(r.Questions) > 3 {
		return errors.New("invalid_user_questions")
	}
	seen := map[string]bool{}
	for _, q := range r.Questions {
		if q.ID == "" || len(q.ID) > 80 || seen[q.ID] || strings.TrimSpace(q.Question) == "" || len(q.Question) > 4096 || len(q.Header) > 120 || len(q.Options) > 8 || q.IsSecret {
			return errors.New("invalid_user_question")
		}
		seen[q.ID] = true
		labels := map[string]bool{}
		for _, o := range q.Options {
			if strings.TrimSpace(o.Label) == "" || len(o.Label) > 240 || len(o.Description) > 800 || labels[o.Label] {
				return errors.New("invalid_user_option")
			}
			labels[o.Label] = true
		}
	}
	return nil
}
func Normalize(q Question, text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" || len(text) > 8192 {
		return "", errors.New("invalid_user_answer")
	}
	if len(q.Options) > 0 {
		n, e := strconv.Atoi(text)
		if e == nil {
			if n < 1 || n > len(q.Options) {
				return "", errors.New("invalid_option_number")
			}
			return q.Options[n-1].Label, nil
		}
		for _, o := range q.Options {
			if text == o.Label {
				return text, nil
			}
		}
	}
	return text, nil
}

// Schema has no defaults: every supplied answer must come from the human.
func Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"questions":{"type":"array","minItems":1,"maxItems":3,"items":{"type":"object","properties":{"id":{"type":"string"},"header":{"type":"string"},"question":{"type":"string"},"options":{"type":"array","maxItems":8,"items":{"type":"object","properties":{"label":{"type":"string"},"description":{"type":"string"}},"required":["label","description"],"additionalProperties":false}}},"required":["id","header","question"],"additionalProperties":false}}},"required":["questions"],"additionalProperties":false}`)
}
