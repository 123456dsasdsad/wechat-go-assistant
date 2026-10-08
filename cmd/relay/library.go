package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/123456dsasdsad/wechat-go-assistant/internal/files"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/jobs"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/library"
	"github.com/123456dsasdsad/wechat-go-assistant/weixin"
)

func (in *inbound) libraryCommand(ctx context.Context, msg weixin.Message, input, quoted string, refs []files.Ref, media []weixin.Item) (bool, error) {
	command := false
	for _, p := range []string{"资料库", "本会话资料库", "开始收录", "完成收录", "取消收录", "继续收录", "收录到", "收录备注", "研究这组截图", "研究主题", "查资料", "资料 ", "文献 ", "方法 ", "综述", "更新综述", "补查文献", "移动资料", "删除资料", "恢复资料", "资料标签", "导出综述"} {
		if strings.HasPrefix(input, p) {
			command = true
			break
		}
	}
	if in.library == nil || in.libraryDrafts == nil {
		if command {
			return true, in.reply(ctx, msg, "library", "资料库尚未启用。")
		}
		return false, nil
	}
	active, e := in.libraryDrafts.Active(msg.FromUserID, in.sessions.Current().ID)
	collect := strings.HasPrefix(input, "收录到") || strings.HasPrefix(input, "研究这组截图") || (e == nil && (len(media) > 0 || len(refs) > 0 || strings.HasPrefix(input, "收录备注")))
	if !command && !collect {
		return false, nil
	}
	reply := func(text string) (bool, error) { return true, in.reply(ctx, msg, "library", text) }
	fail := func(e error) (bool, error) {
		return reply("资料操作未完成：" + e.Error() + "。已收到的资料会保留，可以重试。")
	}
	owner := msg.FromUserID
	cid := in.sessions.Current().ID
	if strings.HasPrefix(input, "本会话资料库") {
		session := in.sessions.Current()
		value := strings.TrimSpace(strings.TrimPrefix(input, "本会话资料库"))
		session.Profile.LibraryTopics = strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == '，' || r == '、' })
		if e = in.sessions.UpdateProfile(session.ID, session.Profile); e != nil {
			return fail(e)
		}
		return reply("本会话将使用这些类别的资料：" + value)
	}
	if input == "资料库" || input == "资料库列表" || input == "综述列表" {
		var ts []library.Topic
		if e := in.library.Call(ctx, library.Request{Owner: owner, Action: "topics"}, &ts); e != nil {
			return fail(e)
		}
		text := "资料库与综述：\n"
		for _, t := range ts {
			text += fmt.Sprintf("\n%s · %d 篇 · v%d", t.Name, t.Count, t.Version)
			if t.Dirty {
				text += " · 待更新"
			}
			if t.LastError != "" {
				text += " · " + t.LastError
			}
		}
		if len(ts) == 0 {
			text += "还没有研究类别。发“开始收录”，发送截图后发“完成收录”。"
		}
		token, e := in.files.Grant(owner, msg.Key()+":library")
		if e == nil {
			text += "\n\n管理页：" + in.publicURL + "manage/#" + token
		}
		return reply(text)
	}
	if strings.HasPrefix(input, "开始收录") || strings.HasPrefix(input, "继续收录") {
		if strings.HasPrefix(input, "继续收录") {
			active, e = in.libraryDrafts.Resume(owner, strings.TrimSpace(strings.TrimPrefix(input, "继续收录")), cid)
			if e == nil && active.State == "draft" {
				return reply("已恢复收录 " + active.ID + "，请继续发截图后发送“完成收录”。")
			}
			return fail(errors.New("草稿不存在或已经提交"))
		}
		collection := strings.TrimSpace(strings.TrimPrefix(input, "开始收录"))
		if collection == "" {
			collection = "研究资料"
		}
		active, e = in.libraryDrafts.Begin(owner, msg.Key(), cid, collection)
		if e != nil {
			return fail(e)
		}
		return reply("已开始收录到“" + collection + "”。连续发送截图、PDF 或“收录备注 ……”，最后发送“完成收录”。\n草稿：" + active.ID)
	}
	if input == "取消收录" {
		if e != nil {
			return reply("当前没有收录草稿。")
		}
		e = in.libraryDrafts.State(owner, active.ID, "canceled")
		if e != nil {
			return fail(e)
		}
		return reply("已退出收录，草稿保留。")
	}
	if input == "完成收录" {
		if e != nil {
			return reply("当前没有收录草稿。请先发送“开始收录”。")
		}
		if e = in.libraryDrafts.State(owner, active.ID, "syncing"); e != nil {
			return fail(e)
		}
		j, e := in.syncLibrary(ctx, active, msg.ContextToken)
		if e != nil {
			return reply("资料已保存，等待校园通道恢复后自动传输和研究。无需重新发图。\n草稿：" + active.ID)
		}
		return reply(in.libraryAck(j))
	}
	if strings.HasPrefix(input, "研究主题") || strings.HasPrefix(input, "补查文献") {
		body := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(input, "研究主题"), "补查文献"))
		if body == "" {
			return reply("请填写要研究的主题。")
		}
		active, e = in.libraryDrafts.Begin(owner, msg.Key(), cid, "研究资料")
		if e == nil {
			e = in.libraryDrafts.Append(owner, active.ID, msg.Key(), "研究主题："+body, nil)
		}
		if e == nil {
			e = in.libraryDrafts.State(owner, active.ID, "syncing")
		}
		if e != nil {
			return fail(e)
		}
		j, e := in.syncLibrary(ctx, active, msg.ContextToken)
		if e != nil {
			return reply("研究要求已保存，等待校园通道恢复。")
		}
		return reply(in.libraryAck(j))
	}
	if collect {
		immediate := false
		if e != nil {
			collection := strings.TrimSpace(strings.TrimPrefix(input, "收录到"))
			if parts := strings.Fields(collection); len(parts) > 0 {
				collection = parts[0]
			}
			if strings.HasPrefix(input, "研究这组截图") || collection == "" {
				collection = "研究资料"
			}
			active, e = in.libraryDrafts.Begin(owner, msg.Key(), cid, collection)
			immediate = true
		}
		if e != nil {
			return fail(e)
		}
		text := ""
		if strings.HasPrefix(input, "收录到") {
			rest := strings.TrimSpace(strings.TrimPrefix(input, "收录到"))
			if parts := strings.Fields(rest); len(parts) > 1 {
				text = strings.Join(parts[1:], " ")
			}
		}
		if strings.HasPrefix(input, "收录备注") {
			text = strings.TrimSpace(strings.TrimPrefix(input, "收录备注"))
		}
		if strings.HasPrefix(input, "研究这组截图") {
			text = strings.TrimSpace(strings.TrimPrefix(input, "研究这组截图"))
		}
		text += quoted
		var assets []library.Asset
		for _, ref := range refs {
			f, e := in.files.OpenBlob(ref)
			if e != nil {
				return fail(e)
			}
			a := library.Asset{SHA256: ref.SHA256, Name: ref.Name, Size: ref.Size}
			e = in.libraryDrafts.PutBlob(a, f)
			f.Close()
			if e != nil {
				return fail(e)
			}
			assets = append(assets, a)
		}
		for i, item := range media {
			b, e := in.client.Download(ctx, item)
			if e != nil {
				return fail(e)
			}
			name := fmt.Sprintf("截图-%d.jpg", i+1)
			if item.File != nil {
				name = item.File.Name
			}
			ref, e := in.files.Save(owner, msg.Key()+":library:"+strconv.Itoa(i), name, bytes.NewReader(b))
			if e != nil {
				return fail(e)
			}
			a := library.Asset{SHA256: ref.SHA256, Name: ref.Name, Size: ref.Size}
			if e = in.libraryDrafts.PutBlob(a, bytes.NewReader(b)); e != nil {
				return fail(e)
			}
			assets = append(assets, a)
		}
		if e = in.libraryDrafts.Append(owner, active.ID, msg.Key(), text, assets); e != nil {
			return fail(e)
		}
		if immediate {
			if e = in.libraryDrafts.State(owner, active.ID, "syncing"); e != nil {
				return fail(e)
			}
			j, e := in.syncLibrary(ctx, active, msg.ContextToken)
			if e != nil {
				return reply("资料已保存在草稿，通道恢复后自动继续。")
			}
			return reply(in.libraryAck(j))
		}
		return reply(fmt.Sprintf("已收录本条 %d 个文件。发送“完成收录”后统一查文献、整理方法并更新所有相关类别综述。", len(assets)))
	}
	q := library.Request{Owner: owner}
	parts := strings.Fields(input)
	switch {
	case strings.HasPrefix(input, "查资料"):
		q.Action = "search"
		q.Query = strings.TrimSpace(strings.TrimPrefix(input, "查资料"))
		var ms []library.Material
		if e = in.library.Call(ctx, q, &ms); e != nil {
			return fail(e)
		}
		var b strings.Builder
		for _, m := range ms {
			fmt.Fprintf(&b, "\n资料 %d · %s\n类别：%s\n%s\n", m.ID, m.Title, strings.Join(m.Topics, "、"), m.Summary)
		}
		if len(ms) == 0 {
			return reply("未找到匹配资料。")
		}
		return reply(b.String())
	case len(parts) == 2 && (parts[0] == "资料" || parts[0] == "文献" || parts[0] == "方法"):
		q.Action = "get"
		q.MaterialID, _ = strconv.ParseInt(parts[1], 10, 64)
		var m library.Material
		if e = in.library.Call(ctx, q, &m); e != nil {
			return fail(e)
		}
		b := fmt.Sprintf("资料 %d · %s\n类别：%s\n\n%s\n", m.ID, m.Title, strings.Join(m.Topics, "、"), m.Summary)
		for _, c := range m.Claims {
			b += "\n" + c.Field + "：" + c.Text + "（" + c.Locator + "）\n"
		}
		for _, s := range m.Sources {
			b += "\n来源：" + s.URL + " · " + s.ReadingScope
		}
		if len(b) > 7000 {
			b = shortPreview(b, 2200) + "\n完整内容请在管理页查看。"
		}
		return reply(b)
	case strings.HasPrefix(input, "综述变化") || strings.HasPrefix(input, "综述 ") || strings.HasPrefix(input, "导出综述"):
		prefix := "综述"
		if strings.HasPrefix(input, "综述变化") {
			prefix = "综述变化"
		}
		if strings.HasPrefix(input, "导出综述") {
			prefix = "导出综述"
		}
		q.Action = "review"
		q.Collection = strings.TrimSpace(strings.TrimPrefix(input, prefix))
		var r library.Review
		if e = in.library.Call(ctx, q, &r); e != nil {
			return fail(e)
		}
		b := fmt.Sprintf("%s 综述 v%d\n", r.Topic, r.Version)
		if prefix == "综述变化" {
			b += strings.Join(r.Changes, "\n")
		} else {
			for _, s := range r.Sections {
				b += "\n" + s.Name + "\n" + s.Text + "\n"
			}
		}
		if len(b) > 6500 {
			b = shortPreview(b, 2100) + "\n完整综述请在管理页查看。"
		}
		token, _ := in.files.Grant(owner, msg.Key()+":review")
		b += "\n管理页：" + in.publicURL + "manage/#" + token
		return reply(b)
	case strings.HasPrefix(input, "更新综述"):
		j, e := in.queueLibrary(owner, msg.ContextToken, msg.Key(), "review_update", strings.TrimSpace(strings.TrimPrefix(input, "更新综述")), "更新已有证据的类别综述")
		if e != nil {
			return fail(e)
		}
		return reply(in.libraryAck(j))
	case len(parts) >= 2 && (parts[0] == "移动资料" || parts[0] == "删除资料" || parts[0] == "恢复资料" || parts[0] == "资料标签"):
		q.MaterialID, _ = strconv.ParseInt(parts[1], 10, 64)
		q.Action = map[string]string{"移动资料": "move", "删除资料": "trash", "恢复资料": "restore", "资料标签": "tags"}[parts[0]]
		if q.Action == "move" {
			raw := strings.TrimSpace(strings.Join(parts[2:], " "))
			raw = strings.TrimSpace(strings.TrimPrefix(raw, "到"))
			q.Topics = strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == '，' || r == '、' })
		}
		if q.Action == "tags" {
			q.Tags = strings.FieldsFunc(strings.Join(parts[2:], " "), func(r rune) bool { return r == ',' || r == '，' })
		}
		if e = in.library.Call(ctx, q, nil); e != nil {
			return fail(e)
		}
		j, e := in.queueLibrary(owner, msg.ContextToken, msg.Key(), "review_update", "*", "资料改变后更新全部受影响类别综述")
		if e != nil {
			return fail(e)
		}
		return reply("已保存修改，原类别与新类别的综述都将更新。\n" + in.libraryAck(j))
	}
	return reply("资料库指令：开始收录 / 完成收录 / 查资料 关键词 / 资料 编号 / 综述列表 / 综述 类别 / 研究主题 主题 / 更新综述 类别。")
}
func (in *inbound) queueLibrary(owner, reply, key, kind, target, input string, conversations ...string) (jobs.Job, error) {
	if target == "" {
		target = "*"
	}
	c := in.sessions.Current()
	if len(conversations) > 0 && conversations[0] != "" {
		if selected, ok := in.sessions.Get(conversations[0]); ok {
			c = selected
		}
	}
	return in.queue.EnqueueLibrary(key, input, owner, reply, kind, target, in.preferences.Current(c.ID), c.Profile.BudgetUSD)
}
func (in *inbound) libraryAck(j jobs.Job) string {
	text := "已安排资料研究与综述更新，任务 " + j.ID[:8] + "。所有关联类别都会更新。"
	if in.outputs != nil {
		link, e := in.outputs.TaskLink(in.publicURL, j.Owner, j.ID)
		if e == nil {
			text += "\n进度与结果：" + link
		}
	}
	return text
}
func (in *inbound) syncLibrary(ctx context.Context, local library.Intake, reply string) (jobs.Job, error) {
	var remote library.Intake
	e := in.library.Call(ctx, library.Request{Owner: local.Owner, Action: "begin", Key: "relay:" + local.ID, Conversation: local.Conversation, Collection: local.Collection}, &remote)
	if e != nil {
		return jobs.Job{}, e
	}
	local, e = in.libraryDrafts.Intake(local.Owner, local.ID)
	if e != nil {
		return jobs.Job{}, e
	}
	for _, a := range local.Assets {
		f, e := in.libraryDrafts.Blob(local.Owner, a.SHA256)
		if e != nil {
			return jobs.Job{}, e
		}
		e = in.library.Upload(ctx, local.Owner, remote.ID, a, f)
		f.Close()
		if e != nil {
			return jobs.Job{}, e
		}
	}
	if e = in.library.Call(ctx, library.Request{Owner: local.Owner, Action: "append", ID: remote.ID, Key: "complete", Text: local.Text}, nil); e != nil {
		return jobs.Job{}, e
	}
	if e = in.library.Call(ctx, library.Request{Owner: local.Owner, Action: "state", ID: remote.ID, State: "queued"}, nil); e != nil {
		return jobs.Job{}, e
	}
	j, e := in.queueLibrary(local.Owner, reply, local.ID, "library_intake", remote.ID, "从截图或资料查找文献、整理方法、自动分类，更新所有关联类别综述", local.Conversation)
	if e == nil {
		e = in.libraryDrafts.State(local.Owner, local.ID, "queued")
		if e == nil {
			_ = in.libraryDrafts.RetireStaging(local.Owner, local.ID)
		}
	}
	return j, e
}
func (in *inbound) libraryRetry(ctx context.Context, statePath string) {
	if in.libraryDrafts == nil {
		return
	}
	timer := time.NewTicker(30 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			pending, _ := in.libraryDrafts.Pending(in.owner)
			state, e := weixin.LoadState(statePath)
			if e != nil {
				continue
			}
			for _, v := range pending {
				_, _ = in.syncLibrary(ctx, v, state.Contexts[in.owner])
			}
			active := false
			for _, j := range in.queue.History() {
				if j.Owner == in.owner && j.Kind != "" && (j.Status == "queued" || j.Status == "running") {
					active = true
					break
				}
			}
			if active {
				continue
			}
			var ts []library.Topic
			if in.library.Call(ctx, library.Request{Owner: in.owner, Action: "topics"}, &ts) != nil {
				continue
			}
			pendingTopics := []library.Topic{}
			for _, t := range ts {
				if t.Dirty && t.LastError == "" {
					pendingTopics = append(pendingTopics, t)
				}
			}
			if len(pendingTopics) > 0 {
				b, _ := json.Marshal(pendingTopics)
				h := sha256.Sum256(b)
				_, _ = in.queueLibrary(in.owner, state.Contexts[in.owner], fmt.Sprintf("review-recovery:%x", h[:16]), "review_update", "*", "继续未完成的关联综述更新")
			}
		}
	}
}

// Ensure imported file names cannot address paths outside an intake workspace.
func libraryFilename(name string) string { return filepath.Base(name) }
