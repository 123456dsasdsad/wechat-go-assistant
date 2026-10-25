package main

import (
	"errors"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/conversations"
	"os"
	"path/filepath"
)

// New native threads use a separate Codex state directory, including memories
// and thread indexes. Legacy threads retain their existing home until explicitly
// migrated with their native state; we never silently start a replacement thread.
func sessionHome(cfg config, cid, thread string) (string, error) {
	if !cfg.IsolateCodexHomes || cid == "" {
		return cfg.CodexHome, nil
	}
	if !conversations.ValidID(cid) {
		return "", errors.New("invalid_session_home")
	}
	dir := filepath.Join(cfg.WorkRoot, "codex-homes", cid)
	ready := filepath.Join(dir, ".session-home-ready")
	if _, err := os.Stat(ready); err == nil {
		return dir, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if thread != "" {
		return cfg.CodexHome, nil
	}
	data, err := os.ReadFile(filepath.Join(cfg.CodexHome, "config.toml"))
	if err != nil {
		return "", err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	if err = os.WriteFile(filepath.Join(dir, "config.toml"), data, 0600); err != nil {
		return "", err
	}
	if err = os.WriteFile(ready, []byte(cid), 0600); err != nil {
		return "", err
	}
	return dir, nil
}
