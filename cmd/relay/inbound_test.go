package main

import (
	"bytes"
	"context"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/conversations"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/files"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/jobs"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/models"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/settings"
	"github.com/123456dsasdsad/wechat-go-assistant/weixin"
	"image"
	"image/png"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeMessages struct {
	text     string
	download []byte
}

func TestImageQuestionBindsAnActualImageAndAcknowledgesOriginalQuestion(t *testing.T) {
	defer metadb.CloseAll()
	in := inboundFixture(t)
	var picture bytes.Buffer
	png.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 2, 2)))
	in.client.(*fakeMessages).download = picture.Bytes()
	msg := textMessage("screenshot", "这张图的图例遮挡了哪部分？")
	msg.Items = append(msg.Items, weixin.Item{Type: weixin.ImageType, Image: &weixin.ImageItem{}})
	if e := in.handle(context.Background(), msg); e != nil {
		t.Fatal(e)
	}
	task, _ := in.queue.Claim(time.Now())
	if task == nil || len(task.Attachments) != 1 || !strings.HasSuffix(task.Attachments[0].Name, ".png") || task.Attachments[0].Size != int64(picture.Len()) || task.Input != "这张图的图例遮挡了哪部分？" {
		t.Fatal("screenshot not bound to current question", task)
	}
	if !strings.Contains(in.client.(*fakeMessages).text, "原问题：这张图") {
		t.Fatal("acknowledgement points to wrong question")
	}
}
func TestTrainingStatusQuestionDoesNotWaitBehindTraining(t *testing.T) {
	defer metadb.CloseAll()
	in := inboundFixture(t)
	in.handle(context.Background(), textMessage("train", "运行长期训练"))
	in.queue.Claim(time.Now())
	if e := in.handle(context.Background(), textMessage("status", "跑完长期训练的结果在哪？")); e != nil {
		t.Fatal(e)
	}
	if len(in.queue.History()) != 1 || !strings.Contains(in.client.(*fakeMessages).text, "校园服务器执行中") || !strings.Contains(in.client.(*fakeMessages).text, "原问题：运行长期训练") {
		t.Fatal("status was queued or falsely reported completed")
	}
}

func (f *fakeMessages) SendText(_ context.Context, _ weixin.Reply, text string) (weixin.SendResult, error) {
	f.text = text
	return weixin.SendResult{}, nil
}
func (f *fakeMessages) Download(context.Context, weixin.Item) ([]byte, error) { return f.download, nil }
func inboundFixture(t *testing.T) *inbound {
	root := t.TempDir()
	f, _ := files.Open(filepath.Join(root, "files"))
	q, _ := jobs.Open(filepath.Join(root, "jobs"))
	s, _ := settings.Open(filepath.Join(root, "settings.json"), models.Catalog{Models: []models.Model{{ID: "gpt-6-sol", Efforts: []string{"low", "high"}, DefaultEffort: "high"}}})
	c, _ := conversations.Open(filepath.Join(root, "conversations.json"))
	return &inbound{client: &fakeMessages{download: []byte("real attachment")}, files: f, queue: q, preferences: s, sessions: c, owner: "owner", publicURL: "https://example.com/wechat-files/"}
}
func textMessage(id, input string) weixin.Message {
	return weixin.Message{MessageID: weixin.ID(id), FromUserID: "owner", ContextToken: "private-context", Type: 1, Items: []weixin.Item{{Type: weixin.TextType, Text: &weixin.TextItem{Text: input}}}}
}
func TestDirectFileCreatesRealAttachmentAndReplays(t *testing.T) {
	defer metadb.CloseAll()
	in := inboundFixture(t)
	msg := textMessage("direct", "请读取文件")
	msg.Items = append(msg.Items, weixin.Item{Type: weixin.FileType, File: &weixin.FileItem{Name: "资料.txt"}})
	if err := in.handle(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if err := in.handle(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	task, _ := in.queue.Claim(time.Now())
	if len(task.Attachments) != 1 || task.ConversationID != in.sessions.Current().ID || task.Input != "请读取文件" {
		t.Fatal(task)
	}
	if refs := in.files.List("owner"); len(refs) != 1 {
		t.Fatal("duplicate inbound attachment")
	}
	if topic := in.sessions.Current().FirstTask; !strings.Contains(topic, "资料.txt") {
		t.Fatal("attachment topic not recorded", topic)
	}
}

func TestTextTopicAndRenameAreHandledOutsideAI(t *testing.T) {
	defer metadb.CloseAll()
	in := inboundFixture(t)
	ctx := context.Background()
	if err := in.handle(ctx, textMessage("first-task", "请检查实验数据")); err != nil {
		t.Fatal(err)
	}
	original := in.sessions.Current()
	if original.Title != "检查实验数据" {
		t.Fatal("text task not reflected in session title", original)
	}
	if err := in.handle(ctx, textMessage("rename", "重命名会话 1 实验项目")); err != nil {
		t.Fatal(err)
	}
	if len(in.queue.History()) != 1 {
		t.Fatal("rename sent to model")
	}
	if in.sessions.Current().ID != original.ID || in.sessions.Current().Number != original.Number {
		t.Fatal("rename changed session identity")
	}
	if err := in.handle(ctx, textMessage("list", "会话列表")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(in.client.(*fakeMessages).text, "实验项目 · 检查实验数据") {
		t.Fatal("list does not distinguish task")
	}
}
func TestNumberReplySelectsWithoutAIJob(t *testing.T) {
	defer metadb.CloseAll()
	in := inboundFixture(t)
	for i, input := range []string{"新建会话 论文", "会话列表", "1"} {
		if err := in.handle(context.Background(), textMessage(string(rune('a'+i)), input)); err != nil {
			t.Fatal(err)
		}
	}
	if in.sessions.Current().Number != 1 {
		t.Fatal("numeric selection failed")
	}
	if task, _ := in.queue.Claim(time.Now()); task != nil {
		t.Fatal("settings created AI job")
	}
	if !strings.Contains(in.client.(*fakeMessages).text, "已继续会话：1") {
		t.Fatal("selection acknowledgement missing")
	}
}
