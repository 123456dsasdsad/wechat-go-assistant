package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/conversations"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/steering"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/usage"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/userinput"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type SteerInput struct{ ID, Input string }
type Steering interface {
	Poll(context.Context) ([]SteerInput, error)
	Reserve(context.Context, string, string) (bool, error)
	Ack(context.Context, steering.Receipt) error
}
type rpcMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  json.RawMessage `json:"error,omitempty"`
}

func runAppServer(ctx context.Context, c Config, prompt string) (result Result, retErr error) {
	args := append([]string{"app-server"}, questionArgs(c.QuestionMCP)...)
	cmd := exec.Command(c.Binary, args...)
	cmd.Dir = c.Directory
	cmd.Stderr = io.Discard
	for _, v := range os.Environ() {
		n := strings.SplitN(v, "=", 2)[0]
		if n != "CODEX_HOME" && n != "COCKPIT_API_KEY" && n != "OPENAI_API_KEY" && n != "CODEX_API_KEY" {
			cmd.Env = append(cmd.Env, v)
		}
	}
	cmd.Env = append(cmd.Env, "CODEX_HOME="+c.Home, "COCKPIT_API_KEY="+c.Key)
	cmd.Env = append(cmd.Env, questionEnv(c.QuestionMCP)...)
	configureProcess(cmd)
	input, e := cmd.StdinPipe()
	if e != nil {
		return result, errors.New("codex_pipe_failure")
	}
	output, e := cmd.StdoutPipe()
	if e != nil {
		return result, errors.New("codex_pipe_failure")
	}
	if e = cmd.Start(); e != nil {
		return result, errors.New("codex_start_failure")
	}
	stop := context.AfterFunc(ctx, func() { terminateProcess(cmd) })
	defer stop()
	readerCtx, stopReader := context.WithCancel(ctx)
	defer func() { stopReader(); input.Close(); terminateProcess(cmd); cmd.Wait() }()
	messages := make(chan rpcMessage, 128)
	go func() {
		defer close(messages)
		s := bufio.NewScanner(output)
		s.Buffer(make([]byte, 65536), 8<<20)
		for s.Scan() {
			var m rpcMessage
			if json.Unmarshal(s.Bytes(), &m) != nil {
				return
			}
			select {
			case messages <- m:
			case <-readerCtx.Done():
				return
			}
		}
	}()
	nextID := 0
	send := func(method string, params any) (int, error) {
		nextID++
		b, e := json.Marshal(map[string]any{"id": nextID, "method": method, "params": params})
		if e != nil {
			return 0, e
		}
		_, e = input.Write(append(b, '\n'))
		return nextID, e
	}
	// Startup is sequential; unexpected server requests cannot silently prompt or hang.
	call := func(method string, params any) (json.RawMessage, error) {
		id, e := send(method, params)
		if e != nil {
			return nil, errors.New("codex_rpc_write_failed")
		}
		for {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case m, ok := <-messages:
				if !ok {
					return nil, errors.New("codex_rpc_eof")
				}
				if m.Method != "" && len(m.ID) != 0 {
					return nil, errors.New("codex_unexpected_request")
				}
				if string(m.ID) == strconv.Itoa(id) {
					if len(m.Error) > 0 {
						return nil, errors.New("codex_rpc_rejected")
					}
					return m.Result, nil
				}
			}
		}
	}
	if _, e = call("initialize", map[string]any{"clientInfo": map[string]string{"name": "campus_wechat_go", "version": "1.0"}, "capabilities": map[string]any{"experimentalApi": true}}); e != nil {
		return result, e
	}
	if _, e = input.Write([]byte("{\"method\":\"initialized\",\"params\":{}}\n")); e != nil {
		return result, e
	}
	sandbox := "read-only"
	policy := "readOnly"
	if c.Permissions == ":danger-full-access" {
		sandbox = "danger-full-access"
		policy = "dangerFullAccess"
	}
	params := map[string]any{"model": c.Model, "cwd": c.Directory, "approvalPolicy": "never", "sandbox": sandbox}
	if c.QuestionMCP != nil {
		params["developerInstructions"] = userinput.Instructions
	}
	method := "thread/start"
	if c.ThreadID != "" {
		method = "thread/resume"
		params["threadId"] = c.ThreadID
		params["excludeTurns"] = true
	} else {
		params["ephemeral"] = !c.Persistent
	}
	raw, e := call(method, params)
	if e != nil {
		return result, e
	}
	var thread struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if json.Unmarshal(raw, &thread) != nil || !conversations.ValidThread(thread.Thread.ID) || (c.ThreadID != "" && c.ThreadID != thread.Thread.ID) {
		return result, errors.New("codex_thread_mismatch")
	}
	if c.Persistent {
		result.ThreadID = thread.Thread.ID
	}
	raw, e = call("turn/start", map[string]any{"threadId": thread.Thread.ID, "input": []any{map[string]string{"type": "text", "text": prompt}}, "model": c.Model, "effort": c.Effort, "approvalPolicy": "never", "sandboxPolicy": map[string]string{"type": policy}})
	if e != nil {
		return result, e
	}
	var start struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if json.Unmarshal(raw, &start) != nil || start.Turn.ID == "" {
		return result, errors.New("codex_invalid_turn")
	}
	baseline := c.UsageBaseline
	if c.ThreadID == "" {
		baseline = usage.Tokens{Available: true}
	}
	pending := map[int]steering.Receipt{}
	progress := newProgress(c.Progress)
	seen := map[string]bool{}
	acknowledge := func(r steering.Receipt) {
		result.SteerReceipts = append(result.SteerReceipts, r)
		if c.Steering != nil {
			ackCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
			defer cancel()
			_ = c.Steering.Ack(ackCtx, r)
		}
	}
	defer func() {
		for _, r := range pending {
			r.State = "uncertain"
			acknowledge(r)
		}
	}()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case <-tick.C:
			if c.Steering == nil {
				continue
			}
			pollCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			items, err := c.Steering.Poll(pollCtx)
			cancel()
			if err != nil {
				continue
			}
			for _, v := range items {
				if seen[v.ID] {
					continue
				}
				reserveCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				reserved, err := c.Steering.Reserve(reserveCtx, v.ID, start.Turn.ID)
				cancel()
				if err != nil || !reserved {
					continue
				}
				seen[v.ID] = true
				text := "CURRENT_SUPPLEMENT（对当前正在执行任务的追加用户要求，和原问题同属本轮）：\n" + v.Input
				id, err := send("turn/steer", map[string]any{"threadId": thread.Thread.ID, "expectedTurnId": start.Turn.ID, "input": []any{map[string]string{"type": "text", "text": text}}})
				r := steering.Receipt{ID: v.ID, TurnID: start.Turn.ID, State: "uncertain"}
				if err != nil {
					acknowledge(r)
					return result, errors.New("codex_rpc_write_failed")
				}
				pending[id] = r
			}
		case m, ok := <-messages:
			if !ok {
				return result, errors.New("codex_rpc_eof")
			}
			if len(m.ID) != 0 && m.Method != "" {
				if m.Method != "item/tool/requestUserInput" || c.Questions == nil {
					return result, errors.New("codex_unexpected_request")
				}
				request, err := nativeQuestionRequest(m.ID, m.Params, thread.Thread.ID, start.Turn.ID)
				if err != nil {
					b, _ := json.Marshal(map[string]any{"id": m.ID, "error": map[string]any{"code": -32602, "message": err.Error()}})
					if _, err = input.Write(append(b, '\n')); err != nil {
						return result, errors.New("codex_rpc_write_failed")
					}
					continue
				}
				response, err := c.Questions.Wait(ctx, request)
				if err != nil {
					return result, err
				}
				b, _ := json.Marshal(map[string]any{"id": m.ID, "result": response})
				if _, err = input.Write(append(b, '\n')); err != nil {
					return result, errors.New("codex_rpc_write_failed")
				}
				continue
			}
			if r, ok := pendingID(m.ID, pending); ok {
				clientID, _ := strconv.Atoi(string(m.ID))
				delete(pending, clientID)
				if len(m.Error) > 0 {
					r.State = "queued"
				} else {
					var ack struct {
						TurnID string `json:"turnId"`
					}
					if json.Unmarshal(m.Result, &ack) == nil && ack.TurnID == start.Turn.ID {
						r.State = "accepted"
					}
				}
				acknowledge(r)
				continue
			}
			if m.Method == "thread/tokenUsage/updated" {
				var v tokenUsageEvent
				if json.Unmarshal(m.Params, &v) == nil && v.ThreadID == thread.Thread.ID && v.TurnID == start.Turn.ID {
					result.Cumulative = v.TokenUsage.Total.tokens()
					result.Usage = usage.Difference(result.Cumulative, baseline)
				}
				continue
			}
			var event struct {
				ThreadID string                                 `json:"threadId"`
				TurnID   string                                 `json:"turnId"`
				ItemID   string                                 `json:"itemId"`
				Delta    string                                 `json:"delta"`
				Turn     struct{ ID, Status string }            `json:"turn"`
				Item     struct{ ID, Type, Text, Phase string } `json:"item"`
			}
			if m.Method == "item/agentMessage/delta" {
				if json.Unmarshal(m.Params, &event) == nil && event.ThreadID == thread.Thread.ID && event.TurnID == start.Turn.ID && event.ItemID != "" {
					progress.update(event.ItemID, "", event.Delta, false)
				}
				continue
			}
			if m.Method == "item/completed" || m.Method == "turn/completed" {
				if json.Unmarshal(m.Params, &event) != nil || event.ThreadID != thread.Thread.ID {
					return result, errors.New("codex_event_mismatch")
				}
				if m.Method == "item/completed" && event.TurnID == start.Turn.ID {
					if event.Item.Type == "agentMessage" {
						progress.update(event.Item.ID, event.Item.Phase, event.Item.Text, true)
					}
					if event.Item.Type == "agentMessage" && (event.Item.Phase == "final_answer" || event.Item.Phase == "") {
						result.Text = event.Item.Text
					}
					if event.Item.Type == "commandExecution" {
						result.ToolCount++
					}
				}
				if m.Method == "turn/completed" && event.Turn.ID == start.Turn.ID {
					if event.Turn.Status != "completed" || strings.TrimSpace(result.Text) == "" {
						result.Text = ""
						return result, ErrFailed
					}
					if len(result.Text) > 64<<10 {
						result.Text = ""
						return result, errors.New("codex_answer_limit")
					}
					return result, nil
				}
			}
		}
	}
}

func pendingID(id json.RawMessage, pending map[int]steering.Receipt) (steering.Receipt, bool) {
	n, e := strconv.Atoi(string(id))
	if e != nil {
		return steering.Receipt{}, false
	}
	r, ok := pending[n]
	return r, ok
}
