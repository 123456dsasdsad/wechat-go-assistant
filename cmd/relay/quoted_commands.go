package main

import (
	"context"
	"fmt"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/quotes"
	"github.com/123456dsasdsad/wechat-go-assistant/weixin"
	"strings"
)

func (in *inbound) quotedContent(msg weixin.Message) (quotes.Content, bool) {
	if in.quotes == nil {
		return quotes.Content{}, false
	}
	for _, v := range msg.Items {
		if v.Ref == nil {
			continue
		}
		ids := []string{string(v.Ref.ServerID)}
		if v.Ref.Item != nil {
			ids = append(ids, string(v.Ref.Item.MsgID))
			if v.Ref.Item.Text != nil {
				ids = append(ids, quoteTextKey(v.Ref.Item.Text.Text))
			}
		}
		for _, id := range ids {
			if c, ok := in.quotes.Get(in.botID, msg.FromUserID, id); ok && (c.JobID != "" || c.MaterialID != "") {
				return c, true
			}
		}
	}
	return quotes.Content{}, false
}
func (in *inbound) quotedConversation(msg weixin.Message) string {
	c, ok := in.quotedContent(msg)
	if !ok {
		return ""
	}
	if c.JobID != "" {
		if j, exists := in.queue.Snapshot(c.JobID); exists && j.Owner == msg.FromUserID {
			return j.ConversationID
		}
	}
	if c.MaterialID != "" && in.materials != nil {
		if p, e := in.materials.Get(msg.FromUserID, c.MaterialID); e == nil {
			return p.Conversation
		}
	}
	return ""
}
func (in *inbound) quotedOperation(ctx context.Context, msg weixin.Message, input string) (bool, error) {
	c, ok := in.quotedContent(msg)
	if !ok {
		return false, nil
	}
	reply := func(v string) (bool, error) { return true, in.reply(ctx, msg, "quoted-operation", v) }
	if c.MaterialID != "" && strings.HasPrefix(input, "加入会话 ") && in.materials != nil {
		s, ok := in.sessions.Resolve(strings.TrimSpace(strings.TrimPrefix(input, "加入会话 ")))
		if !ok || s.Archived {
			return reply("未找到可用会话。请发送“会话列表”。")
		}
		if e := in.materials.SetConversation(msg.FromUserID, c.MaterialID, s.ID); e != nil {
			return reply("材料尚未转入：" + e.Error())
		}
		return reply(fmt.Sprintf("材料已转入会话 %d · %s。全局会话选择保持原值。", s.Number, s.DisplayName()))
	}
	if c.MaterialID != "" && in.materials != nil {
		for _, prefix := range []string{"处理这份材料：", "处理这份材料:", "总结这个", "行动清单"} {
			if strings.HasPrefix(input, prefix) {
				instruction := strings.TrimSpace(strings.TrimPrefix(input, prefix))
				if instruction == "" {
					instruction = input
				}
				p, e := in.materials.Get(msg.FromUserID, c.MaterialID)
				if e != nil {
					return reply(e.Error())
				}
				j, e := in.submitMaterial(msg.FromUserID, msg.ContextToken, p, instruction)
				if e != nil {
					return reply("材料处理未提交：" + e.Error())
				}
				return reply(in.materialJobAck(j))
			}
		}
	}
	if c.JobID == "" {
		return false, nil
	}
	j, exists := in.queue.Snapshot(c.JobID)
	if !exists || j.Owner != msg.FromUserID {
		return reply("引用任务不存在，请发送“任务列表”。")
	}
	switch input {
	case "停止这个", "停止任务", "停止这条任务":
		if _, e := in.queue.Cancel(msg.FromUserID, j.ID); e != nil {
			return reply(e.Error())
		}
		return reply("已请求停止引用任务 " + j.ID[:8] + "，等待执行端确认。")
	case "补发这个", "补发结果", "重新发送结果":
		if _, e := in.queue.RequestMedia(msg.FromUserID, j.ID, msg.ContextToken); e != nil {
			return reply("该任务尚未完成或没有附件。")
		}
		return reply("已安排补发引用任务 " + j.ID[:8] + " 的缺失附件，不重跑 AI。")
	case "重试这个", "重试任务":
		v, e := in.queue.Retry(msg.FromUserID, j.ID, msg.Key(), msg.ContextToken)
		if e != nil {
			return reply("只有已失败或已停止的任务可以重试。")
		}
		return reply("引用任务已重试：" + v.ID[:8])
	case "继续这个会话", "切换到这个会话":
		_, text, e := in.sessions.Handle(msg.Key(), "继续会话 "+j.ConversationID)
		return true, func() error {
			if e != nil {
				return e
			}
			return in.reply(ctx, msg, "quoted-operation", text)
		}()
	}
	return false, nil
}
