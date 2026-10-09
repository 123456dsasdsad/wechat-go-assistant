package main

import (
	"context"
	"fmt"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/materials"
	"github.com/123456dsasdsad/wechat-go-assistant/weixin"
	"strconv"
	"strings"
)

func (in *inbound) resolveProject(owner, value string) (materials.Project, error) {
	ps, e := in.materials.Projects(owner)
	if e != nil {
		return materials.Project{}, e
	}
	if n, e := strconv.Atoi(value); e == nil && n >= 1 && n <= len(ps) {
		return ps[n-1], nil
	}
	return in.materials.Project(owner, value)
}
func (in *inbound) projectCommand(ctx context.Context, msg weixin.Message, input string) (bool, error) {
	if in.materials == nil {
		return false, nil
	}
	owner := msg.FromUserID
	reply := func(v string) (bool, error) { return true, in.reply(ctx, msg, "projects", v) }
	if input == "项目列表" || input == "项目" {
		ps, e := in.materials.Projects(owner)
		if e != nil {
			return reply(e.Error())
		}
		var b strings.Builder
		b.WriteString("项目工作区：\n")
		for i, p := range ps {
			fmt.Fprintf(&b, "\n%d. %s · %d 个会话\n", i+1, p.Title, len(p.Conversations))
		}
		b.WriteString("\n创建项目 名称\n加入项目 名称或编号\n项目状态 名称或编号\n管理页面 → 材料与项目\n编号随项目列表排列，请优先使用名称。")
		return reply(b.String())
	}
	if strings.HasPrefix(input, "创建项目 ") {
		title := strings.TrimSpace(strings.TrimPrefix(input, "创建项目 "))
		if existing, e := in.materials.Project(owner, title); e == nil {
			return reply("项目已存在：" + existing.Title + "。发送“加入项目 " + title + "”关联当前会话。")
		}
		p, e := in.materials.SaveProject(materials.Project{Owner: owner, Title: title, Conversations: []string{in.sessions.Current().ID}, Topics: []string{}})
		if e != nil {
			return reply(e.Error())
		}
		return reply("已创建项目“" + p.Title + "”，已关联当前会话。创建不调用 AI。")
	}
	if strings.HasPrefix(input, "加入项目 ") {
		p, e := in.resolveProject(owner, strings.TrimSpace(strings.TrimPrefix(input, "加入项目 ")))
		if e != nil {
			return reply(e.Error())
		}
		cid := in.sessions.Current().ID
		found := false
		for _, v := range p.Conversations {
			if v == cid {
				found = true
			}
		}
		if !found {
			p.Conversations = append(p.Conversations, cid)
		}
		if _, e = in.materials.SaveProject(p); e != nil {
			return reply(e.Error())
		}
		if draft, ok, _ := in.materials.Active(owner); ok {
			if _, e = in.materials.Bind(owner, draft.ID, p.ID); e != nil {
				return reply(e.Error())
			}
		}
		return reply("当前会话已加入项目“" + p.Title + "”。正在收集的材料也会归入该项目。")
	}
	if strings.HasPrefix(input, "项目状态 ") {
		p, e := in.resolveProject(owner, strings.TrimSpace(strings.TrimPrefix(input, "项目状态 ")))
		if e != nil {
			return reply(e.Error())
		}
		return reply(in.projectSummary(owner, p))
	}
	return false, nil
}
func (in *inbound) projectSummary(owner string, p materials.Project) string {
	var b strings.Builder
	fmt.Fprintf(&b, "项目：%s\n", p.Title)
	members := map[string]bool{}
	for _, cid := range p.Conversations {
		members[cid] = true
		if s, ok := in.sessions.Get(cid); ok {
			fmt.Fprintf(&b, "会话 %d · %s\n", s.Number, s.DisplayName())
		}
	}
	packs, _ := in.materials.List(owner)
	for _, v := range packs {
		if v.Project == p.ID {
			fmt.Fprintf(&b, "材料 %s · %s · %s\n", v.ID[:8], v.Title, v.State)
		}
	}
	rows := in.queue.Recent(owner, 100)
	n := 0
	for i := len(rows) - 1; i >= 0; i-- {
		j := rows[i]
		if !members[j.ConversationID] {
			continue
		}
		fmt.Fprintf(&b, "\n任务 %s · %s\n%s\n", j.ID[:8], taskStatusLabel(j), shortPreview(j.Input, 80))
		n++
		if n >= 10 {
			break
		}
	}
	if n == 0 {
		b.WriteString("还没有关联任务。\n")
	}
	b.WriteString("管理页面 → 材料与项目：查看材料原文、文件、执行过程和结果。")
	return b.String()
}
