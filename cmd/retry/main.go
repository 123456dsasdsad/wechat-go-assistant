// retry is an offline admin repair helper. Run only while Relay and Worker are
// stopped; preserve old jobs and create deduplicated immutable replacement jobs.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/conversations"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/files"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/jobs"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/models"
	"github.com/123456dsasdsad/wechat-go-assistant/weixin"
	"os"
	"path/filepath"
	"strings"
)

type config struct {
	StatePath         string `json:"state_path"`
	JobsDir           string `json:"jobs_dir"`
	FilesDir          string `json:"files_dir"`
	ModelsFile        string `json:"models_file"`
	ConversationsFile string `json:"conversations_file"`
}

func main() {
	path := flag.String("config", "", "private Relay config")
	ids := flag.String("jobs", "", "original job IDs in replay order")
	prefix := flag.String("prefix", "project-zip-repair-20261007", "deduplication namespace for this repair")
	flag.Parse()
	if err := runWithPrefix(*path, strings.Split(*ids, ","), *prefix); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(path string, ids []string) error {
	return runWithPrefix(path, ids, "project-zip-repair-20261007")
}
func runWithPrefix(path string, ids []string, prefix string) error {
	if len(prefix) < 1 || len(prefix) > 60 || strings.Trim(prefix, "abcdefghijklmnopqrstuvwxyz0123456789-") != "" {
		return errors.New("invalid_repair_namespace")
	}
	var cfg config
	raw, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(raw, &cfg) != nil {
		return errors.New("private_config_unreadable")
	}
	account, err := weixin.LoadState(cfg.StatePath)
	if err != nil {
		return errors.New("wechat_state_unreadable")
	}
	owner := account.Account.OwnerID
	reply := account.Contexts[owner]
	if reply == "" {
		return errors.New("reply_context_missing")
	}
	catalog, err := models.Load(cfg.ModelsFile)
	if err != nil {
		return err
	}
	store, err := jobs.Open(cfg.JobsDir)
	if err != nil {
		return err
	}
	fs, err := files.Open(cfg.FilesDir)
	if err != nil {
		return err
	}
	sessions, err := conversations.Open(cfg.ConversationsFile)
	if err != nil {
		return err
	}
	previous := sessions.Current()
	if len(store.Active()) != 0 {
		return errors.New("active_tasks_must_finish")
	}
	selected := make([]jobs.Job, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		job, ok := store.Snapshot(id)
		if !ok || seen[id] || job.Owner != owner || job.Status != "delivered" || job.ConversationID == "" {
			return errors.New("invalid_replay_job")
		}
		seen[id] = true
		if _, ok = sessions.Get(job.ConversationID); !ok {
			return errors.New("original_conversation_missing")
		}
		if _, err = catalog.Resolve(job.Model, job.Effort); err != nil {
			return err
		}
		for _, ref := range job.Attachments {
			actual, e := fs.Get(owner, ref.ID)
			if e != nil || actual != ref {
				return errors.New("original_attachment_unavailable")
			}
		}
		selected = append(selected, job)
	}
	if len(selected) < 1 || len(selected) > 8 {
		return errors.New("unsupported_replay_count")
	}
	var repaired []map[string]any
	for _, original := range selected {
		job, e := store.EnqueueConversation(prefix+":"+original.ID, original.Input, owner, reply, models.Choice{Model: original.Model, Effort: original.Effort}, original.Attachments, original.ConversationID)
		if e != nil {
			return e
		}
		var names []string
		for _, ref := range job.Attachments {
			names = append(names, ref.Name)
		}
		if e = sessions.RecordTask(job.ConversationID, job.Input, names, job.Created); e != nil {
			return e
		}
		session, _ := sessions.Get(job.ConversationID)
		repaired = append(repaired, map[string]any{"original": original.ID, "job": job.ID, "conversation": session.Number, "model": job.Model, "effort": job.Effort, "files": len(job.Attachments), "inputPreserved": job.Input == original.Input, "conversationPreserved": job.ConversationID == original.ConversationID})
	}
	result := map[string]any{"replayed": repaired, "selectionPreserved": sessions.Current().ID == previous.ID, "originalJobsPreserved": true}
	raw, _ = json.MarshalIndent(result, "", "  ")
	if err = os.WriteFile(filepath.Join(filepath.Dir(cfg.JobsDir), prefix+"-retry.json"), raw, 0600); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}
