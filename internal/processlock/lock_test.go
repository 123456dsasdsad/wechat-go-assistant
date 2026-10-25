package processlock

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestLockSurvivesStaleFileAndExcludesAnotherOwner(t *testing.T) {
	p := filepath.Join(t.TempDir(), "process.lock")
	if err := os.WriteFile(p, []byte("obsolete pid"), 0600); err != nil {
		t.Fatal(err)
	}
	release, err := Acquire(p)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := Acquire(p); err == nil {
		second()
		t.Fatal("second owner accepted")
	}
	release()
	release()
	release, err = Acquire(p)
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func TestKernelReleasesLockAfterAbruptExit(t *testing.T) {
	if p := os.Getenv("WECHAT_LOCK_TEST_PATH"); p != "" {
		if _, err := Acquire(p); err != nil {
			os.Exit(2)
		}
		os.Exit(0) // Deliberately bypass release.
	}
	p := filepath.Join(t.TempDir(), "process.lock")
	c := exec.Command(os.Args[0], "-test.run=^TestKernelReleasesLockAfterAbruptExit$")
	c.Env = append(os.Environ(), "WECHAT_LOCK_TEST_PATH="+p)
	if b, err := c.CombinedOutput(); err != nil {
		t.Fatal(err, string(b))
	}
	release, err := Acquire(p)
	if err != nil {
		t.Fatal(err)
	}
	release()
}
