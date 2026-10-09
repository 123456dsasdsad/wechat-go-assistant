package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestQueueUsesRealJobCountsAndMetricsWithoutChangingTraining(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "status"), 0700)
	p := filepath.Join(root, "status", "experiment_queue.json")
	b := []byte(`{"counts":{"running":99},"jobs":[{"id":"run1","method":"paper","dataset":"set1","seed":1,"gpu":2,"status":"running"},{"id":"run2","status":"pending"}]}`)
	os.WriteFile(p, b, 0600)
	m := filepath.Join(root, "results", "run1", "fold_0")
	os.MkdirAll(m, 0700)
	os.WriteFile(filepath.Join(m, "live_progress.json"), []byte(`{"epoch":4,"microstep":200,"optimizer_update":25,"loss":0.42,"private":"never expose"}`), 0600)
	u, e := snapshot(config{Root: root}, time.Now())
	if e != nil || !strings.Contains(u.Text, "总计 2 · 运行 1 · 排队 1") || !strings.Contains(u.Text, "轮次 4") || !strings.Contains(u.Text, "loss 0.42") || strings.Contains(u.Text, "never expose") {
		t.Fatal(u, e)
	}
	after, _ := os.ReadFile(p)
	if string(after) != string(b) {
		t.Fatal("collector changed training")
	}
	os.WriteFile(p, []byte(`{"jobs":[{"id":"run1","status":"complete"}]}`), 0600)
	u, e = snapshot(config{Root: root}, time.Now())
	if e != nil || u.State != "done" {
		t.Fatal(u, e)
	}
	os.WriteFile(p, []byte(`{"jobs":[{"id":"../../escape","status":"running"}]}`), 0600)
	if _, e = snapshot(config{Root: root}, time.Now()); e == nil {
		t.Fatal("escaped experiment directory")
	}
}
