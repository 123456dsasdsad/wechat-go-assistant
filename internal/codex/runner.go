package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/conversations"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/models"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/steering"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/usage"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/userinput"
	"io"
	"os"
	"os/exec"
	"strings"
)

type Config struct {
	UsageBaseline                                         usage.Tokens
	Binary, Home, Directory, Key, Model, Effort, ThreadID string
	Persistent                                            bool
	Permissions                                           string
	AppServer                                             bool
	Steering                                              Steering
	Questions                                             Questions
	QuestionMCP                                           *userinput.Connection
	Progress                                              func(string)
}
type Result struct {
	Usage         usage.Tokens       `json:"usage"`
	Cumulative    usage.Tokens       `json:"cumulative"`
	Text          string             `json:"text"`
	ToolCount     int                `json:"tool_count"`
	ThreadID      string             `json:"thread_id,omitempty"`
	SteerReceipts []steering.Receipt `json:"steer_receipts,omitempty"`
}

var ErrFailed = errors.New("codex_turn_failed")

func Run(ctx context.Context, c Config, prompt string) (Result, error) {
	var result Result
	if c.Effort == "" {
		c.Effort = "high"
	}
	if c.Binary == "" || c.Directory == "" || c.Home == "" || !models.ValidID(c.Model) || !models.ValidEffort(c.Effort) || c.Key == "" || strings.TrimSpace(prompt) == "" {
		return result, errors.New("invalid_codex_configuration")
	}
	if c.ThreadID != "" && (!c.Persistent || !conversations.ValidThread(c.ThreadID)) {
		return result, errors.New("invalid_codex_thread")
	}
	if c.Permissions != "" && c.Permissions != ":danger-full-access" {
		return result, errors.New("invalid_codex_permissions")
	}
	if c.AppServer {
		return runAppServer(ctx, c, prompt)
	}
	args := []string{"exec", "--json", "--skip-git-repo-check", "-C", c.Directory, "--model", c.Model, "-c", fmt.Sprintf("model_reasoning_effort=%q", c.Effort)}
	if !c.Persistent {
		args = append(args, "--ephemeral")
	}
	if c.ThreadID != "" {
		args = []string{"-C", c.Directory, "exec", "resume", "--json", "--skip-git-repo-check", "--model", c.Model, "-c", fmt.Sprintf("model_reasoning_effort=%q", c.Effort), c.ThreadID}
	}
	if c.Permissions != "" {
		args = append(args, "-c", fmt.Sprintf("default_permissions=%q", c.Permissions), "-c", "approval_policy=\"never\"")
	}
	args = append(args, questionArgs(c.QuestionMCP)...)
	args = append(args, "-")
	cmd := exec.Command(c.Binary, args...)
	cmd.Env = []string{}
	for _, v := range os.Environ() {
		name := strings.SplitN(v, "=", 2)[0]
		if name != "CODEX_HOME" && name != "COCKPIT_API_KEY" && name != "OPENAI_API_KEY" && name != "CODEX_API_KEY" {
			cmd.Env = append(cmd.Env, v)
		}
	}
	cmd.Env = append(cmd.Env, "CODEX_HOME="+c.Home, "COCKPIT_API_KEY="+c.Key)
	cmd.Env = append(cmd.Env, questionEnv(c.QuestionMCP)...)
	if c.QuestionMCP != nil {
		prompt = userinput.Instructions + "\n" + prompt
	}
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Stderr = io.Discard
	configureProcess(cmd)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return result, errors.New("codex_pipe_failure")
	}
	if err = cmd.Start(); err != nil {
		return result, errors.New("codex_start_failure")
	}
	stop := context.AfterFunc(ctx, func() { terminateProcess(cmd) })
	defer stop()
	scanner := bufio.NewScanner(out)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	complete, failed := false, false
	progress := newProgress(c.Progress)
	total := 0
	for scanner.Scan() {
		raw := scanner.Bytes()
		total += len(raw)
		if total > 8*1024*1024 {
			terminateProcess(cmd)
			cmd.Wait()
			return result, errors.New("codex_output_limit")
		}
		var e struct {
			Type     string `json:"type"`
			ThreadID string `json:"thread_id"`
			Item     struct {
				ID     string `json:"id"`
				Phase  string `json:"phase"`
				Type   string `json:"type"`
				Text   string `json:"text"`
				Status string `json:"status"`
			} `json:"item"`
		}
		if json.Unmarshal(raw, &e) != nil {
			terminateProcess(cmd)
			cmd.Wait()
			return result, errors.New("codex_invalid_jsonl")
		}
		switch e.Type {
		case "thread.started":
			if c.Persistent {
				if !conversations.ValidThread(e.ThreadID) || (c.ThreadID != "" && e.ThreadID != c.ThreadID) {
					terminateProcess(cmd)
					cmd.Wait()
					return Result{}, errors.New("codex_thread_mismatch")
				}
				result.ThreadID = e.ThreadID
			}
		case "turn.completed":
			complete = true
			var v struct {
				Usage *struct {
					Input  int64 `json:"input_tokens"`
					Cached int64 `json:"cached_input_tokens"`
					Output int64 `json:"output_tokens"`
				} `json:"usage"`
			}
			if json.Unmarshal(raw, &v) == nil && v.Usage != nil {
				result.Usage = usage.Tokens{Available: true, Input: v.Usage.Input, Cached: v.Usage.Cached, Output: v.Usage.Output, Total: v.Usage.Input + v.Usage.Output}
			}
		case "turn.failed", "error":
			failed = true
		}
		if e.Type == "item.completed" && e.Item.Type == "agent_message" {
			progress.update(e.Item.ID, e.Item.Phase, e.Item.Text, true)
			if e.Item.Phase == "" || e.Item.Phase == "final_answer" {
				result.Text = e.Item.Text
			}
		}
		if e.Type == "item.completed" && e.Item.Type == "command_execution" {
			result.ToolCount++
		}
	}
	scanErr := scanner.Err()
	if scanErr != nil {
		terminateProcess(cmd)
	}
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if scanErr != nil {
		return result, errors.New("codex_output_limit")
	}
	if waitErr != nil || failed || !complete || strings.TrimSpace(result.Text) == "" {
		return Result{ThreadID: result.ThreadID}, ErrFailed
	}
	if c.Persistent && result.ThreadID == "" {
		return Result{}, errors.New("codex_session_not_persisted")
	}
	if len(result.Text) > 64*1024 {
		return Result{ThreadID: result.ThreadID}, errors.New("codex_answer_limit")
	}
	return result, nil
}
