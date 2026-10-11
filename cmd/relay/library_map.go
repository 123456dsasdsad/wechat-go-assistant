package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/123456dsasdsad/wechat-go-assistant/internal/jobs"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/library"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/models"
	"github.com/123456dsasdsad/wechat-go-assistant/weixin"
)

func (in *inbound) mapCommand(ctx context.Context, msg weixin.Message, input string) (bool, error) {
	prefix := ""
	for _, p := range []string{"更新知识树", "研究方向", "研究地图", "知识树"} {
		if input == p || strings.HasPrefix(input, p+" ") {
			prefix = p
			break
		}
	}
	if prefix == "" {
		return false, nil
	}
	reply := func(s string) (bool, error) { return true, in.reply(ctx, msg, "map", s) }
	if in.library == nil {
		return reply("资料库尚未启用。")
	}
	topic := strings.TrimSpace(strings.TrimPrefix(input, prefix))
	if prefix == "更新知识树" {
		j, e := in.queueLibrary(msg.FromUserID, msg.ContextToken, msg.Key(), "review_update", topic, "更新知识树和关联综述")
		if e != nil {
			return true, e
		}
		return reply(in.libraryAck(j))
	}
	var view library.KnowledgeMap
	if e := in.library.Call(ctx, library.Request{Owner: msg.FromUserID, Action: "map"}, &view); e != nil {
		return reply("知识树暂时不可用，请稍后重试。")
	}
	var b strings.Builder
	b.WriteString("研究知识树\n")
	for _, status := range view.Branches {
		if topic != "" && status.Topic.Name != topic {
			continue
		}
		fmt.Fprintf(&b, "\n%s · %d 篇 · 知识树 v%d", status.Topic.Name, status.Topic.Count, status.Branch.Version)
		if status.Dirty {
			b.WriteString(" · 待整理")
		}
		if status.LastError != "" {
			b.WriteString(" · 更新未完成，可发“更新知识树 " + status.Topic.Name + "”重试")
		}
		if prefix == "研究方向" {
			for _, n := range status.Branch.Nodes {
				if n.Direction != nil {
					title := n.Title
					if n.Annotation != nil && n.Annotation.Title != "" {
						title = n.Annotation.Title
					}
					fmt.Fprintf(&b, "\n候选：%s\n问题：%s\n最小基线：%s\n否证标准：%s\n", title, n.Direction.Question, n.Direction.Baseline, n.Direction.Falsification)
				}
			}
		}
	}
	if len(view.Branches) == 0 {
		b.WriteString("\n先发送“开始收录”导入研究资料。")
	}
	b.WriteString("\n\n候选方向是待验证假设，库内近邻不代表全球最新或空白。查询知识树不调用AI。")
	token, e := in.files.Grant(msg.FromUserID, msg.Key()+":map")
	if e == nil {
		b.WriteString("\n打开管理页 → 知识树：\n" + in.publicURL + "manage/#" + token)
	}
	text := b.String()
	if len(text) > 7000 {
		text = shortPreview(text, 2000) + "\n完整知识树请在管理页查看。"
		if e == nil {
			text += "\n" + in.publicURL + "manage/#" + token
		}
	}
	return reply(text)
}

func (in *inbound) directionTask(ctx context.Context, owner, reply, source, topic, key string) (jobs.Job, error) {
	var result struct {
		Prompt string `json:"prompt"`
	}
	if e := in.library.Call(ctx, library.Request{Owner: owner, Action: "map_prompt", Collection: topic, Key: key}, &result); e != nil {
		return jobs.Job{}, e
	}
	c, e := in.sessions.CreateBackground("direction:"+source, "研究方向 · "+shortPreview(topic, 22))
	if e != nil {
		return jobs.Job{}, e
	}
	choice := models.Choice{Model: "gpt-6.1-sol", Effort: "high"}
	j, e := in.queue.EnqueuePersonalized("direction:"+source, result.Prompt, owner, reply, choice, nil, c.ID, "", c.Profile)
	if e == nil {
		e = recordJob(in.sessions, j)
	}
	return j, e
}
