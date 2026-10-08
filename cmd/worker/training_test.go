package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTrainingSpecRequiresDeclaredResumeAndLocalPaths(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "training-control.json")
	os.WriteFile(p, []byte(`{"enabled":true,"argv":["python","train.py"],"resume_argv":["python","train.py","--resume","checkpoint.pt"],"checkpoint":"checkpoint.pt","progress_file":"progress.json"}`), 0600)
	s, e := readTrainingSpec(dir)
	if e != nil || len(s.ResumeArgv) == 0 {
		t.Fatal(s, e)
	}
	os.WriteFile(p, []byte(`{"enabled":true,"argv":["python"],"checkpoint":"../other"}`), 0600)
	if _, e = readTrainingSpec(dir); e == nil {
		t.Fatal("traversal")
	}
}
