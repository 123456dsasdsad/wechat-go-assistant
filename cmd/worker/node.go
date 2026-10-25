package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/jobs"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/models"
	"time"
)

func prepareNode(cfg *config) error {
	if cfg.NodeID == "" {
		return nil
	}
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return err
	}
	cfg.nodeInstance = hex.EncodeToString(b[:])
	return nil
}
func nodeHeartbeat(ctx context.Context, cfg config, catalog models.Catalog, key string) {
	if cfg.NodeID == "" {
		return
	}
	n := jobs.NodeHeartbeat{ID: cfg.NodeID, Instance: cfg.nodeInstance, Version: "2026.10.16-ha1", Ready: cfg.NodeReady, Capabilities: []string{"execute"}}
	for _, m := range catalog.Models {
		n.Models = append(n.Models, m.ID)
	}
	if cfg.LibraryRoot != "" {
		n.Capabilities = append(n.Capabilities, "library")
	}
	if cfg.TranscriptionCommand != "" {
		n.Capabilities = append(n.Capabilities, "transcription")
	}
	for {
		if e := postWorker(ctx, cfg.RelayURL, key, "/nodes/heartbeat", n, nil); e != nil {
			fmt.Println(`{"type":"node_heartbeat_failed"}`)
		}
		if !pause(ctx, 10*time.Second) {
			return
		}
	}
}
