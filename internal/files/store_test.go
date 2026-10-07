package files

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOwnerReplayPersistenceAndGrantExpiry(t *testing.T) {
	root := t.TempDir()
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := s.Save("owner", "message-1", "资料.txt", strings.NewReader("original"))
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.Save("owner", "message-1", "changed.txt", strings.NewReader("changed"))
	if err != nil || again != ref {
		t.Fatal("replay changed file")
	}
	if _, err = s.Get("other", ref.ID); err == nil {
		t.Fatal("foreign owner could get file")
	}
	token, err := s.Grant("owner", "link-1")
	if err != nil {
		t.Fatal(err)
	}
	s, err = Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if other, _ := s.Grant("owner", "link-1"); other != token {
		t.Fatal("link replay changed token")
	}
	index, _ := os.ReadFile(filepath.Join(root, "index.json"))
	if bytes.Contains(index, []byte(token)) {
		t.Fatal("raw link token persisted")
	}
	if owner, ok := s.Authorize(token); !ok || owner != "owner" {
		t.Fatal("grant lost after reopen")
	}
	now := time.Now()
	s.now = func() time.Time { return now.Add(31 * time.Minute) }
	if _, ok := s.Authorize(token); ok {
		t.Fatal("expired grant accepted")
	}
	if got := s.List("owner"); len(got) != 1 || got[0] != ref {
		t.Fatal(got)
	}
}
func TestLimitsAndRetiredBlobRemoval(t *testing.T) {
	s, _ := Open(t.TempDir())
	s.space = func(string) (int64, error) { return ReserveDiskBytes + (64 << 10), nil }
	if _, err := s.Save("owner", "large", "large.zip", io.LimitReader(ioRepeater{}, 65<<10)); err == nil {
		t.Fatal("disk headroom ignored")
	}
	s.space = availableDisk
	for _, name := range []string{"../secret", "C:\\secret", "bad\x00.txt", ".."} {
		if _, err := s.Save("owner", name, name, strings.NewReader("a")); err == nil {
			t.Fatal("unsafe filename accepted")
		}
	}
	ref, _ := s.Save("owner", "old", "a.txt", strings.NewReader("a"))
	now := time.Now()
	s.now = func() time.Time { return now.Add(8 * 24 * time.Hour) }
	if _, err := s.Save("owner", "new", "b.txt", strings.NewReader("b")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.root, ref.ID+".bin")); !os.IsNotExist(err) {
		t.Fatal("retired blob kept")
	}
}

type ioRepeater struct{}

// Used only through finite LimitReader instances in tests.
func (r ioRepeater) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	return len(p), nil
}

func TestStreamingFileExceedsOldCeiling(t *testing.T) {
	s, _ := Open(t.TempDir())
	ref, err := s.Save("owner", "larger", "large.bin", io.LimitReader(ioRepeater{}, 32<<20))
	if err != nil || ref.Size != 32<<20 {
		t.Fatal("old 25 MiB cap still active", ref, err)
	}
	s, err = Open(s.root)
	if err != nil || len(s.List("owner")) != 1 {
		t.Fatal("large file metadata did not reopen", err)
	}
}
