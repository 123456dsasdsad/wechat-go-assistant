package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/123456dsasdsad/wechat-go-assistant/internal/jobs"
	"github.com/123456dsasdsad/wechat-go-assistant/weixin"
)

func userQuestionText(j jobs.Job, b jobs.UserQuestion, index int) string {
	q := b.Request.Questions[index]
	var text strings.Builder
	fmt.Fprintf(&text, "需要你回答 · %s\n任务 %s\n%s\n", jobs.QuestionCode(b, index), j.ID[:8], q.Question)
	for i, o := range q.Options {
		fmt.Fprintf(&text, "%d. %s", i+1, o.Label)
		if o.Description != "" {
			fmt.Fprintf(&text, " — %s", o.Description)
		}
		text.WriteByte('\n')
	}
	fmt.Fprintf(&text, "请引用这条问题，回复选项编号、选项文字或你的内容。\n也可发送：回答 %s 你的答案\n收到回答前会等待，不会自动替你选择。", jobs.QuestionCode(b, index))
	return text.String()
}
func (in *inbound) answerUserQuestion(ctx context.Context, msg weixin.Message, input string, media int) (bool, error) {
	var job jobs.Job
	var batch jobs.UserQuestion
	index := 0
	found := false
	quoted := false
	for _, item := range msg.Items {
		if item.Ref == nil {
			continue
		}
		quoted = true
		id := string(item.Ref.ServerID)
		if id == "" && item.Ref.Item != nil {
			id = string(item.Ref.Item.MsgID)
		}
		j, b, i, ok := in.queue.FindQuotedQuestion(msg.FromUserID, id)
		if !ok && item.Ref.Item != nil && item.Ref.Item.Text != nil {
			j, b, i, ok = in.queue.FindQuotedQuestion(msg.FromUserID, quoteTextKey(item.Ref.Item.Text.Text))
		}
		if !ok {
			continue
		}
		if found && (job.ID != j.ID || batch.ID != b.ID || index != i) {
			return true, in.reply(ctx, msg, "answer", "这条消息引用了不同问题，请分别引用每条问题回答。")
		}
		job, batch, index, found = j, b, i, true
	}
	if !found && !quoted && media == 0 {
		if input == "待回答" || input == "问题列表" {
			var lines []string
			for _, j := range in.queue.Active() {
				if j.Owner != msg.FromUserID || j.VerificationOnly || !j.WaitingForUser() {
					continue
				}
				for _, b := range j.Questions {
					if b.State != "pending" || b.Attempt != j.Attempts {
						continue
					}
					for i, q := range b.Request.Questions {
						if _, ok := b.Answers[q.ID]; !ok {
							lines = append(lines, userQuestionText(j, b, i))
						}
					}
				}
			}
			if len(lines) == 0 {
				lines = append(lines, "当前没有等待回答的问题。")
			}
			return true, in.reply(ctx, msg, "questions", strings.Join(lines, "\n\n"))
		}
		if strings.HasPrefix(input, "回答 ") {
			parts := strings.SplitN(strings.TrimSpace(strings.TrimPrefix(input, "回答 ")), " ", 2)
			if len(parts) != 2 {
				return true, in.reply(ctx, msg, "answer", "请引用需要回答的问题直接回复，或发送“回答 <问题编号> <答案>”。")
			}
			job, batch, index, found = in.queue.FindQuestionCode(msg.FromUserID, parts[0])
			input = strings.TrimSpace(parts[1])
			if !found {
				return true, in.reply(ctx, msg, "answer", "未找到这个问题编号。发送“待回答”查看，或引用原问题回复。")
			}
		}
	}
	if !found {
		return false, nil
	}
	if media > 0 {
		return true, in.reply(ctx, msg, "answer", "这条问题需要文字回答。请引用它回复选项编号或内容；附件可另发普通任务。")
	}
	complete, e := in.queue.AnswerQuestion(msg.FromUserID, job.ID, batch.ID, batch.Request.Questions[index].ID, msg.Key(), input, time.Now())
	if e != nil {
		text := "这条问题已回答、结束或失效，没有修改其他任务。发送“待回答”查看当前问题。"
		if e.Error() == "invalid_option_number" || e.Error() == "invalid_user_answer" {
			text = "答案为空或选项编号无效。请引用原问题，回复列出的编号、选项文字或具体内容。"
		}
		return true, in.reply(ctx, msg, "answer", text)
	}
	text := "已收到这条回答；还有问题待答，任务继续等待。"
	if complete {
		text = "已收到回答，已交回任务 " + job.ID[:8] + " 继续执行。"
	}
	return true, in.reply(ctx, msg, "answer", text)
}

func deliverUserQuestion(ctx context.Context, client messageClient, store *jobs.Store, j jobs.Job, b jobs.UserQuestion, index int) error {
	qid := b.Request.Questions[index].ID
	r, e := client.SendText(ctx, weixin.Reply{ToUserID: j.Owner, ContextToken: j.ReplyContext, ClientID: "go-question-" + b.ID + fmt.Sprintf("-%d", index+1)}, userQuestionText(j, b, index))
	if e != nil {
		return e
	}
	// Some iLink responses accept the text without returning a server ID. Bind
	// the complete accepted prompt so it is not resent and its quote still works.
	for _, id := range []string{string(r.MessageID), quoteTextKey(userQuestionText(j, b, index))} {
		if id != "" {
			if e := store.RecordQuestionMessage(j.ID, b.ID, qid, id); e != nil {
				return e
			}
		}
	}
	return nil
}
func deliverUserQuestions(ctx context.Context, client *liveResultSender, store *jobs.Store) {
	backoff := map[string]deliveryBackoff{}
	for pause(ctx, time.Second) {
		for _, j := range store.Active() {
			if j.VerificationOnly || !j.WaitingForUser() || !time.Now().Before(j.LeaseUntil) {
				continue
			}
			for _, b := range j.Questions {
				if b.State != "pending" || b.Attempt != j.Attempts {
					continue
				}
				for i, q := range b.Request.Questions {
					if _, ok := b.Answers[q.ID]; ok || len(b.Messages[q.ID]) > 0 {
						continue
					}
					code := jobs.QuestionCode(b, i)
					current, _ := client.contextFor(j.Owner)
					if !shouldAttempt(current, backoff[code]) {
						continue
					}
					sendCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
					e := deliverUserQuestion(sendCtx, client, store, j, b, i)
					cancel()
					if e != nil {
						backoff[code] = failedDelivery(current, backoff[code], e)
						fmt.Println(`{"type":"user_question_delivery_retry"}`)
					} else {
						delete(backoff, code)
					}
				}
			}
		}
	}
}

func pendingQuestionsText(j jobs.Job) string {
	if !j.WaitingForUser() {
		return ""
	}
	var parts []string
	for _, b := range j.Questions {
		if b.State != "pending" || b.Attempt != j.Attempts {
			continue
		}
		for i, q := range b.Request.Questions {
			if _, ok := b.Answers[q.ID]; !ok {
				parts = append(parts, userQuestionText(j, b, i))
			}
		}
	}
	return strings.Join(parts, "\n\n")
}
