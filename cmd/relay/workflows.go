package main

import (
	"context"
	"errors"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/files"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/jobs"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/materials"
	"io"
	"strings"
	"time"
)

func deliverySummary(j jobs.Job) map[string]any {
	rows := []map[string]any{}
	missing := 0
	for _, f := range j.Outputs {
		accepted := j.PartDelivered(f.ID) || j.PackageDelivered()
		if !accepted {
			missing++
		}
		rows = append(rows, map[string]any{"id": f.ID, "name": f.Name, "accepted": accepted, "via_package": j.PackageDelivered(), "via_link": f.Size > directMediaBytes || j.PartDelivered("package-link:"+j.MediaPackage.ID)})
	}
	return map[string]any{"text_accepted": j.PartDelivered("text"), "outputs": rows, "missing": missing, "output_pending": j.OutputPending, "package_accepted": j.PackageDelivered(), "paused": j.MediaDeferred, "requested": j.MediaRequested, "note": "接口接收情况，不代表手机送达或已读；普通消息不会触发旧附件补发。"}
}
func (in *inbound) workflowAction(owner, reply string, b workspaceRequest) (any, bool, error) {
	if !strings.HasPrefix(b.Action, "material_") && !strings.HasPrefix(b.Action, "project_") {
		return nil, false, nil
	}
	if in.materials == nil {
		return nil, true, errors.New("materials_unavailable")
	}
	cid := b.Conversation
	if cid == "" {
		cid = in.sessions.Current().ID
	}
	if _, ok := in.sessions.Get(cid); !ok {
		return nil, true, errors.New("conversation_not_found")
	}
	switch b.Action {
	case "project_save":
		p, e := in.materials.Project(owner, b.Name)
		if e != nil {
			p = materials.Project{Owner: owner, Title: b.Name}
		}
		if b.Input != "" {
			p.Topics = strings.FieldsFunc(b.Input, func(r rune) bool { return r == ',' || r == '，' })
		}
		p, e = in.materials.SaveProject(p)
		return p, true, e
	case "project_member":
		p, e := in.materials.Project(owner, b.ID)
		if e != nil {
			return nil, true, e
		}
		exists := false
		for _, id := range p.Conversations {
			if id == cid {
				exists = true
			}
		}
		if !exists {
			p.Conversations = append(p.Conversations, cid)
		}
		p, e = in.materials.SaveProject(p)
		return p, true, e
	case "material_start":
		p, e := in.materials.Begin(owner, "portal:"+b.Source, b.Name, cid)
		return p, true, e
	case "material_get":
		p, e := in.materials.Get(owner, b.ID)
		if e != nil {
			return nil, true, e
		}
		rows, actionErr := in.packActions(owner, p)
		return map[string]any{"pack": p, "actions": rows, "action_error": actionErr}, true, nil
	case "material_action":
		e := in.materials.ActionStatus(owner, b.ID, b.Name, b.Input)
		return map[string]bool{"ok": e == nil}, true, e
	case "material_bind":
		p, e := in.materials.Bind(owner, b.ID, b.Name)
		return p, true, e
	case "material_conversation":
		e := in.materials.SetConversation(owner, b.ID, cid)
		return map[string]bool{"ok": e == nil}, true, e
	case "material_append":
		if b.Input == "" && len(b.Files) == 0 {
			return nil, true, errors.New("empty_material_entry")
		}
		refs := []files.Ref{}
		for _, id := range b.Files {
			f, e := in.files.Get(owner, id)
			if e != nil {
				return nil, true, e
			}
			src, e := in.files.OpenBlob(f)
			if e != nil {
				return nil, true, e
			}
			e = in.materials.PutBlob(f, src)
			src.Close()
			if e != nil {
				return nil, true, e
			}
			refs = append(refs, f)
		}
		p, e := in.materials.Append(owner, b.ID, materials.Entry{ID: "portal:" + b.Source, Kind: "pasted", Text: b.Input, Speaker: b.Name, Time: b.Body, Files: refs})
		return p, true, e
	case "material_save":
		p, e := in.materials.Update(owner, b.ID, "saved", "", "")
		return p, true, e
	case "material_resume":
		p, e := in.materials.Get(owner, b.ID)
		if e != nil {
			return nil, true, e
		}
		if p.JobID != "" {
			return nil, true, errors.New("material_already_submitted")
		}
		p, e = in.materials.Update(owner, b.ID, "collecting", "", "")
		return p, true, e
	case "material_submit":
		p, e := in.materials.Get(owner, b.ID)
		if e != nil {
			return nil, true, e
		}
		j, e := in.submitMaterial(owner, reply, p, b.Input)
		return map[string]any{"id": j.ID}, true, e
	case "material_library":
		p, e := in.materials.Get(owner, b.ID)
		if e != nil {
			return nil, true, e
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		text, e := in.libraryMaterial(ctx, owner, reply, p, b.Name)
		return map[string]any{"message": text}, true, e
	}
	return nil, true, errors.New("unknown_workflow_action")
}

func (in *inbound) packActions(owner string, p materials.Pack) ([]materials.Action, string) {
	message := ""
	if j, ok := in.queue.Snapshot(p.JobID); ok && j.Owner == owner && in.outputs != nil {
		for _, r := range j.Outputs {
			if r.Name != "行动清单.json" {
				continue
			}
			f, e := in.outputs.OpenBlob(r)
			if e == nil {
				raw, err := io.ReadAll(io.LimitReader(f, 256<<10+1))
				f.Close()
				if err == nil {
					e = in.materials.ImportActions(owner, p.ID, raw)
				}
			}
			if e != nil {
				message = e.Error()
			}
		}
	}
	rows, e := in.materials.Actions(owner, p.ID)
	if e != nil {
		message = e.Error()
	}
	return rows, message
}
