package userinput

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"
)

func TestMCPToolWaitsForHumanAndPreservesStringID(t *testing.T) {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	defer inR.Close()
	defer inW.Close()
	defer outR.Close()
	defer outW.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan Request, 1)
	answer := make(chan struct{})
	go ServeMCP(ctx, inR, outW, func(ctx context.Context, r Request) (Response, error) {
		started <- r
		select {
		case <-answer:
			return Response{Answers: map[string]Answer{"choice": {Answers: []string{"SVG"}}}}, nil
		case <-ctx.Done():
			return Response{}, ctx.Err()
		}
	})
	responses := make(chan map[string]any, 4)
	go func() {
		s := bufio.NewScanner(outR)
		for s.Scan() {
			var v map[string]any
			json.Unmarshal(s.Bytes(), &v)
			responses <- v
		}
	}()
	io.WriteString(inW, `{"jsonrpc":"2.0","id":"pick-format","method":"tools/call","params":{"name":"ask_user","arguments":{"questions":[{"id":"choice","header":"Format","question":"Which format?"}]}}}`+"\n")
	select {
	case r := <-started:
		if r.ID != `mcp:"pick-format"` {
			t.Fatal(r)
		}
	case <-time.After(time.Second):
		t.Fatal("tool not called")
	}
	select {
	case <-responses:
		t.Fatal("answered without human input")
	case <-time.After(20 * time.Millisecond):
	}
	close(answer)
	select {
	case r := <-responses:
		if r["id"] != "pick-format" || r["result"].(map[string]any)["isError"] != false {
			t.Fatal(r)
		}
	case <-time.After(time.Second):
		t.Fatal("answer not returned")
	}
}
func TestQuestionValidationAndChoices(t *testing.T) {
	q := Question{ID: "format", Question: "Which?", Options: []Option{{Label: "PNG"}, {Label: "SVG"}}}
	for _, tc := range []struct{ input, want string }{{"2", "SVG"}, {"PNG", "PNG"}, {"my answer", "my answer"}} {
		v, e := Normalize(q, tc.input)
		if e != nil || v != tc.want {
			t.Fatal(v, e)
		}
	}
	if _, e := Normalize(q, "3"); e == nil {
		t.Fatal("invalid number silently selected")
	}
	if Validate(Request{ID: "id", Questions: []Question{q, q}}) == nil {
		t.Fatal("duplicate question IDs")
	}
	q.IsSecret = true
	if Validate(Request{ID: "id", Questions: []Question{q}}) == nil {
		t.Fatal("secret question sent through public result page")
	}
}
