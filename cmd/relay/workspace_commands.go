package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/assistant"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/files"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/jobs"
	"github.com/123456dsasdsad/wechat-go-assistant/weixin"
	"strings"
)

func (in *inbound) enqueue(source, input, owner, reply, cid string, refs []files.Ref) (jobs.Job, error) {
	if cid == "" {
		cid = in.sessions.Current().ID
	}
	session, ok := in.sessions.Get(cid)
	if !ok || session.Archived {
		return jobs.Job{}, errors.New("conversation_unavailable")
	}
	if in.watches != nil {
		if _, ok := in.watches.Current(owner, cid); ok {
			return jobs.Job{}, errors.New("这个会话用于查看原 Codex 任务，请切换其他会话创建任务。")
		}
	}
	choice, body, e := in.preferences.ChoiceForTask(input, cid)
	if e != nil {
		return jobs.Job{}, e
	}
	memory := ""
	if in.assistant != nil {
		memory = in.assistant.MemoryText(owner)
	}
	if session.Profile.Notes != "" {
		memory += "\n会话偏好资料：\n" + session.Profile.Notes
	}
	j, e := in.queue.EnqueuePersonalized(source, body, owner, reply, choice, refs, cid, memory, session.Profile)
	if e == nil {
		e = recordJob(in.sessions, j)
	}
	return j, e
}
func (in *inbound) workspaceCommand(ctx context.Context, msg weixin.Message, input string) (bool, error) {
	reply := func(s string) (bool, error) { return true, in.reply(ctx, msg, "workspace", s) }
	switch input {
	case "管理页面", "手机管理", "控制台":
		if in.publicURL == "" {
			return reply("手机管理页尚未配置。")
		}
		token, e := in.files.Grant(msg.FromUserID, msg.Key())
		if e != nil {
			return true, e
		}
		return reply("手机管理页：\n" + in.publicURL + "manage/#" + token + "\n首次打开后可收藏此页，登录保留30天。任务、会话、文件、模板和用量操作不调用AI。")
	case "任务列表", "任务中心":
		rows, e := in.queue.TaskMenu(msg.FromUserID)
		if e != nil {
			return true, e
		}
		var b strings.Builder
		b.WriteString("任务列表（编号10分钟内有效）：\n")
		for i, j := range rows {
			fmt.Fprintf(&b, "\n%d. %s · %s\n%s\n", i+1, j.ID[:8], taskStatusLabel(j), shortPreview(j.Input, 70))
		}
		b.WriteString("\n停止任务 <编号或ID>\n重试任务 <编号或ID>\n补发结果 <编号或ID>\n管理页面")
		return reply(b.String())
	case "模板列表":
		if in.templates == nil {
			return reply("模板尚未配置。")
		}
		var b strings.Builder
		for _, v := range in.templates.List() {
			b.WriteString(v.Name + "\n")
		}
		b.WriteString("运行模板 <名称>：任务要求\n保存模板 <名称>：指令（用{{内容}}插入任务要求）\n删除模板 <名称>")
		return reply(b.String())
	case "会话用量", "任务用量":
		cid := in.sessions.Current().ID
		if input == "任务用量" {
			cid = ""
		}
		u, e := in.queue.Usage(msg.FromUserID, cid)
		if e != nil {
			return true, e
		}
		return reply(fmt.Sprintf("完成任务 %d；有用量记录 %d\n输入 %d；缓存 %d；输出 %d；总token %d\nAPI等值约 $%.4f（非订阅账单）\n缺少记录 %d，未知价格 %d。", u.Tasks, u.Measured, u.Input, u.Cached, u.Output, u.Total, u.USD, u.Tasks-u.Measured, u.Unpriced))
	}
	if strings.HasPrefix(input, "任务用量 ") {
		j, e := in.queue.ResolveTask(msg.FromUserID, strings.TrimSpace(strings.TrimPrefix(input, "任务用量 ")))
		if e != nil {
			return reply("任务编号不存在或已过期，请先发送“任务列表”。")
		}
		if !j.Usage.Available {
			return reply(j.ID[:8] + "\n该任务尚无可用的 token 记录。")
		}
		text := fmt.Sprintf("任务 %s\n输入 %d；缓存 %d；输出 %d；总token %d", j.ID[:8], j.Usage.Input, j.Usage.Cached, j.Usage.Output, j.Usage.Total)
		if cost, ok := jobs.Cost(j); ok {
			text += fmt.Sprintf("\nAPI等值约 $%.4f（非订阅账单）", cost)
		} else {
			text += "\n当前型号价格不可用。"
		}
		return reply(text)
	}
	for _, prefix := range []string{"停止任务 ", "重试任务 ", "补发结果 "} {
		if strings.HasPrefix(input, prefix) {
			j, e := in.queue.ResolveTask(msg.FromUserID, strings.TrimSpace(strings.TrimPrefix(input, prefix)))
			if e != nil {
				return reply("未找到任务，或列表编号过期。请发送“任务列表”后重试。")
			}
			switch prefix {
			case "停止任务 ":
				j, e = in.queue.Cancel(msg.FromUserID, j.ID)
				if e != nil {
					return true, e
				}
				text := "停止请求已保存，校园端确认进程退出后结束。"
				if j.Status != "running" && j.Training.State != "running" {
					text = "任务已停止或已经结束。"
				}
				return reply(j.ID[:8] + "\n" + text)
			case "重试任务 ":
				j, e = in.queue.Retry(msg.FromUserID, j.ID, msg.Key(), msg.ContextToken)
				if e != nil {
					return reply("只有失败或停止的任务可重试。")
				}
				return reply("重试已排队：" + j.ID[:8] + "，继续原会话。")
			default:
				_, e = in.queue.RequestMedia(msg.FromUserID, j.ID, msg.ContextToken)
				if e != nil {
					return reply("该任务尚未完成或没有附件。")
				}
				return reply("已安排补发未接收的附件：" + j.ID[:8] + "。不重新调用AI。")
			}
		}
	}
	for _, prefix := range []string{"搜索会话 ", "归档会话 ", "恢复会话 ", "置顶会话 ", "取消置顶会话 ", "取消置顶 "} {
		if strings.HasPrefix(input, prefix) {
			value := strings.TrimSpace(strings.TrimPrefix(input, prefix))
			if prefix == "搜索会话 " {
				var b strings.Builder
				for _, v := range in.sessions.List(value, false) {
					fmt.Fprintf(&b, "%d. %s\n", v.Number, v.DisplayName())
				}
				if b.Len() == 0 {
					b.WriteString("没有匹配的会话。")
				}
				return reply(b.String())
			}
			v, ok := in.sessions.Resolve(value)
			if !ok {
				return reply("没有找到会话。")
			}
			switch prefix {
			case "归档会话 ":
				v.Archived = true
			case "恢复会话 ":
				v.Archived = false
			case "置顶会话 ":
				v.Pinned = true
			case "取消置顶 ", "取消置顶会话 ":
				v.Pinned = false
			}
			if e := in.sessions.Mark(v.ID, v.Pinned, v.Archived); e != nil {
				return reply("请先切换到其他会话，再归档当前会话。")
			}
			return reply("会话已保存：" + v.Title)
		}
	}
	if in.templates != nil {
		for _, prefix := range []string{"保存模板 ", "删除模板 ", "运行模板 "} {
			if strings.HasPrefix(input, prefix) {
				rest := strings.TrimSpace(strings.TrimPrefix(input, prefix))
				if prefix == "删除模板 " {
					if e := in.templates.Delete(rest); e != nil {
						return true, e
					}
					return reply("已删除模板：" + rest)
				}
				n := strings.IndexAny(rest, ":：")
				if n < 1 {
					return reply("请发送“" + strings.TrimSpace(prefix) + " <名称>：内容”。")
				}
				name := strings.TrimSpace(rest[:n])
				body := strings.TrimSpace(strings.TrimLeft(rest[n:], ":："))
				if prefix == "保存模板 " {
					e := in.templates.Save(templateValue(name, body))
					if e != nil {
						return reply("模板名称或内容无效。")
					}
					return reply("已保存模板：" + name)
				}
				expanded, e := in.templates.Expand(name, body)
				if e != nil {
					return reply("未找到模板或展开后内容过长。")
				}
				j, e := in.enqueue(msg.Key(), expanded, msg.FromUserID, msg.ContextToken, "", nil)
				if e != nil {
					return true, e
				}
				return reply("模板任务已排队：" + j.ID[:8])
			}
		}
	}
	return false, nil
}
func shortPreview(s string, n int) string {
	r := []rune(strings.ReplaceAll(s, "\n", " "))
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return string(r)
}
func templateValue(name, body string) assistant.Template {
	return assistant.Template{Name: name, Body: body}
}
