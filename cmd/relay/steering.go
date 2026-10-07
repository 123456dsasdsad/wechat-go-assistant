package main

import (
	"github.com/123456dsasdsad/wechat-go-assistant/internal/conversations"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/jobs"
	"strconv"
	"strings"
)

func supplementText(s string) (string, bool) {
	for _, p := range []string{"补充：", "补充:"} {
		if strings.HasPrefix(s, p) {
			return strings.TrimSpace(strings.TrimPrefix(s, p)), true
		}
	}
	return "", false
}
func supplementLabel(state string) string {
	switch state {
	case "pending":
		return "等待送入当前任务"
	case "dispatching":
		return "正在送入当前任务"
	case "accepted":
		return "已送入当前任务"
	case "queued":
		return "已转为后续排队任务"
	default:
		return "送达状态待确认，请勿重复提交；可查询任务结果"
	}
}
func supplementSummary(j jobs.Job) string {
	var b strings.Builder
	for i, v := range j.Supplements {
		b.WriteString("\n补充 ")
		b.WriteString(strconv.Itoa(i + 1))
		b.WriteString("：")
		b.WriteString(conversations.QuestionPreview(v.Input))
		b.WriteString(" · ")
		b.WriteString(supplementLabel(v.State))
		if v.FollowupID != "" {
			b.WriteString(" · 后续任务 " + v.FollowupID[:8])
		}
	}
	return b.String()
}
