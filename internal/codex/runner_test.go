package codex

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if mode := os.Getenv("CAMPUS_TEST_RPC"); mode != "" {
		fakeAppServer(mode)
		os.Exit(0)
	}
	if mode := os.Getenv("CAMPUS_TEST_CLI"); mode != "" {
		io.Copy(io.Discard, os.Stdin)
		switch mode {
		case "full":
			args := strings.Join(os.Args[1:], "|")
			if !strings.Contains(args, "-c|default_permissions=\":danger-full-access\"|-c|approval_policy=\"never\"") {
				os.Exit(12)
			}
			fmt.Println(`{"type":"thread.started","thread_id":"11111111-1111-4111-8111-111111111111"}`)
			fmt.Println(`{"type":"item.completed","item":{"type":"agent_message","text":"execution enabled"}}`)
			fmt.Println(`{"type":"turn.completed"}`)
		case "too-long":
			fmt.Println(`{"type":"thread.started","thread_id":"11111111-1111-4111-8111-111111111111"}`)
			fmt.Printf("{\"type\":\"item.completed\",\"item\":{\"type\":\"agent_message\",\"text\":%q}}\n", strings.Repeat("x", 65537))
			fmt.Println(`{"type":"turn.completed"}`)
		case "persistent", "resume":
			args := strings.Join(os.Args[1:], "|")
			if strings.Contains(args, "--ephemeral") || strings.Contains(args, "--last") {
				os.Exit(8)
			}
			if mode == "resume" && (!strings.Contains(args, "exec|resume|--json") || !strings.Contains(args, "11111111-1111-4111-8111-111111111111|-")) {
				os.Exit(9)
			}
			fmt.Println(`{"type":"thread.started","thread_id":"11111111-1111-4111-8111-111111111111"}`)
			fmt.Println(`{"type":"item.completed","item":{"type":"agent_message","text":"context result"}}`)
			fmt.Println(`{"type":"turn.completed"}`)
		case "wrong-thread":
			fmt.Println(`{"type":"thread.started","thread_id":"22222222-2222-4222-8222-222222222222"}`)
		case "selection":
			args := strings.Join(os.Args[1:], "|")
			if !strings.Contains(args, "--model|gpt-6-luna|-c|model_reasoning_effort=\"low\"|-") {
				os.Exit(7)
			}
			fmt.Println(`{"type":"item.completed","item":{"type":"agent_message","text":"selection passed"}}`)
			fmt.Println(`{"type":"turn.completed"}`)
		case "good":
			fmt.Println(`{"type":"item.completed","item":{"type":"command_execution","status":"completed"}}`)
			fmt.Println(`{"type":"item.completed","item":{"type":"agent_message","text":"fixture result"}}`)
			fmt.Println(`{"type":"turn.completed"}`)
		case "failed":
			fmt.Println(`{"type":"item.completed","item":{"type":"agent_message","text":"partial answer"}}`)
			fmt.Println(`{"type":"turn.failed"}`)
		case "incomplete":
			fmt.Println(`{"type":"item.completed","item":{"type":"agent_message","text":"partial answer"}}`)
		case "malformed":
			fmt.Println(`not json`)
		case "timeout":
			time.Sleep(time.Minute)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestFullPermissionsOverrideForNewAndResumedTurns(t *testing.T) {
	c := testConfig(t, "full")
	c.Persistent = true
	c.Permissions = ":danger-full-access"
	r, e := Run(context.Background(), c, "execute")
	if e != nil {
		t.Fatal(e)
	}
	c.ThreadID = r.ThreadID
	if _, e = Run(context.Background(), c, "continue"); e != nil {
		t.Fatal(e)
	}
	c.Permissions = "malicious\" value"
	if _, e = Run(context.Background(), c, "invalid"); e == nil {
		t.Fatal("invalid profile accepted")
	}
}

func TestOversizedAnswerKeepsNativeIdentity(t *testing.T) {
	c := testConfig(t, "too-long")
	c.Persistent = true
	r, err := Run(context.Background(), c, "first")
	if err == nil || err.Error() != "codex_answer_limit" || r.Text != "" || r.ThreadID != "11111111-1111-4111-8111-111111111111" {
		t.Fatal("oversized answer lost its persisted context", r, err)
	}
}

func TestPersistentAndExplicitResumeArguments(t *testing.T) {
	c := testConfig(t, "persistent")
	c.Persistent = true
	r, err := Run(context.Background(), c, "first")
	if err != nil || r.ThreadID != "11111111-1111-4111-8111-111111111111" {
		t.Fatal(r, err)
	}
	t.Setenv("CAMPUS_TEST_CLI", "resume")
	c.ThreadID = r.ThreadID
	if _, err = Run(context.Background(), c, "second"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CAMPUS_TEST_CLI", "wrong-thread")
	if _, err = Run(context.Background(), c, "third"); err == nil {
		t.Fatal("different native session accepted")
	}
	c.ThreadID = "--last"
	if _, err = Run(context.Background(), c, "fourth"); err == nil {
		t.Fatal("resume flag injection accepted")
	}
}
func TestRunnerPassesModelAndEffortAsArguments(t *testing.T) {
	c := testConfig(t, "selection")
	c.Model = "gpt-6-luna"
	c.Effort = "low"
	r, e := Run(context.Background(), c, "prompt")
	if e != nil || r.Text != "selection passed" {
		t.Fatal(r, e)
	}
	c.Model = "--model=other"
	if _, e = Run(context.Background(), c, "prompt"); e == nil {
		t.Fatal("model flag accepted")
	}
	c.Model = "gpt-6-luna"
	c.Effort = "high\"; write_config=true"
	if _, e = Run(context.Background(), c, "prompt"); e == nil {
		t.Fatal("configuration injection accepted")
	}
}
func testConfig(t *testing.T, mode string) Config {
	t.Setenv("CAMPUS_TEST_CLI", mode)
	return Config{Binary: os.Args[0], Home: t.TempDir(), Directory: t.TempDir(), Key: "fixture-key", Model: "gpt-6-sol"}
}
func TestRunnerRequiresCompleteTurn(t *testing.T) {
	r, e := Run(context.Background(), testConfig(t, "good"), "prompt")
	if e != nil || r.Text != "fixture result" || r.ToolCount != 1 {
		t.Fatalf("%+v %v", r, e)
	}
}
func TestRunnerRejectsPartialAndFailed(t *testing.T) {
	for _, mode := range []string{"failed", "incomplete", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			if _, e := Run(context.Background(), testConfig(t, mode), "prompt"); e == nil {
				t.Fatal("partial result accepted")
			}
		})
	}
}
func TestRunnerTimeout(t *testing.T) {
	c := testConfig(t, "timeout")
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := Run(ctx, c, "prompt")
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 4*time.Second {
		t.Fatalf("timeout not enforced: %v", err)
	}
}
