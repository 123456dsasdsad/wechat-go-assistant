//go:build linux

package main

import (
	"context"
	"encoding/json"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/files"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/jobs"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTrainingStopsResumesRealCheckpointAndReturnsArtifacts(t *testing.T) {
	if _, e := os.Stat("/usr/bin/python3"); e != nil {
		t.Skip("python3 required for CPU fixture")
	}
	root := t.TempDir()
	store, e := jobs.Open(filepath.Join(root, "jobs"))
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	outputs, _ := files.Open(filepath.Join(root, "outputs"))
	server := httptest.NewServer(jobs.HandlerWithOutputs(store, "private-test-key", nil, outputs))
	defer server.Close()
	j, _ := store.Enqueue("training", "train", "owner", "reply")
	task, _ := store.Claim(time.Now())
	if e = store.Complete(jobs.Completion{ID: j.ID, Lease: task.Lease, Result: "Training registered"}, time.Now()); e != nil {
		t.Fatal(e)
	}
	dir := filepath.Join(root, "turn")
	os.MkdirAll(dir, 0700)
	script := `import json,pathlib,time,sys
p=pathlib.Path('checkpoint.json');n=0;x=0.0
if '--resume' in sys.argv:
 v=json.loads(p.read_text());n=v['epoch'];x=v['x']
for epoch in range(n+1,61):
 for _ in range(10000):x-=0.00001*2*(x-3)
 data={'epoch':epoch,'total':60,'loss':(x-3)**2,'x':x}
 pathlib.Path('progress.tmp').write_text(json.dumps(data));pathlib.Path('progress.tmp').replace('progress.json')
 pathlib.Path('checkpoint.tmp').write_text(json.dumps(data));pathlib.Path('checkpoint.tmp').replace(p)
 time.sleep(.15)
pathlib.Path('outputs').mkdir(exist_ok=True)
pathlib.Path('outputs/result.json').write_text(json.dumps(data))
pathlib.Path('outputs/manifest.json').write_text(json.dumps({'files':['outputs/result.json']}))
`
	os.WriteFile(filepath.Join(dir, "train.py"), []byte(script), 0600)
	spec := trainingSpec{Enabled: true, Argv: []string{"/usr/bin/python3", "train.py"}, ResumeArgv: []string{"/usr/bin/python3", "train.py", "--resume"}, Checkpoint: "checkpoint.json", ProgressFile: "progress.json"}
	b, _ := json.Marshal(spec)
	os.WriteFile(filepath.Join(dir, "training-control.json"), b, 0600)
	key := filepath.Join(root, "key")
	os.WriteFile(key, []byte("private-test-key"), 0600)
	cfg := config{WorkRoot: root, RelayURL: server.URL, RelayKeyFile: key}
	if e = rememberTraining(cfg, *task, dir); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- trainingRun(ctx, cfg, j.ID, false, "") }()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		v, _ := store.Snapshot(j.ID)
		if v.Training.Epoch >= 3 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if e = store.RequestTraining("owner", j.ID, "stop"); e != nil {
		t.Fatal(e)
	}
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	v, _ := store.Snapshot(j.ID)
	if v.Training.State != "stopped" || v.Training.Checkpoint == "" || v.Training.RSSMiB <= 0 {
		t.Fatal(v.Training)
	}
	if e = store.RequestTraining("owner", j.ID, "resume"); e != nil {
		t.Fatal(e)
	}
	v, _ = store.Snapshot(j.ID)
	if e = trainingRun(ctx, cfg, j.ID, true, v.Training.CommandID); e != nil {
		t.Fatal(e)
	}
	v, _ = store.Snapshot(j.ID)
	if v.Training.State != "completed" || v.Training.Epoch != 60 {
		t.Fatal(v.Training)
	}
	b, e = __readOutbox(cfg, j.ID+"-training")
	if e != nil {
		t.Fatal(e)
	}
	var out resultOutbox
	json.Unmarshal(b, &out)
	if e = recoverResult(ctx, cfg, "private-test-key", out); e != nil {
		t.Fatal(e)
	}
	v, _ = store.Snapshot(j.ID)
	if v.OutputPending || len(v.Outputs) != 1 {
		t.Fatal("training artifacts not delivered", v.OutputPending, len(v.Outputs))
	}
}
