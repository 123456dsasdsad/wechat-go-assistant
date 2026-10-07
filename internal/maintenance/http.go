package maintenance

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
)

func Handler(store *Store, key string) http.Handler {
	bridge := Bridge(key)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if key == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+key)) != 1 {
			http.Error(w, "unauthorized", 401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "GET" && r.URL.Path == "/maintenance/release":
			bridge.ServeHTTP(w, r)
		case r.Method == "POST" && r.URL.Path == "/maintenance/report":
			var report Report
			d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024))
			d.DisallowUnknownFields()
			if d.Decode(&report) != nil || store.Put(report) != nil {
				http.Error(w, "report_rejected", 400)
				return
			}
			json.NewEncoder(w).Encode(map[string]bool{"ok": true})
		case r.Method == "GET" && r.URL.Path == "/maintenance/reports":
			json.NewEncoder(w).Encode(map[string]string{"text": store.Latest(r.URL.Query().Get("kind"))})
		default:
			http.NotFound(w, r)
		}
	})
}
