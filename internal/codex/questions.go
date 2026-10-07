package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/123456dsasdsad/wechat-go-assistant/internal/userinput"
)

type Questions interface {
	Wait(context.Context, userinput.Request) (userinput.Response, error)
}

func questionArgs(c *userinput.Connection) []string {
	if c == nil {
		return nil
	}
	return []string{"-c", "mcp_servers.wechat_questions.command=" + strconv.Quote(c.Executable), "-c", `mcp_servers.wechat_questions.args=["--question-mcp"]`, "-c", `mcp_servers.wechat_questions.env_vars=["WECHAT_QUESTION_URL","WECHAT_QUESTION_KEY","WECHAT_QUESTION_JOB","WECHAT_QUESTION_LEASE"]`, "-c", "mcp_servers.wechat_questions.tool_timeout_sec=43200", "-c", "mcp_servers.wechat_questions.required=true"}
}
func questionEnv(c *userinput.Connection) []string {
	if c == nil {
		return nil
	}
	return []string{"WECHAT_QUESTION_URL=" + c.URL, "WECHAT_QUESTION_KEY=" + c.Key, "WECHAT_QUESTION_JOB=" + c.JobID, "WECHAT_QUESTION_LEASE=" + c.Lease}
}

type nativeQuestion struct {
	ThreadID   string               `json:"threadId"`
	TurnID     string               `json:"turnId"`
	ItemID     string               `json:"itemId"`
	IsBlocking bool                 `json:"isBlocking"`
	Questions  []userinput.Question `json:"questions"`
}

func nativeQuestionRequest(id any, raw json.RawMessage, thread, turn string) (userinput.Request, error) {
	var p nativeQuestion
	if json.Unmarshal(raw, &p) != nil || p.ThreadID != thread || p.TurnID != turn || p.ItemID == "" {
		return userinput.Request{}, fmt.Errorf("codex_question_mismatch")
	}
	if !p.IsBlocking {
		return userinput.Request{}, fmt.Errorf("Use mcp__wechat_questions__ask_user for a blocking question; do not continue without the human answer")
	}
	b, _ := json.Marshal(id)
	r := userinput.Request{ID: "native:" + string(b) + ":" + p.ItemID, Questions: p.Questions}
	return r, userinput.Validate(r)
}
