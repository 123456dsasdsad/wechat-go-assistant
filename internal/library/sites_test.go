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

func TestCampusAppBareAddressAndPortUpdateKeepExistingLink(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "old") }))
	defer up.Close()
	newUp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "new") }))
	defer newUp.Close()
	s, _ := Open(t.TempDir())
	defer s.Close()
	if _, e := s.AddCampusApp(context.Background(), "owner", "Test", "127.0.0.1"); e == nil || e.Error() != "campus_app_port_required" {
		t.Fatal("missing port silently guessed", e)
	}
	a, e := s.AddCampusApp(context.Background(), "owner", "Test", strings.TrimPrefix(up.URL, "http://"))
	if e != nil {
		t.Fatal(e)
	}
	reused, e := s.AddCampusApp(context.Background(), "owner", "Test", "127.0.0.1")
	if e != nil || reused.ID != a.ID {
		t.Fatal("unique registered application not recognized", reused, e)
	}
	for _, raw := range []string{"http://127.0.0.1", "http://127.0.0.1/", "http://localhost"} {
		matched, e := s.AddCampusApp(context.Background(), "owner", "Test", raw)
		if e != nil || matched.ID != a.ID {
			t.Fatal("complete loopback address not recognized", raw, matched, e)
		}
	}
	updated, e := s.UpdateCampusApp(context.Background(), "owner", a.ID, "", strings.TrimPrefix(newUp.URL, "http://"))
	if e != nil || updated.ID != a.ID || updated.URL != newUp.URL {
		t.Fatal("upgrade changed public route ID", updated, e)
	}
	if _, e := s.UpdateCampusApp(context.Background(), "other", a.ID, "Hijacked", newUp.URL); !errors.Is(e, ErrNotFound) {
		t.Fatal("foreign owner updated app", e)
	}
	other, e := s.AddCampusApp(context.Background(), "owner", "Second", up.URL)
	if e != nil {
		t.Fatal(e)
	}
	if _, e := s.AddCampusApp(context.Background(), "owner", "Ambiguous", "localhost"); e == nil || e.Error() != "campus_app_port_required" {
		t.Fatal("ambiguous port silently selected", e)
	}
	if _, e := s.UpdateCampusApp(context.Background(), "owner", a.ID, "", other.URL); e == nil || e.Error() != "campus_app_address_already_registered" {
		t.Fatal("conflicting address replaced existing route", e)
	}
	current, _ := s.CampusApp("owner", a.ID)
	if current.URL != newUp.URL {
		t.Fatal("failed update altered existing route", current)
	}
	// The original public route ID now reaches the upgraded address.
	r := httptest.NewRequest("GET", "/library/app/"+a.ID+"/", nil)
	r.Header.Set("Authorization", "Bearer "+strings.Repeat("k", 32))
	r.Header.Set("X-Campus-App-Owner", "owner")
	w := httptest.NewRecorder()
	Handler(s, strings.Repeat("k", 32)).ServeHTTP(w, r)
	if w.Code != 200 || w.Body.String() != "new" {
		t.Fatal("old route no longer works after address update", w.Code, w.Body.String())
	}
}
