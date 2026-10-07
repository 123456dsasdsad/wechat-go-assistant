package files

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const MaxResults = 128

func ValidResults(refs []Ref) bool {
	if len(refs) > MaxResults {
		return false
	}
	seen := map[string]bool{}
	for _, ref := range refs {
		if !ValidRef(ref) || seen[ref.ID] {
			return false
		}
		seen[ref.ID] = true
	}
	return true
}
func (s *Store) downloadSignature(ref Ref, owner string, expiry int64) string {
	h := hmac.New(sha256.New, s.key)
	fmt.Fprintf(h, "download\x00%s\x00%s\x00%d", owner, ref.ID, expiry)
	return hex.EncodeToString(h.Sum(nil))
}
func (s *Store) DownloadLink(publicURL, owner string, ref Ref) (string, error) {
	actual, e := s.Get(owner, ref.ID)
	if e != nil || actual != ref {
		return "", errors.New("output_unavailable")
	}
	u, e := url.Parse(publicURL)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("invalid_download_origin")
	}
	expiry := s.now().Add(24 * time.Hour).Unix()
	u.Path = strings.TrimRight(u.Path, "/") + "/result/" + ref.ID
	u.RawQuery = url.Values{"e": {strconv.FormatInt(expiry, 10)}, "token": {s.downloadSignature(ref, owner, expiry)}}.Encode()
	return u.String(), nil
}
func (s *Store) DownloadHandler(publicURL string) http.Handler {
	origin, _ := url.Parse(publicURL)
	prefix := strings.TrimRight(origin.Path, "/") + "/result/"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method != "GET" || !strings.HasPrefix(r.URL.Path, prefix) {
			http.NotFound(w, r)
			return
		}
		id := strings.TrimPrefix(r.URL.Path, prefix)
		expiry, e := strconv.ParseInt(r.URL.Query().Get("e"), 10, 64)
		token := r.URL.Query().Get("token")
		s.mu.Lock()
		record, ok := s.state.Files[id]
		now := s.now()
		s.mu.Unlock()
		if !ok || !validHex(id, 24) || e != nil || expiry <= now.Unix() || expiry > now.Add(24*time.Hour+time.Minute).Unix() || !validHex(token, 64) || !hmac.Equal([]byte(token), []byte(s.downloadSignature(record.Ref, record.Owner, expiry))) {
			http.Error(w, "download authorization required", 401)
			return
		}
		f, e := s.OpenBlob(record.Ref)
		if e != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": record.Name}))
		w.Header().Set("Content-Length", strconv.FormatInt(record.Size, 10))
		http.ServeContent(w, r, record.Name, record.Created, f)
	})
}
