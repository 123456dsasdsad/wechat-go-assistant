package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/steering"
	"os"
	"strings"
	"testing"
	"time"
)

const fakeThread = "11111111-1111-4111-8111-111111111111"

func fakeAppServer(mode string) {
	scan := bufio.NewScanner(os.Stdin)
	emit := func(v any) { b, _ := json.Marshal(v); fmt.Println(string(b)) }
	thread := fakeThread
	turn := "turn-123"
	for scan.Scan() {
		if strings.HasPrefix(mode, "question-") && strings.Contains(scan.Text(), `"answers"`) {
			var reply struct {
				ID     json.RawMessage `json:"id"`
				Result json.RawMessage `json:"result"`
			}
			json.Unmarshal(scan.Bytes(), &reply)
			want := `"question-rpc"`
			if mode == "question-number" {
				want = "9007199254740993"
			}
			if string(reply.ID) != want || !strings.Contains(string(reply.Result), "SVG") {
				os.Exit(30)
			}
			emit(map[string]any{"method": "item/completed", "params": map[string]any{"threadId": thread, "turnId": turn, "item": map[string]string{"type": "agentMessage", "phase": "final_answer", "text": "human answer received"}}})
			emit(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": thread, "turn": map[string]string{"id": turn, "status": "completed"}}})
			continue
		}
		var r struct {
			ID     int
			Method string
			Params map[string]any
		}
		json.Unmarshal(scan.Bytes(), &r)
		result := map[string]any{}
		switch r.Method {
		case "initialize":
		case "initialized":
			continue
		case "thread/start", "thread/resume":
			if r.Params["sandbox"] != "danger-full-access" || r.Params["approvalPolicy"] != "never" || (r.Method == "thread/resume" && r.Params["threadId"] != fakeThread) {
				os.Exit(18)
			}
			if mode == "mismatch" {
				thread = "22222222-2222-4222-8222-222222222222"
			}
			result["thread"] = map[string]string{"id": thread}
		case "turn/start":
			if r.Params["model"] != "gpt-6-sol" || r.Params["effort"] != "high" || r.Params["sandboxPolicy"].(map[string]any)["type"] != "dangerFullAccess" {
				os.Exit(19)
			}
			result["turn"] = map[string]string{"id": turn}
		case "turn/steer":
			if r.Params["threadId"] != thread || r.Params["expectedTurnId"] != turn {
				os.Exit(20)
			}
			if mode == "disconnect" {
				os.Exit(0)
			}
			if mode == "reject" {
				emit(map[string]any{"id": r.ID, "error": map[string]any{"code": -32000, "message": "no active turn"}})
			} else {
				emit(map[string]any{"id": r.ID, "result": map[string]string{"turnId": turn}})
			}
			emit(map[string]any{"method": "item/completed", "params": map[string]any{"threadId": thread, "turnId": turn, "item": map[string]string{"type": "agentMessage", "phase": "final_answer", "text": "steered answer"}}})
			emit(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": thread, "turn": map[string]string{"id": turn, "status": "completed"}}})
			continue
		}
		emit(map[string]any{"id": r.ID, "result": result})
		if strings.HasPrefix(mode, "question-") && r.Method == "turn/start" {
			var id any = "question-rpc"
			if mode == "question-number" {
				id = json.Number("9007199254740993")
			}
			emit(map[string]any{"id": id, "method": "item/tool/requestUserInput", "params": map[string]any{"threadId": thread, "turnId": turn, "itemId": "question-item", "isBlocking": true, "autoResolutionMs": 1, "questions": []any{map[string]any{"id": "format", "header": "格式", "question": "选择格式", "options": []any{map[string]string{"label": "SVG", "description": "矢量图"}}}}}})
		}
		if mode == "progress" && r.Method == "turn/start" {
			emit(map[string]any{"method": "item/agentMessage/delta", "params": map[string]any{"threadId": thread, "turnId": "another-turn", "itemId": "foreign", "delta": "其他任务内容"}})
			emit(map[string]any{"method": "item/reasoning/textDelta", "params": map[string]any{"threadId": thread, "turnId": turn, "itemId": "private", "delta": "不应显示的推理"}})
			emit(map[string]any{"method": "item/agentMessage/delta", "params": map[string]any{"threadId": thread, "turnId": turn, "itemId": "comment", "delta": "正在检查"}})
			emit(map[string]any{"method": "item/completed", "params": map[string]any{"threadId": thread, "turnId": turn, "item": map[string]string{"id": "comment", "type": "agentMessage", "phase": "commentary", "text": "资料已检查"}}})
			emit(map[string]any{"method": "item/completed", "params": map[string]any{"threadId": thread, "turnId": turn, "item": map[string]string{"id": "answer", "type": "agentMessage", "phase": "final_answer", "text": "最终结论"}}})
			emit(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": thread, "turn": map[string]string{"id": turn, "status": "completed"}}})
		}
	}
}

type fakeSteering struct {
	reserved bool
	acks     []steering.Receipt
}

func (s *fakeSteering) Poll(context.Context) ([]SteerInput, error) {
	return []SteerInput{{ID: "abc", Input: "change answer"}}, nil
}
func (s *fakeSteering) Reserve(context.Context, string, string) (bool, error) {
	if s.reserved {
		return false, nil
	}
	s.reserved = true
	return true, nil
}
func (s *fakeSteering) Ack(_ context.Context, r steering.Receipt) error {
	s.acks = append(s.acks, r)
	return nil
}
func TestAppServerSteersSameTurnAndClassifiesReceipt(t *testing.T) {
	for _, mode := range []string{"accept", "reject", "disconnect"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("CAMPUS_TEST_RPC", mode)
			s := &fakeSteering{}
			c := Config{Binary: os.Args[0], Home: t.TempDir(), Directory: t.TempDir(), Key: "key", Model: "gpt-6-sol", Permissions: ":danger-full-access", Persistent: true, AppServer: true, Steering: s, ThreadID: fakeThread}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			r, e := Run(ctx, c, "initial")
			if mode == "disconnect" {
				if e == nil {
					t.Fatal("broken RPC accepted")
				}
			} else if e != nil || r.Text != "steered answer" || r.ThreadID != fakeThread {
				t.Fatal(r, e)
			}
			want := map[string]string{"accept": "accepted", "reject": "queued", "disconnect": "uncertain"}[mode]
			if len(r.SteerReceipts) != 1 || r.SteerReceipts[0].State != want || len(s.acks) != 1 {
				t.Fatal(r, s.acks)
			}
		})
	}
}
func TestAppServerRejectsNativeThreadMismatch(t *testing.T) {
	t.Setenv("CAMPUS_TEST_RPC", "mismatch")
	c := Config{Binary: os.Args[0], Home: t.TempDir(), Directory: t.TempDir(), Key: "key", Model: "gpt-6-sol", Permissions: ":danger-full-access", Persistent: true, AppServer: true, ThreadID: fakeThread}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, e := Run(ctx, c, "initial"); e == nil {
		t.Fatal("wrong context accepted")
	}
}
