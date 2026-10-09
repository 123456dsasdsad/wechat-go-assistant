package accountupload

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAccountManagementAuthenticationAndQueuedDeletion(t *testing.T) {
	root, ids := twoAccountPool(t)
	store, _ := Open(t.TempDir())
	token, _ := store.Grant("owner", "message")
	handler, err := PublicHandler(store, "https://example.com/wechat-files/", root)
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path, body, origin string, auth bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Origin", origin)
		if auth {
			r.AddCookie(&http.Cookie{Name: cookieName, Value: token})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	payload := `{"account_ids":["` + ids[0] + `"],"request_id":"111111111111111111111111"}`
	if w := request("GET", pathPrefix+"list", "", "", false); w.Code != 401 {
		t.Fatal("anonymous account list", w.Code)
	}
	if w := request("POST", pathPrefix+"delete", payload, "https://example.com", false); w.Code != 401 {
		t.Fatal("anonymous deletion", w.Code)
	}
	if w := request("POST", pathPrefix+"delete", payload, "https://evil.example", true); w.Code != 403 {
		t.Fatal("cross-origin deletion", w.Code)
	}
	if w := request("GET", pathPrefix+"delete", "", "", true); w.Code != 405 {
		t.Fatal("GET deletion", w.Code)
	}
	for _, bad := range []string{`{"account_ids":["../secret"],"request_id":"111111111111111111111111"}`, payload + ` {}`, `{"account_ids":["` + ids[0] + `"]}`, `{"account_ids":["missing"],"request_id":"111111111111111111111111"}`} {
		if w := request("POST", pathPrefix+"delete", bad, "https://example.com", true); w.Code != 400 {
			t.Fatal("invalid deletion accepted", w.Code)
		}
	}
	w := request("GET", pathPrefix+"list", "", "", true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "free@example.com") {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, secret := range []string{"access_token", "refresh_token", "id_token", "free-refresh", "team-refresh", "test-private-key"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("credentials in listing", secret)
		}
	}
	w = request("POST", pathPrefix+"delete", payload, "https://example.com", true)
	var response struct {
		Receipt Receipt `json:"receipt"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil || response.Receipt.Status != "pending" {
		t.Fatal(w.Code, w.Body.String())
	}
	// The public endpoint only stages the mutation; activation belongs to relay.
	views, _ := ListAccounts(root)
	if len(views) != 2 {
		t.Fatal("HTTP deletion bypassed maintenance lease")
	}
	w = request("GET", pathPrefix+"list", "", "", true)
	if !strings.Contains(w.Body.String(), `"pending_delete":true`) {
		t.Fatal("pending deletion not displayed")
	}
	if err := store.Process(response.Receipt.ID, root, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	w = request("GET", pathPrefix+"list", "", "", true)
	if w.Code != 200 || strings.Contains(w.Body.String(), ids[0]) || !strings.Contains(w.Body.String(), ids[1]) {
		t.Fatal("deleted account still listed", w.Body.String())
	}
}
