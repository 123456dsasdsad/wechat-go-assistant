package userinput

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"sync"
)

const Instructions = "When you need the user's choice, missing information, or approval before proceeding, call mcp__wechat_questions__ask_user. This tool sends each question to WeChat and blocks until the user quotes that question and answers. Do not ask only in commentary/final text and then assume a default, fabricate a reply, or finish dependent work. Ordinary WeChat messages remain queued. Use short clear questions, numbered options where helpful; do not ask for passwords, API keys or private keys."

// ServeMCP is a task-scoped stdio server; no public listener or extra runtime.
func ServeMCP(ctx context.Context, in io.Reader, out io.Writer, wait func(context.Context, Request) (Response, error)) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var mu sync.Mutex
	write := func(id json.RawMessage, result any, rpcError any) {
		mu.Lock()
		defer mu.Unlock()
		v := map[string]any{"jsonrpc": "2.0", "id": id}
		if rpcError != nil {
			v["error"] = rpcError
		} else {
			v["result"] = result
		}
		_ = json.NewEncoder(out).Encode(v)
	}
	lines := make(chan []byte)
	go func() {
		defer close(lines)
		s := bufio.NewScanner(in)
		s.Buffer(make([]byte, 4096), 64<<10)
		for s.Scan() {
			b := append([]byte(nil), s.Bytes()...)
			select {
			case lines <- b:
			case <-ctx.Done():
				return
			}
		}
	}()
	var callsMu sync.Mutex
	calls := map[string]context.CancelFunc{}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case b, ok := <-lines:
			if !ok {
				return nil
			}
			var r struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if json.Unmarshal(b, &r) != nil {
				continue
			}
			if r.Method == "notifications/cancelled" {
				var p struct {
					RequestID json.RawMessage `json:"requestId"`
				}
				_ = json.Unmarshal(r.Params, &p)
				callsMu.Lock()
				stop := calls[string(p.RequestID)]
				callsMu.Unlock()
				if stop != nil {
					stop()
				}
				continue
			}
			if len(r.ID) == 0 {
				continue
			}
			switch r.Method {
			case "initialize":
				var p struct {
					ProtocolVersion string `json:"protocolVersion"`
				}
				_ = json.Unmarshal(r.Params, &p)
				write(r.ID, map[string]any{"protocolVersion": p.ProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "wechat_questions", "version": "1.0.0"}}, nil)
			case "tools/list":
				write(r.ID, map[string]any{"tools": []any{map[string]any{"name": "ask_user", "description": Instructions, "inputSchema": Schema()}}}, nil)
			case "ping":
				write(r.ID, map[string]any{}, nil)
			case "tools/call":
				var p struct {
					Name      string `json:"name"`
					Arguments struct {
						Questions []Question `json:"questions"`
					} `json:"arguments"`
				}
				if json.Unmarshal(r.Params, &p) != nil || p.Name != "ask_user" {
					write(r.ID, nil, map[string]any{"code": -32602, "message": "unknown question tool"})
					continue
				}
				callCtx, stop := context.WithCancel(ctx)
				key := string(r.ID)
				callsMu.Lock()
				_, exists := calls[key]
				if !exists {
					calls[key] = stop
				}
				callsMu.Unlock()
				if exists {
					stop()
					write(r.ID, nil, map[string]any{"code": -32600, "message": "duplicate active call"})
					continue
				}
				go func(id json.RawMessage, key string, questions []Question) {
					defer stop()
					defer func() { callsMu.Lock(); delete(calls, key); callsMu.Unlock() }()
					response, e := wait(callCtx, Request{ID: "mcp:" + key, Questions: questions})
					text := ""
					failed := e != nil
					if failed {
						text = "Question could not be answered: " + e.Error() + ". Stop dependent work; never assume an answer."
					} else {
						b, _ := json.Marshal(response)
						text = string(b)
					}
					write(id, map[string]any{"content": []any{map[string]string{"type": "text", "text": text}}, "isError": failed}, nil)
				}(r.ID, key, p.Arguments.Questions)
			default:
				write(r.ID, nil, map[string]any{"code": -32601, "message": "method not found"})
			}
		}
	}
}
