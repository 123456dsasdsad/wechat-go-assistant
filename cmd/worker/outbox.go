package main

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/artifacts"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/jobs"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"time"
)

type resultOutbox struct {
	Training       bool              `json:"training,omitempty"`
	Task           jobs.Task         `json:"task"`
	Completion     jobs.Completion   `json:"completion"`
	Directory      string            `json:"directory"`
	Entries        []artifacts.Entry `json:"entries"`
	ResultAccepted bool              `json:"result_accepted"`
	Finished       bool              `json:"finished"`
}

var outboxLocks sync.Map

func outboxLock(id string) *sync.Mutex {
	v, _ := outboxLocks.LoadOrStore(id, &sync.Mutex{})
	return v.(*sync.Mutex)
}

func outboxPath(cfg config, id string) string {
	return filepath.Join(cfg.WorkRoot, "outbox", id+".json")
}
func resultPath(cfg config, v resultOutbox) string {
	if v.Training {
		return outboxPath(cfg, v.Task.ID+"-training")
	}
	return outboxPath(cfg, v.Task.ID)
}
func queueResult(cfg config, task jobs.Task, c jobs.Completion, dir string) (resultOutbox, error) {
	v := resultOutbox{Task: task, Completion: c, Directory: dir}
	if c.Error == "" {
		entries, e := artifacts.Read(dir)
		if e != nil {
			return v, e
		}
		v.Entries = entries
		for _, f := range entries {
			v.Completion.ExpectedOutputs = append(v.Completion.ExpectedOutputs, jobs.OutputIntent{Name: f.Name, SHA256: f.SHA256, Size: f.Size})
		}
		v.Completion.OutputPending = len(entries) > 0
	}
	lock := outboxLock(task.ID)
	lock.Lock()
	defer lock.Unlock()
	if e := os.MkdirAll(filepath.Dir(outboxPath(cfg, task.ID)), 0700); e != nil {
		return v, e
	}
	return v, metadb.WriteJSON(outboxPath(cfg, task.ID), v)
}
func recoverResult(ctx context.Context, cfg config, key string, v resultOutbox) error {
	lock := outboxLock(v.Task.ID)
	lock.Lock()
	defer lock.Unlock()
	var err error
	v, err = reloadResult(cfg, v)
	if err != nil || v.Finished {
		return err
	}
	path := resultPath(cfg, v)
	rel, e := filepath.Rel(cfg.WorkRoot, v.Directory)
	if e != nil || !filepath.IsLocal(rel) {
		return errors.New("invalid_outbox_directory")
	}
	if !v.ResultAccepted {
		if v.Training {
			b, e := metadb.ReadJSON(outboxPath(cfg, v.Task.ID))
			if e == nil {
				var base resultOutbox
				if json.Unmarshal(b, &base) != nil || !base.Finished {
					return errors.New("previous_result_pending")
				}
			} else if !os.IsNotExist(e) {
				return e
			}
		}
		endpoint := "/jobs/result"
		if v.Training {
			endpoint = "/jobs/training/outputs"
		}
		if e := postWorker(ctx, cfg.RelayURL, key, endpoint, v.Completion, nil); e != nil {
			return e
		}
		v.ResultAccepted = true
		if e = metadb.WriteJSON(path, v); e != nil {
			return e
		}
	}
	if v.Completion.OutputPending {
		var control struct {
			Cancel bool `json:"cancel"`
		}
		if e := postWorker(ctx, cfg.RelayURL, key, "/jobs/control", map[string]string{"id": v.Task.ID, "lease": v.Task.Lease}, &control); e != nil {
			return e
		}
		if control.Cancel {
			v.Finished = true
			return metadb.WriteJSON(path, v)
		}
		actual, e := artifacts.Read(v.Directory)
		if e != nil {
			return e
		}
		if !reflect.DeepEqual(actual, v.Entries) {
			return errors.New("output_file_changed")
		}
		refs, e := uploadOutputs(ctx, cfg.RelayURL, key, v.Task, v.Directory)
		if e != nil {
			return e
		}
		if e = postWorker(ctx, cfg.RelayURL, key, "/jobs/outputs/finish", jobs.Completion{ID: v.Task.ID, Lease: v.Task.Lease, Outputs: refs}, nil); e != nil {
			return e
		}
	}
	v.Finished = true
	return metadb.WriteJSON(path, v)
}
func reloadResult(cfg config, v resultOutbox) (resultOutbox, error) {
	b, e := metadb.ReadJSON(resultPath(cfg, v))
	if e != nil {
		return v, e
	}
	if json.Unmarshal(b, &v) != nil {
		return v, errors.New("invalid_outbox")
	}
	return v, nil
}

// Accept the text first. Recovery owns attachment transport and never repeats inference.
func acceptResult(ctx context.Context, cfg config, key string, v resultOutbox) error {
	lock := outboxLock(v.Task.ID)
	lock.Lock()
	defer lock.Unlock()
	v, e := reloadResult(cfg, v)
	if e != nil || v.ResultAccepted {
		return e
	}
	if e = postWorker(ctx, cfg.RelayURL, key, "/jobs/result", v.Completion, nil); e != nil {
		return e
	}
	v.ResultAccepted = true
	return metadb.WriteJSON(resultPath(cfg, v), v)
}
func replayOutbox(ctx context.Context, cfg config, key string) {
	slots := make(chan struct{}, 2)
	var active sync.Map
	var workers sync.WaitGroup
	defer workers.Wait()
	for pause(ctx, 5*time.Second) {
		entries, e := os.ReadDir(filepath.Join(cfg.WorkRoot, "outbox"))
		if e != nil {
			continue
		}
		for _, entry := range entries {
			if filepath.Ext(entry.Name()) != ".sqlite" {
				continue
			}
			path := filepath.Join(cfg.WorkRoot, "outbox", entry.Name()[:len(entry.Name())-7])
			b, e := metadb.ReadJSON(path)
			var v resultOutbox
			if e == nil && json.Unmarshal(b, &v) == nil && !v.Finished && len(v.Task.ID) == 24 {
				if _, exists := active.LoadOrStore(v.Task.ID, true); exists {
					continue
				}
				select {
				case slots <- struct{}{}:
					workers.Add(1)
					go func(v resultOutbox) {
						defer workers.Done()
						defer active.Delete(v.Task.ID)
						defer func() { <-slots }()
						jobCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
						defer cancel()
						_ = recoverResult(jobCtx, cfg, key, v)
					}(v)
				default:
					active.Delete(v.Task.ID)
				}
			}
			if ctx.Err() != nil {
				return
			}
		}
	}
}
func cachedOutbox(cfg config, id, lease string) (bool, error) {
	lock := outboxLock(id)
	lock.Lock()
	defer lock.Unlock()
	path := outboxPath(cfg, id)
	b, e := metadb.ReadJSON(path)
	if os.IsNotExist(e) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	var v resultOutbox
	if json.Unmarshal(b, &v) != nil {
		return false, errors.New("invalid_outbox")
	}
	if !v.ResultAccepted {
		v.Task.Lease = lease
		v.Completion.Lease = lease
		e = metadb.WriteJSON(path, v)
	}
	return true, e
}
