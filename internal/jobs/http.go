package jobs

import (
	"crypto/subtle"
	"encoding/json"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/files"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/models"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func Handler(s *Store, key string, current ...func() models.Choice) http.Handler {
	return HandlerWithFiles(s, key, nil, current...)
}
func HandlerWithFiles(s *Store, key string, fileStore *files.Store, current ...func() models.Choice) http.Handler {
	return HandlerWithOutputs(s, key, fileStore, nil, current...)
}
func HandlerWithOutputs(s *Store, key string, fileStore, outputStore *files.Store, current ...func() models.Choice) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if key == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+key)) != 1 {
			http.Error(w, "unauthorized", 401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "POST" && r.URL.Path == "/jobs/claim" {
			if raw, e := os.ReadFile(filepath.Join(filepath.Dir(s.dir), "maintenance-drain.flag")); e == nil {
				until, e := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
				if e == nil && until > time.Now().Unix() && until < time.Now().Add(20*time.Minute).Unix() {
					w.WriteHeader(204)
					return
				}
			}
		}
		switch {
		case r.Method == "POST" && r.URL.Path == "/jobs/progress":
			var update ProgressUpdate
			d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20))
			d.DisallowUnknownFields()
			if d.Decode(&update) != nil || len(update.Text) > 256<<10 || update.Sequence == 0 {
				http.Error(w, "invalid progress", 400)
				return
			}
			if e := s.UpdateProgress(update, time.Now()); e != nil {
				http.Error(w, "stale progress or lease", 409)
				return
			}
			io.WriteString(w, `{"ok":true}`)
		case r.Method == "POST" && (r.URL.Path == "/jobs/steering/poll" || r.URL.Path == "/jobs/steering/ack"):
			var request SteerRequest
			d := json.NewDecoder(io.LimitReader(r.Body, 2048))
			d.DisallowUnknownFields()
			if d.Decode(&request) != nil {
				http.Error(w, "invalid steering request", 400)
				return
			}
			if r.URL.Path == "/jobs/steering/poll" {
				pending, e := s.PendingSupplements(request.ID, request.Lease, time.Now())
				if e != nil {
					http.Error(w, "invalid steering lease", 409)
					return
				}
				json.NewEncoder(w).Encode(pending)
			} else {
				if e := s.AckSupplement(request, time.Now()); e != nil {
					http.Error(w, "steering receipt rejected", 409)
					return
				}
				io.WriteString(w, "{}")
			}
		case r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/jobs/") && strings.Contains(r.URL.Path, "/outputs/") && outputStore != nil:
			parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
			if len(parts) != 4 || parts[0] != "jobs" || parts[2] != "outputs" {
				http.NotFound(w, r)
				return
			}
			index, e := strconv.Atoi(parts[3])
			if e != nil || index < 0 || index >= files.MaxResults {
				http.Error(w, "invalid output index", 400)
				return
			}
			owner, e := s.OutputOwner(parts[1], r.Header.Get("X-Job-Lease"), time.Now(), false)
			if e != nil {
				http.Error(w, "invalid output lease", 403)
				return
			}
			name := r.URL.Query().Get("name")
			ref, e := outputStore.SaveVerified(owner, "job-output:"+parts[1]+":"+parts[3], name, r.Body, r.ContentLength, r.Header.Get("X-File-SHA256"))
			if e != nil {
				http.Error(w, "output storage or integrity failed", 400)
				return
			}
			json.NewEncoder(w).Encode(ref)
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/jobs/") && fileStore != nil:
			parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
			if len(parts) != 4 || parts[0] != "jobs" || parts[2] != "files" {
				http.NotFound(w, r)
				return
			}
			ref, err := s.Attachment(parts[1], r.Header.Get("X-Job-Lease"), parts[3], time.Now())
			if err != nil {
				http.Error(w, "invalid file lease", 403)
				return
			}
			reader, err := fileStore.OpenBlob(ref)
			if err != nil {
				http.Error(w, "file unavailable", 404)
				return
			}
			defer reader.Close()
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Length", fmtSize(ref.Size))
			w.Header().Set("X-File-SHA256", ref.SHA256)
			io.Copy(w, reader)
		case r.Method == "GET" && r.URL.Path == "/health":
			choice := models.Choice{Model: "gpt-6-sol", Effort: "high"}
			if len(current) > 0 {
				choice = current[0]()
			}
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "model": choice.Model, "effort": choice.Effort})
		case r.Method == "POST" && r.URL.Path == "/jobs/claim":
			task, err := s.Claim(time.Now())
			if err != nil {
				http.Error(w, "storage error", 500)
				return
			}
			if task == nil {
				w.WriteHeader(204)
				return
			}
			json.NewEncoder(w).Encode(task)
		case r.Method == "POST" && r.URL.Path == "/jobs/result":
			var c Completion
			d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128*1024))
			d.DisallowUnknownFields()
			if err := d.Decode(&c); err != nil {
				http.Error(w, "invalid result", 400)
				return
			}
			if len(c.Outputs) > 0 {
				owner, e := s.OutputOwner(c.ID, c.Lease, time.Now(), true)
				if e != nil || outputStore == nil || !files.ValidResults(c.Outputs) {
					http.Error(w, "invalid result outputs", 409)
					return
				}
				for _, ref := range c.Outputs {
					actual, e := outputStore.Get(owner, ref.ID)
					if e != nil || actual != ref {
						http.Error(w, "invalid result outputs", 409)
						return
					}
				}
			}
			if err := s.Complete(c, time.Now()); err != nil {
				http.Error(w, "stale or invalid result", 409)
				return
			}
			json.NewEncoder(w).Encode(map[string]bool{"ok": true})
		case r.Method == "POST" && r.URL.Path == "/jobs/heartbeat":
			var body struct {
				ID    string `json:"id"`
				Lease string `json:"lease"`
			}
			d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
			d.DisallowUnknownFields()
			if d.Decode(&body) != nil {
				http.Error(w, "invalid heartbeat", 400)
				return
			}
			if err := s.Heartbeat(body.ID, body.Lease, time.Now()); err != nil {
				http.Error(w, "stale lease", 409)
				return
			}
			json.NewEncoder(w).Encode(map[string]bool{"ok": true})
		default:
			http.NotFound(w, r)
		}
	})
}

func fmtSize(n int64) string { return strconv.FormatInt(n, 10) }
