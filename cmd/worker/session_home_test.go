package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSessionHomesDoNotShareMemoryOrNativeIndexes(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "base")
	os.Mkdir(base, 0700)
	os.WriteFile(filepath.Join(base, "config.toml"), []byte("model='example'"), 0600)
	os.WriteFile(filepath.Join(base, "memory.md"), []byte("another conversation"), 0600)
	cfg := config{WorkRoot: root, CodexHome: base, IsolateCodexHomes: true}
	a, err := sessionHome(cfg, "aaaaaaaa", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := sessionHome(cfg, "bbbbbbbb", "")
	if err != nil || a == b {
		t.Fatal(a, b, err)
	}
	if _, err = os.Stat(filepath.Join(a, "memory.md")); !os.IsNotExist(err) {
		t.Fatal("shared memories copied")
	}
	again, err := sessionHome(cfg, "aaaaaaaa", "12345678-1234-1234-1234-123456789012")
	if err != nil || again != a {
		t.Fatal(again, err)
	}
	legacy, err := sessionHome(cfg, "cccccccc", "12345678-1234-1234-1234-123456789012")
	if err != nil || legacy != base {
		t.Fatal("legacy thread lost", legacy, err)
	}
}
