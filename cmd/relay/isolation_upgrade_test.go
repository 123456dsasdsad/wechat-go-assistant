package main

import (
	"github.com/123456dsasdsad/wechat-go-assistant/internal/conversations"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
	"testing"
)

func TestBackgroundWorkspaceCreationDoesNotChangeSelection(t *testing.T) {
	defer metadb.CloseAll()
	in := inboundFixture(t)
	before := in.sessions.Current().ID
	req := workspaceRequest{Action: "conversation_create", Source: "research-project-review", Name: "研序评审"}
	result, err := in.workspaceAction("owner", req, "")
	if err != nil {
		t.Fatal(err)
	}
	cid := result.(map[string]any)["conversation"].(string)
	again, err := in.workspaceAction("owner", req, "")
	if err != nil || again.(map[string]any)["conversation"] != cid {
		t.Fatal(again, err)
	}
	if in.sessions.Current().ID != before || cid == before {
		t.Fatal("background creation changed selection")
	}
	identity := conversations.Identity{App: "research-writing", ProjectID: "project-a", ProjectName: "研序"}
	if err := in.sessions.UpdateProfile(cid, conversations.Profile{Identity: identity, Notes: "keep notes"}); err != nil {
		t.Fatal(err)
	}
	_, err = in.workspaceAction("owner", workspaceRequest{Action: "model", Source: "model-only", Conversation: cid, Model: "gpt-6-sol", Effort: "high"}, "")
	if err != nil {
		t.Fatal(err)
	}
	saved, _ := in.sessions.Get(cid)
	if saved.Profile.Notes != "keep notes" || saved.Profile.Identity != identity {
		t.Fatal("model change erased profile", saved)
	}
	j, err := in.enqueue("research-task", "检查当前项目", "owner", "reply", cid, nil)
	if err != nil || j.Identity != identity {
		t.Fatal(j, err)
	}
}
