package main

import (
	"github.com/123456dsasdsad/wechat-go-assistant/internal/files"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/jobs"
	"time"
)

func retainTaskFiles(queue *jobs.Store, inputs, outputs *files.Store) error {
	inIDs, outIDs := map[string]bool{}, map[string]bool{}
	if err := queue.Each(func(j jobs.Job) error {
		if j.Status == "queued" || j.Status == "running" || j.Training.State == "running" {
			for _, f := range j.Attachments {
				inIDs[f.ID] = true
			}
		}
		if j.Status == "done" || j.OutputPending || j.MediaDeferred {
			for _, f := range j.Outputs {
				outIDs[f.ID] = true
			}
			if j.MediaPackage.ID != "" {
				outIDs[j.MediaPackage.ID] = true
			}
		}
		return nil
	}); err != nil {
		return err
	}
	until := time.Now().Add(24 * time.Hour)
	if err := inputs.Retain(inIDs, until); err != nil {
		return err
	}
	return outputs.Retain(outIDs, until)
}
