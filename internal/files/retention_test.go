package files

import (
	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
	"strings"
	"testing"
	"time"
)

func TestActiveReferenceSurvivesNormalExpiryThenExpiresAfterRelease(t *testing.T) {
	defer metadb.CloseAll()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	s.now = func() time.Time { return now }
	ref, err := s.Save("owner", "source", "data.txt", strings.NewReader("data"))
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(7 * 24 * time.Hour)
	if err = s.Retain(map[string]bool{ref.ID: true}, now.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Hour)
	if err = s.Prune(); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get("owner", ref.ID); err != nil {
		t.Fatal("active attachment expired", err)
	}
	now = now.Add(24 * time.Hour)
	if err = s.Prune(); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get("owner", ref.ID); err == nil {
		t.Fatal("unreferenced file retained forever")
	}
}

func TestFinishUploadDoesNotRequireSecondFullSizeAllocation(t *testing.T) {
	defer metadb.CloseAll()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.BeginUpload("owner", "aaaaaaaaaaaaaaaaaaaaaaaa", "data.txt", 4, "12345678")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AppendUpload("owner", u.ID, 0, strings.NewReader("data")); err != nil {
		t.Fatal(err)
	}
	s.space = func(string) (int64, error) { return ReserveDiskBytes, nil }
	ref, err := s.FinishUpload("owner", u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if s.Conversation("owner", ref.ID) != "12345678" {
		t.Fatal("scope lost")
	}
	again, err := s.FinishUpload("owner", u.ID)
	if err != nil || again != ref {
		t.Fatal("finish not idempotent", again, err)
	}
}
