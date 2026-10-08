package main

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/artifacts"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/jobs"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type trainingSpec struct {
	Enabled      bool     `json:"enabled"`
	Argv         []string `json:"argv"`
	ResumeArgv   []string `json:"resume_argv"`
	Checkpoint   string   `json:"checkpoint"`
	ProgressFile string   `json:"progress_file"`
}
type trainingRecord struct {
	Task        jobs.Task `json:"task"`
	Directory   string    `json:"directory"`
	Started     bool      `json:"started"`
	LastCommand string    `json:"last_command"`
}

var trainingMu sync.Mutex

func readTrainingSpec(dir string) (trainingSpec, error) {
	var s trainingSpec
	root, e := os.OpenRoot(dir)
	if e != nil {
		return s, e
	}
	defer root.Close()
	b, e := readRootFile(root, "training-control.json", 16<<10)
	if e != nil {
		return s, e
	}
	if len(b) > 16<<10 || json.Unmarshal(b, &s) != nil || !s.Enabled || len(s.Argv) == 0 || len(s.Argv) > 64 || len(s.ResumeArgv) > 64 {
		return s, errors.New("invalid_training_spec")
	}
	for _, arg := range append(append([]string{}, s.Argv...), s.ResumeArgv...) {
		if len(arg) > 4096 || strings.ContainsRune(arg, 0) {
			return s, errors.New("invalid_training_argv")
		}
	}
	for _, p := range []string{s.Checkpoint, s.ProgressFile} {
		if p != "" && (!filepath.IsLocal(p) || p == ".") {
			return s, errors.New("invalid_training_path")
		}
	}
	return s, nil
}
func rememberTraining(cfg config, task jobs.Task, dir string) error {
	trainingMu.Lock()
	defer trainingMu.Unlock()
	path := filepath.Join(cfg.WorkRoot, "training-index.json")
	m := map[string]trainingRecord{}
	b, e := metadb.ReadJSON(path)
	if e == nil {
		if json.Unmarshal(b, &m) != nil {
			return errors.New("invalid_training_registry")
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	if _, ok := m[task.ID]; !ok {
		m[task.ID] = trainingRecord{Task: task, Directory: dir}
	}
	return metadb.WriteJSON(path, m)
}
func trainingManager(ctx context.Context, cfg config, key string) {
	for pause(ctx, 3*time.Second) {
		trainingMu.Lock()
		path := filepath.Join(cfg.WorkRoot, "training-index.json")
		m := map[string]trainingRecord{}
		b, e := metadb.ReadJSON(path)
		if e != nil || json.Unmarshal(b, &m) != nil {
			trainingMu.Unlock()
			continue
		}
		changed := false
		for id, v := range m {
			if v.Started {
				continue
			}
			if _, e := readTrainingSpec(v.Directory); e != nil {
				continue
			}
			var control struct {
				Cancel bool `json:"cancel"`
			}
			if postWorker(ctx, cfg.RelayURL, key, "/jobs/control", map[string]string{"id": v.Task.ID, "lease": v.Task.Lease}, &control) != nil || control.Cancel {
				continue
			}
			if e := launchTraining(cfg, id, false, ""); e == nil {
				v.Started = true
				m[id] = v
				changed = true
			}
		}
		if changed {
			_ = metadb.WriteJSON(path, m)
		}
		trainingMu.Unlock()
		var commands []struct {
			ID       string        `json:"id"`
			Training jobs.Training `json:"training"`
		}
		if postWorker(ctx, cfg.RelayURL, key, "/jobs/training/commands", nil, &commands) != nil {
			continue
		}
		for _, j := range commands {
			_, ok := m[j.ID]
			if !ok || j.Training.Command != "resume" {
				continue
			}
			_ = launchTraining(cfg, j.ID, true, j.Training.CommandID)
		}
	}
}
func trainingRun(ctx context.Context, cfg config, id string, resume bool, commandID string) (runErr error) {
	key, e := os.ReadFile(cfg.RelayKeyFile)
	if e != nil {
		return e
	}
	trainingMu.Lock()
	b, e := metadb.ReadJSON(filepath.Join(cfg.WorkRoot, "training-index.json"))
	trainingMu.Unlock()
	if e != nil {
		return e
	}
	m := map[string]trainingRecord{}
	if json.Unmarshal(b, &m) != nil {
		return errors.New("invalid_training_registry")
	}
	v, ok := m[id]
	if !ok {
		return errors.New("unknown_training")
	}
	relative, e := filepath.Rel(cfg.WorkRoot, v.Directory)
	if e != nil || !filepath.IsLocal(relative) {
		return errors.New("invalid_training_directory")
	}
	unlock, e := lockTraining(v.Directory)
	if e != nil {
		return e
	}
	defer unlock()
	defer func() {
		if runErr != nil {
			notify, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			var failed jobs.Training
			_ = postWorker(notify, cfg.RelayURL, strings.TrimSpace(string(key)), "/jobs/training/state", map[string]string{"id": v.Task.ID, "lease": v.Task.Lease}, &failed)
			failed.State, failed.Error, failed.Acknowledged = "failed", "training_runner_failed", commandID
			_ = postWorker(notify, cfg.RelayURL, strings.TrimSpace(string(key)), "/jobs/training/update", jobs.TrainingUpdate{ID: v.Task.ID, Lease: v.Task.Lease, Training: failed}, nil)
		}
	}()
	spec, e := readTrainingSpec(v.Directory)
	if e != nil {
		return e
	}
	argv := spec.Argv
	root, e := os.OpenRoot(v.Directory)
	if e != nil {
		return e
	}
	defer root.Close()
	checkpoint := func() bool {
		if spec.Checkpoint == "" {
			return false
		}
		f, e := root.Open(spec.Checkpoint)
		if e != nil {
			return false
		}
		defer f.Close()
		info, e := f.Stat()
		return e == nil && info.Mode().IsRegular() && info.Size() > 0
	}
	if resume {
		if len(spec.ResumeArgv) == 0 || !checkpoint() {
			return errors.New("checkpoint_resume_unavailable")
		}
		argv = spec.ResumeArgv
	}
	log, e := os.OpenFile(filepath.Join(v.Directory, "training.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer log.Close()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = v.Directory
	cmd.Stdout = log
	cmd.Stderr = log
	configureTraining(cmd)
	if e = cmd.Start(); e != nil {
		return e
	}
	stop := context.AfterFunc(ctx, func() { stopTraining(cmd) })
	defer stop()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	started := time.Now()
	state := jobs.Training{State: "running", Acknowledged: commandID, Resumable: len(spec.ResumeArgv) > 0}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	publish := func() {
		state.ElapsedSeconds = int64(time.Since(started).Seconds())
		if checkpoint() {
			state.Checkpoint = spec.Checkpoint
		} else {
			state.Checkpoint = ""
		}
		cpu, rss := trainingResources(cmd.Process.Pid, started)
		if rss > 0 {
			state.CPUPercent, state.RSSMiB = cpu, rss
		}
		if spec.ProgressFile != "" {
			if b, e := readRootFile(root, spec.ProgressFile, 64<<10); e == nil {
				var p struct {
					Epoch, Total int
					Loss         *float64
				}
				if json.Unmarshal(b, &p) == nil && p.Epoch >= 0 && p.Total >= 0 {
					state.Epoch = p.Epoch
					state.Total = p.Total
					state.Loss = p.Loss
				}
			}
		}
		_ = postWorker(ctx, cfg.RelayURL, strings.TrimSpace(string(key)), "/jobs/training/update", jobs.TrainingUpdate{ID: v.Task.ID, Lease: v.Task.Lease, Training: state}, nil)
	}
	publish()
	stopping := false
	var forceStop <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			stopTraining(cmd)
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				killTraining(cmd)
				<-done
			}
			return ctx.Err()
		case <-forceStop:
			killTraining(cmd)
			forceStop = nil
		case e := <-done:
			if stopping {
				state.State = "stopped"
			} else if e != nil {
				state.State = "failed"
				state.Error = "training_process_failed"
			} else {
				state.State = "completed"
			}
			publish()
			if state.State == "completed" {
				entries, e := artifacts.Read(v.Directory)
				if e == nil {
					c := jobs.Completion{ID: v.Task.ID, Lease: v.Task.Lease, Result: "训练已实际完成。轮次 " + strconv.Itoa(state.Epoch) + "/" + strconv.Itoa(state.Total) + "，耗时 " + strconv.FormatInt(state.ElapsedSeconds, 10) + " 秒。", OutputPending: len(entries) > 0}
					for _, f := range entries {
						c.ExpectedOutputs = append(c.ExpectedOutputs, jobs.OutputIntent{Name: f.Name, SHA256: f.SHA256, Size: f.Size})
					}
					out := resultOutbox{Training: true, Task: v.Task, Completion: c, Directory: v.Directory, Entries: entries}
					lock := outboxLock(v.Task.ID)
					lock.Lock()
					e = metadb.WriteJSON(resultPath(cfg, out), out)
					lock.Unlock()
					if e != nil {
						return e
					}
				}
			}
			return nil
		case <-tick.C:
			var control jobs.Training
			if postWorker(ctx, cfg.RelayURL, strings.TrimSpace(string(key)), "/jobs/training/state", map[string]string{"id": v.Task.ID, "lease": v.Task.Lease}, &control) == nil && control.Command == "stop" && control.CommandID != state.Acknowledged {
				state.Acknowledged = control.CommandID
				stopping = true
				stopTraining(cmd)
				forceStop = time.After(10 * time.Second)
			}
			publish()
		}
	}
}
func readRootFile(root *os.Root, name string, limit int64) ([]byte, error) {
	f, e := root.Open(name)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, limit+1))
	if int64(len(b)) > limit {
		return nil, errors.New("training_file_too_large")
	}
	return b, e
}
