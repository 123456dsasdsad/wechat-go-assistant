package main

import (
	"github.com/123456dsasdsad/wechat-go-assistant/internal/files"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/jobs"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
	"strings"
	"testing"
	"time"
)

func TestExplicitBatchFocusPausesOldMediaAndOldText(t *testing.T) {
	defer metadb.CloseAll()
	old := jobs.Job{ID: strings.Repeat("a", 24), Owner: "o", Status: "done", Created: time.Now().Add(-time.Hour), MediaDeferred: true, Outputs: []files.Ref{{ID: strings.Repeat("c", 24)}}}
	current := jobs.Job{ID: strings.Repeat("b", 24), Owner: "o", Status: "done", MediaRequested: true, Created: time.Now(), Outputs: []files.Ref{{ID: strings.Repeat("d", 24)}}, DeliveryParts: []string{"text"}}
	history := []jobs.Job{old, current}
	focus := focusedMediaJobs(history)
	selected := selectMediaJobs(history)
	if focus["o"] != current.ID || len(selected) != 1 || selected[0].ID != current.ID {
		t.Fatal("old text/media interfered with explicit current batch", selected)
	}
	current.Status = "delivered"
	history[1] = current
	old.DeliveryParts = []string{"text"}
	history[0] = old
	if len(selectMediaJobs(history)) != 0 {
		t.Fatal("old media resumed automatically after current batch")
	}
}
