package jobs

import (
	"encoding/json"
	"io"
	"net/http"
	"time"
)

func (s *Store) questionsHTTP(w http.ResponseWriter, r *http.Request) {
	var request QuestionRequest
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	d.DisallowUnknownFields()
	if d.Decode(&request) != nil || d.Decode(new(any)) != io.EOF {
		http.Error(w, "invalid question request", 400)
		return
	}
	var value UserQuestion
	var e error
	switch r.URL.Path {
	case "/jobs/questions/publish":
		value, e = s.PublishQuestion(request, time.Now())
	case "/jobs/questions/poll":
		value, e = s.PollQuestion(request, time.Now())
	case "/jobs/questions/resolve":
		e = s.ResolveQuestion(request, time.Now())
	default:
		http.NotFound(w, r)
		return
	}
	if e != nil {
		http.Error(w, "question or lease rejected", 409)
		return
	}
	json.NewEncoder(w).Encode(value)
}
