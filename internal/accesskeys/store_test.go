package accesskeys

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPrimaryOutboxRestartRotationAndHandoff(t *testing.T) {
	root := t.TempDir()
	secret := strings.Repeat("s", 64)
	s, e := Open(root, secret)
	if e != nil {
		t.Fatal(e)
	}
	g, created, e := s.EnsurePrimary("owner")
	if e != nil || !created {
		t.Fatal(e, created)
	}
	p, e := s.Pending("owner")
	if e != nil || len(p) != 1 {
		t.Fatal(e)
	}
	raw := strings.Split(p[0].Text, "\n\n")[1]
	if _, e = s.Lookup(raw); e != nil {
		t.Fatal(e)
	}
	cookie, _, e := s.Session(g)
	if e != nil {
		t.Fatal(e)
	}
	handoff, e := s.Handoff(g)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ConsumeHandoff(handoff); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ConsumeHandoff(handoff); e == nil {
		t.Fatal("one-time login reused")
	}
	s.Close()
	stored, e := os.ReadFile(filepath.Join(root, "accesskeys.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(stored), raw) {
		t.Fatal("plaintext key persisted")
	}
	s, e = Open(root, secret)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if _, e = s.AuthorizeSession(cookie); e != nil {
		t.Fatal("restart lost login", e)
	}
	if p, e = s.Pending("owner"); e != nil || len(p) != 1 {
		t.Fatal("restart lost encrypted notification", e)
	}
	if e = s.Delivered("owner", g.ID); e != nil {
		t.Fatal(e)
	}
	if revealed, e := s.Reveal("owner", g.ID); e != nil || revealed != raw {
		t.Fatal("display lost after notification delivery", e)
	}
	if _, e := s.Reveal("other", g.ID); e == nil {
		t.Fatal("foreign owner could display key")
	}
	if p, e = s.Pending("owner"); e != nil || len(p) != 0 {
		t.Fatal("accepted notification retained", e)
	}
	if _, _, e = s.Rotate("owner"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.AuthorizeSession(cookie); e == nil {
		t.Fatal("old primary browser still authorized")
	}
	if _, e = s.Lookup(raw); e == nil {
		t.Fatal("old primary key still authorized")
	}
	if _, e := s.Reveal("owner", g.ID); e == nil {
		t.Fatal("revoked key displayed")
	}
}

func TestOldHashOnlyKeyDisplayMigratesOnLogin(t *testing.T) {
	s, e := Open(t.TempDir(), strings.Repeat("s", 64))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	g, raw, e := s.Rotate("owner")
	if e != nil {
		t.Fatal(e)
	}
	s.db.Exec("DELETE FROM access_values WHERE key_id=?", g.ID)
	s.Delivered("owner", g.ID)
	if _, e = s.Reveal("owner", g.ID); e == nil {
		t.Fatal("hash-only key was recoverable")
	}
	if _, e = s.Lookup(raw); e != nil {
		t.Fatal(e)
	}
	if shown, e := s.Reveal("owner", g.ID); e != nil || shown != raw {
		t.Fatal("original key changed during migration", e)
	}
}
func TestTemporaryScopesExpiryRevokeAndOwnerIsolation(t *testing.T) {
	s, e := Open(t.TempDir(), strings.Repeat("s", 64))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	now := time.Now()
	s.now = func() time.Time { return now }
	g, raw, e := s.Temporary("owner", "share", "/wechat-files/manage/apps/abc/", 1, true)
	if e != nil {
		t.Fatal(e)
	}
	cookie, _, e := s.Session(g)
	if e != nil {
		t.Fatal(e)
	}
	if !Allows(g, "/wechat-files/manage/apps/abc/api/x", "GET") || Allows(g, "/wechat-files/manage/apps/abcd/", "GET") || Allows(g, "/wechat-files/manage/apps/abc/../other/", "GET") || Allows(g, "/wechat-files/manage/apps/abc/", "POST") {
		t.Fatal("scope boundary or readonly failed")
	}
	if e = s.Revoke("stranger", g.ID); e == nil {
		t.Fatal("cross-owner revoke")
	}
	if _, e = s.Lookup(raw); e != nil {
		t.Fatal(e)
	}
	now = now.Add(61 * time.Second)
	if _, e = s.Lookup(raw); e == nil {
		t.Fatal("expired key accepted")
	}
	if _, e = s.AuthorizeSession(cookie); e == nil {
		t.Fatal("expired browser accepted")
	}
	g, raw, e = s.Temporary("owner", "share", "/wechat-files/task/", 60, false)
	if e != nil {
		t.Fatal(e)
	}
	cookie, _, e = s.Session(g)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Revoke("owner", g.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.AuthorizeSession(cookie); e == nil {
		t.Fatal("revoked browser accepted")
	}
	for _, scope := range []string{"/wechat-files/manage/apps/../", "/wechat-files/%2e%2e/", "//evil/", "/wechat-files/tasks?x=1"} {
		if _, _, e = s.Temporary("owner", "bad", scope, 60, true); e == nil {
			t.Fatal("invalid scope accepted", scope)
		}
	}
}
