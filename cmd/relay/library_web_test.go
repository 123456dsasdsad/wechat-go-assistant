package main

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/123456dsasdsad/wechat-go-assistant/internal/library"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
)

func TestLibraryPortalCannotChooseOwnerOrCommitModelOutput(t *testing.T) {
	defer metadb.CloseAll()
	s, e := library.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	server := httptest.NewServer(library.Handler(s, strings.Repeat("k", 32)))
	defer server.Close()
	in := inboundFixture(t)
	in.library, e = library.NewClient(server.URL, strings.Repeat("k", 32))
	if e != nil {
		t.Fatal(e)
	}
	_, e = in.workspaceAction("owner", workspaceRequest{Action: "library", Library: &library.Request{Owner: "other", Action: "begin", Key: "fake"}}, "")
	if e == nil {
		t.Fatal("browser can inject intake")
	}
	var batch library.Intake
	ctx := context.Background()
	e = in.library.Call(ctx, library.Request{Owner: "other", Action: "begin", Key: "x"}, &batch)
	if e != nil {
		t.Fatal(e)
	}
	_, e = in.workspaceAction("owner", workspaceRequest{Action: "library", Library: &library.Request{Owner: "other", Action: "get", MaterialID: 99}}, "")
	if e == nil {
		t.Fatal("foreign read succeeded")
	}
}
