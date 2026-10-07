package main

import (
	"bytes"
	"context"
	"fmt"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/accountupload"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/conversations"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/files"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/jobs"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/maintenance"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/quotes"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/settings"
	"github.com/123456dsasdsad/wechat-go-assistant/weixin"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"regexp"
	"strconv"
	"strings"
)

type messageClient interface {
	SendText(context.Context, weixin.Reply, string) (weixin.SendResult, error)
	Download(context.Context, weixin.Item) ([]byte, error)
}
type inbound struct {
	client           messageClient
	preferences      *settings.Store
	queue            *jobs.Store
	files            *files.Store
	outputs          *files.Store
	publicURL, owner string
	sessions         *conversations.Store
	reports          *maintenance.Store
	accounts         *accountupload.Store
	quotes           *quotes.Store
	botID            string
}

var statusQuestion = regexp.MustCompile(`^(?:现在)?(?:任务|训练|长期训练|跑完长期训练)(?:进度|的结果在哪|完成了吗|跑完了吗|进行到哪了|怎么样了)$`)

func (in *inbound) reply(ctx context.Context, msg weixin.Message, kind, text string) error {
	_, err := in.client.SendText(ctx, weixin.Reply{ToUserID: msg.FromUserID, ContextToken: msg.ContextToken, ClientID: "go-" + kind + "-" + safeID(msg.Key())}, text)
	return err
}
func (in *inbound) handle(ctx context.Context, msg weixin.Message) error {
	if msg.FromUserID != in.owner || msg.GroupID != "" || msg.Type != 1 {
		return nil
	}
	if pauser, ok := in.client.(interface{ PauseMedia(string) error }); ok {
		if e := pauser.PauseMedia(msg.FromUserID); e != nil {
			return e
		}
	} else if e := in.queue.PauseOwnerMedia(msg.FromUserID); e != nil {
		return e
	}
	var texts []string
	var media []weixin.Item
	unsupported := false
	for _, item := range msg.Items {
		switch {
		case item.Type == weixin.TextType && item.Text != nil:
			texts = append(texts, item.Text.Text)
		case item.Type == weixin.FileType && item.File != nil:
			media = append(media, item)
		case item.Type == weixin.ImageType && item.Image != nil:
			media = append(media, item)
		default:
			unsupported = true
		}
	}
	input := strings.TrimSpace(strings.Join(texts, "\n"))
	in.rememberIncoming(msg, nil)
	if len(input) > 8192 || len(media) > 4 {
		return in.reply(ctx, msg, "input", "文字请控制在 8 KiB 内，每个任务最多 4 个文件。")
	}
	if unsupported {
		return in.reply(ctx, msg, "input", "这类消息暂不能直接读取。合并聊天请发送“转发内容”，打开链接粘贴聊天；压缩包请发送“上传文件”获取手机上传链接。")
	}
	material, refs, quoted, quoteErr := in.quoteMaterial(ctx, msg)
	if quoteErr != nil {
		return in.reply(ctx, msg, "quote", quoteErr.Error())
	}
	if len(refs)+len(media) > 4 {
		return in.reply(ctx, msg, "quote", "本条消息与引用资料合计最多 4 个文件，请分次发送。")
	}
	if len(media) == 0 && !quoted {
		accountCommand := accountUploadCommand(input)
		if accountCommand == "upload" {
			if in.accounts == nil || in.publicURL == "" {
				return in.reply(ctx, msg, "accounts", "账号上传入口尚未配置。")
			}
			token, e := in.accounts.Grant(msg.FromUserID, msg.Key())
			if e != nil {
				return in.reply(ctx, msg, "accounts", "账号上传链接暂时不能生成，请稍后重试。")
			}
			return in.reply(ctx, msg, "accounts", "打开链接上传 Codex 账号 JSON（有效期 30 分钟）：\n"+in.publicURL+"accounts/#"+token+"\n已有账号更新，新账号添加。AI 任务执行中会先保存，任务结束后自动生效。无需 AI，不消耗模型 token。")
		}
		if accountCommand == "status" {
			if in.accounts == nil {
				return in.reply(ctx, msg, "accounts", "账号上传入口尚未配置。")
			}
			rows := in.accounts.Recent(msg.FromUserID)
			var lines []string
			for _, r := range rows {
				lines = append(lines, r.Text())
			}
			if len(lines) == 0 {
				lines = append(lines, "还没有账号上传记录。发送“上传账号”获取专用链接。")
			}
			return in.reply(ctx, msg, "accounts", strings.Join(lines, "\n"))
		}
		if in.reports != nil {
			kind, handled := "", true
			switch input {
			case "用量日报", "token用量", "Token用量":
				kind = "usage"
			case "账号状态", "失效账号":
				kind = "accounts"
			case "更新状态":
				kind = "updates"
			case "运维日报":
			default:
				handled = false
			}
			if handled {
				return in.reply(ctx, msg, "maintenance", in.reports.Latest(kind))
			}
		}
		if statusQuestion.MatchString(strings.Trim(input, " \n。？?!！")) {
			return in.reply(ctx, msg, "status", taskStatusText(in.queue, in.outputs, in.publicURL, msg.FromUserID))
		}
		if strings.HasPrefix(input, "回传任务 ") {
			j, e := in.queue.RequestMedia(msg.FromUserID, strings.TrimSpace(strings.TrimPrefix(input, "回传任务 ")), msg.ContextToken)
			if e != nil {
				return in.reply(ctx, msg, "media", "请发送“回传任务 <8位任务编号>”。该任务需要已完成并含有附件，可发送“任务状态”查看。")
			}
			text := questionIdentity(j) + "\n已安排回传这条任务尚未发送的附件；已接收的附件不会重复发送。发送失败或收到新消息时暂停，可再次明确回传；普通消息不会自动补发旧图。"
			if !hasUnsentOutputs(j) {
				text = questionIdentity(j) + "\n这条任务的附件已全部回传，可在任务页查看原件。"
			}
			if in.outputs != nil {
				if link, e := in.outputs.TaskLink(in.publicURL, j.Owner, j.ID); e == nil {
					text += "\n" + link
				}
			}
			return in.reply(ctx, msg, "media", text)
		}
		handled, text, err := in.sessions.Handle(msg.Key(), input)
		if err != nil {
			return err
		}
		if handled {
			return in.reply(ctx, msg, "conversation", text)
		}
		handled, text, err = in.preferences.Handle(msg.Key(), input)
		if err != nil {
			return err
		}
		if handled {
			err = in.reply(ctx, msg, "settings", text)
			if err == nil {
				choice := in.preferences.Current()
				fmt.Printf("{\"type\":\"settings_replied\",\"model\":%q,\"effort\":%q}\n", choice.Model, choice.Effort)
			}
			return err
		}
		switch input {
		case "转发内容", "转发消息", "聊天转发", "粘贴聊天":
			if in.publicURL == "" {
				return in.reply(ctx, msg, "files", "聊天内容入口尚未配置。")
			}
			token, e := in.files.Grant(msg.FromUserID, msg.Key())
			if e != nil {
				return in.reply(ctx, msg, "files", "链接暂时不能生成，请稍后重试。")
			}
			return in.reply(ctx, msg, "files", "打开链接，在“粘贴聊天内容”中粘贴别人发来的文字，再填写处理要求：\n"+in.publicURL+"#"+token+"\n引用资料、聊天记录或压缩包也可保存为文件后上传。入口不调用 AI；点击发送分析后才创建任务。")
		case "任务状态", "任务列表", "查看任务":
			return in.reply(ctx, msg, "status", taskStatusText(in.queue, in.outputs, in.publicURL, msg.FromUserID))
		case "上传文件", "文件上传":
			if in.publicURL == "" {
				return in.reply(ctx, msg, "files", "手机上传入口尚未配置。")
			}
			token, err := in.files.Grant(msg.FromUserID, msg.Key())
			if err != nil {
				return in.reply(ctx, msg, "files", "上传链接暂时不能生成，请使用最近的有效链接或稍后重试。")
			}
			return in.reply(ctx, msg, "files", "打开这个链接选择文件（有效期 30 分钟）：\n"+in.publicURL+"#"+token+"\n上传后发送“分析最新文件：你的要求”，或使用页面上的文件指令。网页上传没有固定文件大小上限，压缩包请使用 ZIP。")
		case "文件列表", "查看文件":
			refs := in.files.List(msg.FromUserID)
			if len(refs) == 0 {
				return in.reply(ctx, msg, "files", "还没有收到文件。发送“上传文件”获取手机上传入口。")
			}
			var b strings.Builder
			b.WriteString("最近文件（保留 7 天）：\n")
			for i, ref := range refs {
				fmt.Fprintf(&b, "%d. %s · %d KiB\nID：%s\n", i+1, ref.Name, (ref.Size+1023)/1024, ref.ID)
			}
			b.WriteString("发送“分析最新文件：你的要求”，或“分析文件 <ID>：你的要求”。")
			return in.reply(ctx, msg, "files", b.String())
		case "文件帮助":
			return in.reply(ctx, msg, "files", "上传文件\n文件列表\n分析最新文件：你的要求\n分析文件 <ID>：你的要求\n可以直接发送文件。支持 ZIP 解压；RAR、7z 和带密码的 ZIP 请先转换。")
		}
	}
	if quoted {
		if input == "" {
			input = "请阅读引用资料，概述内容并指出可以继续分析的事项。"
		}
		var e error
		input, refs, e = in.bindQuote(msg, input, material, refs)
		if e != nil {
			return in.reply(ctx, msg, "quote", e.Error())
		}
		if len(refs)+len(media) > 4 {
			return in.reply(ctx, msg, "quote", "长引用需要一个文本附件。本条消息与引用资料合计最多 4 个文件，请减少附件后重发。")
		}
	}
	if body, ok := supplementText(input); ok && len(media) == 0 && len(refs) == 0 {
		if body == "" {
			return in.reply(ctx, msg, "steer", "请发送“补充：你的追加要求”。")
		}
		j, v, active, e := in.queue.SupplementMessage(msg.Key(), body, msg.FromUserID, msg.ContextToken, in.sessions.Current().ID, in.preferences.Current())
		if e != nil {
			return in.reply(ctx, msg, "steer", "补充未保存，请稍后重试或发送普通消息排队。")
		}
		text := questionIdentity(j) + "\n当前会话没有正在执行的任务，已将这条要求排队。"
		if active {
			text = questionIdentity(j) + "\n补充已保存：" + supplementLabel(v.State) + "。可在任务页查看送达状态。"
		}
		if in.outputs != nil && in.publicURL != "" {
			if link, e := in.outputs.TaskLink(in.publicURL, j.Owner, j.ID); e == nil {
				text += "\n" + link
			}
		}
		return in.reply(ctx, msg, "steer", text)
	}
	directStart := len(refs)
	for i, item := range media {
		data, err := in.client.Download(ctx, item)
		if err != nil {
			fmt.Println(`{"type":"file_download_failed"}`)
			return in.reply(ctx, msg, "files", "微信文件下载失败。大文件请发送“上传文件”，改用网页上传入口。")
		}
		name := ""
		if item.Type == weixin.ImageType {
			_, format, e := image.DecodeConfig(bytes.NewReader(data))
			if e != nil || (format != "png" && format != "jpeg") {
				return in.reply(ctx, msg, "files", "图片格式无法读取，请发送 PNG/JPEG 图片，或通过“上传文件”上传。")
			}
			name = fmt.Sprintf("微信图片-%d.%s", i+1, format)
		} else {
			name = item.File.Name
		}
		ref, err := in.files.Save(msg.FromUserID, msg.Key()+":file:"+strconv.Itoa(i), name, bytes.NewReader(data))
		if err != nil {
			return in.reply(ctx, msg, "files", "文件保存失败。请检查文件名、大小，或稍后重试。")
		}
		refs = append(refs, ref)
	}
	in.rememberIncoming(msg, refs[directStart:])
	if input == "" && len(refs) > 0 {
		input = "请读取收到的文件，概述主要内容，并列出可以继续分析的事项。"
	}
	if input == "" {
		return in.reply(ctx, msg, "input", "请发送文字任务，或发送“上传文件”获取文件入口。")
	}
	choice, body, err := in.preferences.ChoiceForTask(input)
	if err != nil {
		return in.reply(ctx, msg, "input", "临时型号无效或格式不正确。发送“模型列表”查看可选项。")
	}
	if len(refs) == 0 && !quoted {
		var handled bool
		refs, body, handled, err = in.fileTask(msg.FromUserID, body)
		if handled && err != nil {
			return in.reply(ctx, msg, "files", "未找到所选文件。发送“文件列表”查看文件，或发送“上传文件”重新上传。")
		}
	}
	session := in.sessions.Current()
	job, err := in.queue.EnqueueConversation(msg.Key(), body, msg.FromUserID, msg.ContextToken, choice, refs, session.ID)
	if err != nil {
		return err
	}
	if err = recordJob(in.sessions, job); err != nil {
		return err
	}
	fmt.Printf("{\"type\":\"job_queued\",\"id\":%q,\"model\":%q,\"effort\":%q,\"files\":%d}\n", job.ID, job.Model, job.Effort, len(job.Attachments))
	before := 0
	for _, other := range in.queue.History() {
		if other.ID != job.ID && other.Owner == job.Owner && (other.Status == "running" || other.Status == "queued") && other.Created.Before(job.Created) {
			before++
		}
	}
	text := questionIdentity(job) + fmt.Sprintf("\n已收到，任务已排队，前面有 %d 个任务。模型 %s，推理 %s。", before, job.Model, job.Effort)
	if _, ok := supplementText(input); ok && len(refs) > 0 {
		text += "\n带文件的补充作为下一轮任务执行。"
	}
	if selected, ok := in.sessions.Get(job.ConversationID); ok {
		text = fmt.Sprintf("会话 %d · %s\n", selected.Number, selected.DisplayName()) + text
	}
	if len(job.Attachments) > 0 {
		text += fmt.Sprintf("\n已绑定 %d 个文件；ZIP 将在任务目录解压后分析。", len(job.Attachments))
	}
	if in.outputs != nil && in.publicURL != "" {
		if link, e := in.outputs.TaskLink(in.publicURL, job.Owner, job.ID); e == nil {
			text += "\n打开此页可自动查看进度和结果：\n" + link
		}
	}
	return in.reply(ctx, msg, "ack", text)
}
func (in *inbound) fileTask(owner, input string) ([]files.Ref, string, bool, error) {
	for _, prefix := range []string{"分析最新文件", "分析文件"} {
		if !strings.HasPrefix(input, prefix) {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(input, prefix))
		colon := strings.IndexAny(rest, ":：")
		id := ""
		body := ""
		if colon >= 0 {
			id = strings.TrimSpace(rest[:colon])
			body = strings.TrimSpace(strings.TrimLeft(rest[colon:], ":："))
		} else {
			id = rest
		}
		if body == "" {
			body = "请读取所选文件并概述主要内容。"
		}
		if prefix == "分析最新文件" {
			if id != "" {
				return nil, "", true, fmt.Errorf("invalid_file_command")
			}
			refs := in.files.List(owner)
			if len(refs) == 0 {
				return nil, "", true, fmt.Errorf("file_not_found")
			}
			return refs[:1], body, true, nil
		}
		ref, err := in.files.Get(owner, id)
		if err != nil {
			return nil, "", true, err
		}
		return []files.Ref{ref}, body, true, nil
	}
	return nil, input, false, nil
}
