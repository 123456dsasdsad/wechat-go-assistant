package files

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStreamingUploadBeyondOldLimitAndRejectsExtraParts(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	token, _ := s.Grant("owner", "large-link")
	h, _ := PublicHandler(s, "https://example.com/wechat-files/")
	for _, extra := range []bool{false, true} {
		pipe, writer := io.Pipe()
		form := multipart.NewWriter(writer)
		done := make(chan error, 1)
		go func() {
			part, e := form.CreateFormFile("file", "large.bin")
			if e == nil {
				_, e = io.CopyN(part, ioRepeater{}, 32<<20)
			}
			if e == nil && extra {
				e = form.WriteField("unexpected", "field")
			}
			if e == nil {
				e = form.Close()
			}
			writer.CloseWithError(e)
			done <- e
		}()
		r := httptest.NewRequest("POST", "/wechat-files/upload", pipe)
		r.Header.Set("Content-Type", form.FormDataContentType())
		r.Header.Set("Origin", "https://example.com")
		r.AddCookie(&http.Cookie{Name: "wechat_upload", Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		pipe.Close()
		if e := <-done; e != nil && !extra {
			t.Fatal(e)
		}
		if !extra && w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		if extra && w.Code != 400 {
			t.Fatal("extra multipart accepted", w.Code)
		}
		if got := s.List("owner"); len(got) != 1 || got[0].Size != 32<<20 {
			t.Fatal("partial/invalid multipart committed", got)
		}
	}
}

func TestPublicUploadRequiresGrantAndOrigin(t *testing.T) {
	s, _ := Open(t.TempDir())
	token, _ := s.Grant("owner", "link")
	h, err := PublicHandler(s, "https://example.com/wechat-files/")
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/wechat-files/session", strings.NewReader(`{"token":"`+token+`"}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross-origin request accepted")
	}
	r = httptest.NewRequest("POST", "/wechat-files/session", strings.NewReader(`{"token":"`+token+`"}`))
	r.Header.Set("Origin", "https://example.com")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	cookie := w.Result().Cookies()[0]
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatal("weak upload cookie")
	}
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, _ := form.CreateFormFile("file", "资料.txt")
	part.Write([]byte("campus fixture"))
	form.Close()
	r = httptest.NewRequest("POST", "/wechat-files/upload", bytes.NewReader(body.Bytes()))
	r.Header.Set("Origin", "https://example.com")
	r.Header.Set("Content-Type", form.FormDataContentType())
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("anonymous upload accepted")
	}
	r = httptest.NewRequest("POST", "/wechat-files/upload", bytes.NewReader(body.Bytes()))
	r.Header.Set("Origin", "https://example.com")
	r.Header.Set("Content-Type", form.FormDataContentType())
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var response struct {
		File Ref `json:"file"`
	}
	json.Unmarshal(w.Body.Bytes(), &response)
	if !ValidRef(response.File) || len(s.List("other")) != 0 || len(s.List("owner")) != 1 {
		t.Fatal(response)
	}
	r = httptest.NewRequest("GET", "/jobs/claim", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code == 200 {
		t.Fatal("public endpoint exposes job API")
	}
}

func TestAnalyzeBindsGrantedOwnerAndExistingFile(t *testing.T) {
	s, _ := Open(t.TempDir())
	token, _ := s.Grant("owner", "link")
	ref, _ := s.Save("owner", "first", "note.txt", strings.NewReader("hello"))
	other, _ := s.Save("other", "second", "other.txt", strings.NewReader("private"))
	called := 0
	h, _ := PublicHandler(s, "https://example.com/wechat-files/", func(owner, source string, selected Ref, instruction string) (map[string]any, error) {
		called++
		if owner != "owner" || source != "webtask:0123456789abcdef01234567" || selected != ref || instruction != "read it" {
			t.Fatal("callback changed the owner, file, or task")
		}
		return map[string]any{"ok": true}, nil
	})
	for _, test := range []struct {
		id     string
		cookie bool
		code   int
	}{
		{ref.ID, false, 401},
		{other.ID, true, 404},
		{ref.ID, true, 200},
	} {
		body, _ := json.Marshal(map[string]string{"file_id": test.id, "instruction": "read it", "source": "0123456789abcdef01234567"})
		r := httptest.NewRequest("POST", "/wechat-files/analyze", bytes.NewReader(body))
		r.Header.Set("Origin", "https://example.com")
		if test.cookie {
			r.AddCookie(&http.Cookie{Name: "wechat_upload", Value: token})
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != test.code {
			t.Fatal(w.Code, test.code)
		}
	}
	if called != 1 {
		t.Fatal("unauthorized task created")
	}
}
