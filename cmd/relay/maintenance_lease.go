package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/jobs"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

func maintenanceLease(store *jobs.Store, key, dir string) http.Handler {
	var mu sync.Mutex
	var lease string
	var until time.Time
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+key)) != 1 {
			http.Error(w, "unauthorized", 401)
			return
		}
		var body struct{ Action, Lease string }
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body) != nil {
			http.Error(w, "invalid_lease", 400)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		marker := filepath.Join(dir, "maintenance-drain.flag")
		if body.Action == "release" {
			if lease == "" || body.Lease != lease {
				http.Error(w, "stale_lease", 409)
				return
			}
			os.Remove(marker)
			lease = ""
			until = time.Time{}
			json.NewEncoder(w).Encode(map[string]bool{"released": true})
			return
		}
		if body.Action != "acquire" {
			http.Error(w, "invalid_action", 400)
			return
		}
		if time.Now().Before(until) {
			http.Error(w, "maintenance_busy", 409)
			return
		}
		until = time.Now().Add(15 * time.Minute)
		if e := store.BeginMaintenance(until); e != nil {
			until = time.Time{}
			http.Error(w, "maintenance_busy", 409)
			return
		}
		b := make([]byte, 16)
		if _, e := rand.Read(b); e != nil {
			os.Remove(marker)
			until = time.Time{}
			http.Error(w, "lease_failed", 500)
			return
		}
		lease = hex.EncodeToString(b)
		json.NewEncoder(w).Encode(map[string]string{"lease": lease})
	})
}
