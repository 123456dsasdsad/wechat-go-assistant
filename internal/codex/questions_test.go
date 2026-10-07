package codex

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/123456dsasdsad/wechat-go-assistant/internal/userinput"
)

type blockedQuestions struct {
	called chan struct{}
	answer chan struct{}
}

func (q *blockedQuestions) Wait(ctx context.Context, r userinput.Request) (userinput.Response, error) {
	close(q.called)
	select {
	case <-ctx.Done():
		return userinput.Response{}, ctx.Err()
	case <-q.answer:
		return userinput.Response{Answers: map[string]userinput.Answer{"format": {Answers: []string{"SVG"}}}}, nil
	}
}
func TestNativeQuestionBlocksAndPreservesRPCID(t *testing.T) {
	for _, mode := range []string{"question-string", "question-number"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("CAMPUS_TEST_RPC", mode)
			q := &blockedQuestions{make(chan struct{}), make(chan struct{})}
			c := Config{Binary: os.Args[0], Home: t.TempDir(), Directory: t.TempDir(), Key: "key", Model: "gpt-6-sol", Permissions: ":danger-full-access", Persistent: true, AppServer: true, Questions: q}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				r, e := Run(ctx, c, "ask me")
				if e == nil && r.Text != "human answer received" {
					e = ErrFailed
				}
				done <- e
			}()
			select {
			case <-q.called:
			case <-ctx.Done():
				t.Fatal("question bridge not reached")
			}
			select {
			case e := <-done:
				t.Fatal("did not wait for answer", e)
			case <-time.After(30 * time.Millisecond):
			}
			close(q.answer)
			select {
			case e := <-done:
				if e != nil {
					t.Fatal(e)
				}
			case <-ctx.Done():
				t.Fatal("did not continue")
			}
		})
	}
}
func TestQuestionMCPOverridesApplyWithoutLeakingSecretsInArguments(t *testing.T) {
	c := &userinput.Connection{Executable: "/path with spaces/worker", URL: "http://127.0.0.1:1234", Key: "private-key", JobID: "job", Lease: "private-lease"}
	args := strings.Join(questionArgs(c), "|")
	if !strings.Contains(args, `command="/path with spaces/worker"`) || !strings.Contains(args, "tool_timeout_sec=43200") || strings.Contains(args, c.Key) || strings.Contains(args, c.Lease) {
		t.Fatal("unsafe/incomplete MCP args")
	}
	for _, raw := range []string{`{"threadId":"wrong","turnId":"turn","itemId":"item","isBlocking":true}`, `{"threadId":"thread","turnId":"turn","itemId":"item","isBlocking":false}`} {
		if _, e := nativeQuestionRequest(json.RawMessage(`"request"`), json.RawMessage(raw), "thread", "turn"); e == nil {
			t.Fatal("wrong or nonblocking request accepted")
		}
	}
}
