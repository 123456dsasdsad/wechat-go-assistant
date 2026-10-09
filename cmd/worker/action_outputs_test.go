package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/123456dsasdsad/wechat-go-assistant/internal/artifacts"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/files"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/jobs"
)

func TestActionOutputsRecoverRootFilesAndPreserveOtherOutputs(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "outputs"), 0700)
	os.WriteFile(filepath.Join(dir, "outputs", "report.txt"), []byte("report"), 0600)
	os.WriteFile(filepath.Join(dir, "outputs", "manifest.json"), []byte(`{"files":["outputs/report.txt"]}`), 0600)
	os.WriteFile(filepath.Join(dir, "行动清单.md"), []byte("actual markdown"), 0600)
	os.WriteFile(filepath.Join(dir, "行动清单.json"), []byte(`[{"text":"actual action"}]`), 0600)
	task := jobs.Task{Input: "请提取行动清单：", Attachments: []files.Ref{{Name: "材料来源-12345678.json"}}}
	if err := registerActionOutputs(task, dir); err != nil {
		t.Fatal(err)
	}
	entries, err := artifacts.Read(dir)
	if err != nil || len(entries) != 3 {
		t.Fatal(entries, err)
	}
	if err := registerActionOutputs(task, dir); err != nil {
		t.Fatal("not idempotent", err)
	}
}

func TestActionOutputsDoNotInventFilesOrCollectUnrelatedTasks(t *testing.T) {
	dir := t.TempDir()
	task := jobs.Task{Input: "请提取行动清单：", Attachments: []files.Ref{{Name: "材料来源-12345678.json"}}}
	if err := registerActionOutputs(task, dir); err != nil {
		t.Fatal(err)
	}
	entries, err := artifacts.Read(dir)
	if err != nil || len(entries) != 0 {
		t.Fatal(entries, err)
	}
	os.WriteFile(filepath.Join(dir, "行动清单.json"), []byte("[]"), 0600)
	if err := registerActionOutputs(jobs.Task{Input: "other task"}, dir); err != nil {
		t.Fatal(err)
	}
	entries, _ = artifacts.Read(dir)
	if len(entries) != 0 {
		t.Fatal("unrequested file collected")
	}
}

func TestActionOutputsRejectLinks(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(t.TempDir(), "secret.json")
	os.WriteFile(out, []byte("secret"), 0600)
	if err := os.Symlink(out, filepath.Join(dir, "行动清单.json")); err != nil {
		t.Skip("symlink not permitted")
	}
	task := jobs.Task{Input: "请提取行动清单：", Attachments: []files.Ref{{Name: "材料来源-12345678.json"}}}
	if err := registerActionOutputs(task, dir); err == nil {
		t.Fatal("link accepted")
	}
}
