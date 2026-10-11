package library

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCampusAppOwnerScopePersistenceAndPrivateProxy(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("X-Campus-App-Owner") != "" {
			t.Error("private credential reached app")
		}
		io.WriteString(w, r.URL.RequestURI())
	}))
	defer up.Close()
	root := t.TempDir()
	s, _ := Open(root)
	a, e := s.AddCampusApp(context.Background(), "a", "Test app", up.URL)
	if e != nil {
		t.Fatal(e)
	}
	updated, e := s.AddCampusApp(context.Background(), "a", "Renamed", up.URL+"/")
	if e != nil || updated.ID != a.ID {
		t.Fatal("duplicate route", e, updated)
	}
	if _, e = s.CampusApp("other", a.ID); !errors.Is(e, ErrNotFound) {
		t.Fatal("foreign read", e)
	}
	if e = s.RemoveCampusApp("other", a.ID); !errors.Is(e, ErrNotFound) {
		t.Fatal("foreign removal", e)
	}
	s.Close()
	s, _ = Open(root)
	defer s.Close()
	server := httptest.NewServer(Handler(s, strings.Repeat("k", 32)))
	defer server.Close()
	req, _ := http.NewRequest("GET", server.URL+"/library/app/"+a.ID+"/api/projects?q=test", nil)
	req.Header.Set("Authorization", "Bearer "+strings.Repeat("k", 32))
	req.Header.Set("X-Campus-App-Owner", "a")
	req.Header.Set("Cookie", "wechat_manage=private")
	res, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || string(body) != "/api/projects?q=test" {
		t.Fatal(res.StatusCode, string(body))
	}
	req.Header.Set("X-Campus-App-Owner", "other")
	res, e = http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Fatal("foreign proxy", res.StatusCode)
	}
	if e = s.RemoveCampusApp("a", a.ID); e != nil {
		t.Fatal(e)
	}
	apps, _ := s.CampusApps("a")
	if len(apps) != 0 {
		t.Fatal("route remained")
	}
}
func TestCampusAppRejectsNonLocalAndCredentialAddresses(t *testing.T) {
	for _, raw := range []string{"http://example.org:80", "http://169.254.169.254:80", "http://127.0.0.1", "https://127.0.0.1:8000", "http://user:pass@127.0.0.1:8000", "http://127.0.0.1:8000/?token=x", "file:///etc/passwd"} {
		if _, e := campusAppURL(raw); e == nil {
			t.Fatal("invalid address accepted", raw)
		}
	}
}
