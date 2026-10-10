package main

import (
	"fmt"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/conversations"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/files"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/jobs"
	"strings"
	"time"
)

var replyZone = time.FixedZone("Asia/Shanghai", 8*3600)

func questionIdentity(j jobs.Job) string {
	return fmt.Sprintf("任务 %s · 提问 %s\n原问题：%s", j.ID[:8], j.Created.In(replyZone).Format("01-02 15:04"), conversations.QuestionPreview(j.Input)) + supplementSummary(j)
}
func resultText(j jobs.Job, sessions *conversations.Store, outputs *files.Store, origin string) string {
	sessionHeader := ""
	if sessions != nil {
		if session, ok := sessions.Get(j.ConversationID); ok {
			sessionHeader = fmt.Sprintf("会话 %d · %s\n", session.Number, session.DisplayName())
		}
	}
	text := questionIdentity(j) + "\n" + j.Model + " / " + j.Effort + "\n\n答复：\n" + j.Result
	if j.Error != "" {
		text = questionIdentity(j) + "\n" + failureText(j)
	}
	if outputs != nil && origin != "" {
		if link, e := outputs.TaskLink(origin, j.Owner, j.ID); e == nil {
			// Put the complete result entry before the answer, inside one short reply.
			identity := []rune(questionIdentity(j))
			if len(identity) > 400 {
				identity = append(identity[:400], []rune("…（补充状态见任务页）")...)
			}
			header := string(identity) + fmt.Sprintf("\n%s / %s\n完整答复与全部 %d 个原件：\n%s\n", j.Model, j.Effort, len(j.Outputs), link)
			if j.MediaPackage.ID != "" {
				header += "本任务自动打包交付，任务页可一键下载全部结果。\n"
				if packageLink, e := outputs.DownloadLink(origin, j.Owner, j.MediaPackage); e == nil {
					header += "完整结果包：\n" + packageLink + "\n"
				}
			}
			answer := j.Result
			if j.Error != "" {
				answer = failureText(j)
			}
			runes := []rune(answer)
			if len(runes) > 800 {
				answer = string(runes[:800]) + "\n（完整答复见上方任务页）"
			}
			text = header + "\n答复：\n" + answer
		}
	}
	if j.OutputPending {
		text += "\n附件正在独立回传，失败后自动重试；任务页会自动更新。"
	}
	if j.BudgetReached {
		text += fmt.Sprintf("\n预算提醒：本会话API等值用量达到设置的 $%.2f。", j.BudgetUSD)
	}
	if j.Usage.Available {
		cost, priced := jobs.Cost(j)
		text += fmt.Sprintf("\n本任务 %d tokens", j.Usage.Total)
		if priced {
			text += fmt.Sprintf("，API等值约 $%.4f（非订阅账单）", cost)
		}
	}
	return sessionHeader + text
}
func failureText(j jobs.Job) string {
	if j.Error == "codex_server_overloaded" {
		return "上游模型暂时过载，本次任务未完成。请稍后发送“重试任务 " + j.ID[:8] + "”继续原会话。"
	}
	text := "处理失败（" + j.Error + "）。"
	if hint := fileErrorHint(j.Error); hint != "" {
		text += "\n" + hint
	}
	return text
}
func pendingMedia(j jobs.Job) bool {
	if j.OutputPending || j.Status != "done" || j.MediaDeferred || !j.PartDelivered("text") {
		return false
	}
	return hasUnsentOutputs(j)
}
func hasUnsentOutputs(j jobs.Job) bool {
	if j.PackageDelivered() {
		return false
	}
	for _, ref := range j.Outputs {
		if !j.PartDelivered(ref.ID) {
			return true
		}
	}
	return false
}
func mediaSuperseded(j jobs.Job, history []jobs.Job) bool {
	if j.MediaRequested || j.ConversationID == "" {
		return false
	}
	for _, newer := range history {
		if newer.Owner == j.Owner && newer.ConversationID == j.ConversationID && newer.Created.After(j.Created) && len(newer.Outputs) > 0 && (newer.Status == "done" || newer.Status == "delivered") {
			return true
		}
	}
	return false
}

// Keep one uninterrupted attachment batch per owner. Older revisions remain
// visible on their own result pages instead of interleaving with newer figures.
func selectMediaJobs(history []jobs.Job) []jobs.Job {
	chosen := map[string]bool{}
	focus := focusedMediaJobs(history)
	for _, j := range history {
		if !j.VerificationOnly && j.Status == "done" && !j.PartDelivered("text") && focus[j.Owner] == "" {
			chosen[j.Owner] = true
		}
	}
	var out []jobs.Job
	for _, j := range history {
		if id := focus[j.Owner]; id != "" && id != j.ID {
			continue
		}
		if !j.VerificationOnly && !chosen[j.Owner] && pendingMedia(j) && !mediaSuperseded(j, history) {
			out = append(out, j)
			chosen[j.Owner] = true
		}
	}
	return out
}
func focusedMediaJobs(history []jobs.Job) map[string]string {
	out := map[string]string{}
	for _, j := range history {
		if !j.VerificationOnly && !j.MediaDeferred && j.MediaRequested && j.Status == "done" && (!j.PartDelivered("text") || hasUnsentOutputs(j)) {
			out[j.Owner] = j.ID
		}
	}
	return out
}
func outboundJobID(clientID string) string {
	for _, prefix := range []string{"go-result-", "go-output-"} {
		if strings.HasPrefix(clientID, prefix) && len(clientID) >= len(prefix)+24 {
			return clientID[len(prefix) : len(prefix)+24]
		}
	}
	return ""
}
