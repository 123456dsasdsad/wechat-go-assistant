package main

import (
	"bytes"
	"context"
	"errors"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/files"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/jobs"
	"github.com/123456dsasdsad/wechat-go-assistant/weixin"
	"image"
	"image/png"
	"strings"
	"testing"
	"time"
)

type fakeResultSender struct {
	texts, images, files, uploads int
	fail                          bool
}

func (f *fakeResultSender) SendText(context.Context, weixin.Reply, string) (weixin.SendResult, error) {
	f.texts++
	return weixin.SendResult{}, nil
}
func (f *fakeResultSender) Upload(context.Context, string, int, []byte) (weixin.Uploaded, error) {
	f.uploads++
	return weixin.Uploaded{}, nil
}
func (f *fakeResultSender) SendImage(context.Context, weixin.Reply, weixin.Uploaded) (weixin.SendResult, error) {
	if f.fail {
		f.fail = false
		return weixin.SendResult{}, errors.New("temporary error")
	}
	f.images++
	return weixin.SendResult{}, nil
}
func (f *fakeResultSender) SendFile(context.Context, weixin.Reply, string, weixin.Uploaded) (weixin.SendResult, error) {
	f.files++
	return weixin.SendResult{}, nil
}
func TestPartialDeliveryRetryKeepsCommittedParts(t *testing.T) {
	queue, _ := jobs.Open(t.TempDir())
	queue.Enqueue("source", "request", "owner", "reply")
	task, _ := queue.Claim(time.Now())
	outputs, _ := files.Open(t.TempDir())
	var picture bytes.Buffer
	png.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 2, 2)))
	img, _ := outputs.Save("owner", "png", "figure.png", &picture)
	file, _ := outputs.Save("owner", "zip", "result.zip", strings.NewReader("result"))
	queue.Complete(jobs.Completion{ID: task.ID, Lease: task.Lease, Result: "actual output", Outputs: []files.Ref{img, file}}, time.Now())
	sender := &fakeResultSender{fail: true}
	ctx := context.Background()
	if e := deliverJob(ctx, sender, queue, outputs, "https://example.com/wechat-files/", queue.Ready()[0], "reply"); e == nil {
		t.Fatal("failure ignored")
	}
	if e := deliverJob(ctx, sender, queue, outputs, "https://example.com/wechat-files/", queue.Ready()[0], "reply"); e != nil {
		t.Fatal(e)
	}
	if sender.texts != 1 || sender.images != 1 || sender.files != 1 {
		t.Fatal(sender)
	}
	if e := deliverJob(ctx, sender, queue, outputs, "https://example.com/wechat-files/", queue.Ready()[0], "reply"); e != nil || sender.uploads != 3 {
		t.Fatal("completed part resent", sender, e)
	}
}
