package watches

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sample(now time.Time) Update {
	return Update{Conversation: "abcd1234", Thread: "01a00000-0000-7000-8000-000000000001", Title: "实验进度", State: "running", Text: "首批训练已开始", Sequence: 100, Observed: now}
}
func TestMirrorPersistsAndRejectsOutOfOrderOrRebinding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "watch.json")
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC()
	u := sample(now)
	_, changed, e := s.Put("owner", u, now)
	if e != nil || !changed {
		t.Fatal(e)
	}
	v, changed, e := s.Put("owner", u, now.Add(20*time.Second))
	if e != nil || changed || len(v.Events) != 1 {
		t.Fatal("heartbeat duplicated progress", e, v)
	}
	u.Sequence--
	if _, _, e = s.Put("owner", u, now); e == nil {
		t.Fatal("stale update accepted")
	}
	u.Sequence = 100
	u.Text = "different"
	if _, _, e = s.Put("owner", u, now); e == nil {
		t.Fatal("conflicting update accepted")
	}
	u = sample(now)
	u.Thread = "01a00000-0000-7000-8000-000000000002"
	if _, _, e = s.Put("owner", u, now); e == nil {
		t.Fatal("rebound to other task")
	}
	s, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	v, ok := s.Current("owner", "abcd1234")
	if !ok || len(v.Events) != 1 {
		t.Fatal("binding lost after restart")
	}
	if _, ok = s.Current("another", "abcd1234"); ok {
		t.Fatal("wrong owner authorized")
	}
	if !strings.Contains(v.Status(now.Add(2*time.Minute)), "同步已中断") {
		t.Fatal("stale mirror looks current")
	}
}
func TestRolloutOnlyPublishesVisibleAssistantTextAndHandlesPartialAppend(t *testing.T) {
	p := filepath.Join(t.TempDir(), "rollout.jsonl")
	thread := sample(time.Now()).Thread
	content := `{"timestamp":"2026-10-09T00:00:00Z","type":"session_meta","payload":{"id":"` + thread + `"}}
{"timestamp":"2026-10-09T00:00:01Z","type":"event_msg","payload":{"type":"task_started"}}
{"timestamp":"2026-10-09T00:00:02Z","type":"response_item","payload":{"type":"function_call","arguments":"PRIVATE_KEY"}}
{"timestamp":"2026-10-09T00:00:03Z","type":"response_item","payload":{"type":"message","role":"assistant","phase":"analysis","content":[{"type":"output_text","text":"PRIVATE_REASONING"}]}}
{"timestamp":"2026-10-09T00:00:04Z","type":"response_item","payload":{"type":"message","role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"正在训练中文任务"}]}}
`
	if e := os.WriteFile(p, []byte(content), 0600); e != nil {
		t.Fatal(e)
	}
	r := Reader{Thread: thread}
	if e := r.Read(p); e != nil {
		t.Fatal(e)
	}
	if r.Latest.Text != "正在训练中文任务" || r.Latest.State != "running" {
		t.Fatal(r.Latest)
	}
	offset := r.Offset
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0600)
	f.WriteString(`{"timestamp":"2026-10-09T00:00:05Z","type":"event_msg","payload":{"type":"task_complete"}}`)
	f.Close()
	if e := r.Read(p); e != nil || r.Offset != offset || r.Latest.State != "running" {
		t.Fatal("partial line consumed", e)
	}
	f, _ = os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0600)
	f.WriteString("\n")
	f.Close()
	if e := r.Read(p); e != nil || r.Latest.State != "done" {
		t.Fatal(e, r.Latest)
	}
	if e := r.Read(p); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(r.Latest.Text, "PRIVATE") {
		t.Fatal("private data exposed")
	}
}
