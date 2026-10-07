package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/files"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/jobs"
	"github.com/123456dsasdsad/wechat-go-assistant/weixin"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"strconv"
)

const directMediaBytes = 25 * 1024 * 1024

type resultSender interface {
	SendText(context.Context, weixin.Reply, string) (weixin.SendResult, error)
	Upload(context.Context, string, int, []byte) (weixin.Uploaded, error)
	SendImage(context.Context, weixin.Reply, weixin.Uploaded) (weixin.SendResult, error)
	SendFile(context.Context, weixin.Reply, string, weixin.Uploaded) (weixin.SendResult, error)
}

func deliverJob(ctx context.Context, client resultSender, store *jobs.Store, outputs *files.Store, publicURL string, j jobs.Job, text string) error {
	reply := weixin.Reply{ToUserID: j.Owner, ContextToken: j.ReplyContext, ClientID: "go-result-" + j.ID, RunID: j.ID}
	if !j.PartDelivered("text") {
		var e error
		text, e = store.PrepareDeliveryText(j.ID, text)
		if e != nil {
			return e
		}
		chunks := textChunks(text)
		for i, chunk := range chunks {
			part := "text"
			if len(chunks) > 1 {
				part = "text:" + strconv.Itoa(i)
				reply.ClientID = "go-result-" + j.ID + "-" + part
			}
			if j.PartDelivered(part) {
				continue
			}
			if _, e := client.SendText(ctx, reply, chunk); e != nil {
				return e
			}
			if e := store.CommitPart(j.ID, part); e != nil {
				return e
			}
		}
		if e := store.CommitPart(j.ID, "text"); e != nil {
			return e
		}
	}
	for _, ref := range j.Outputs {
		if j.PartDelivered(ref.ID) {
			continue
		}
		reply.ClientID = "go-output-" + j.ID + "-" + ref.ID
		reply.ContextToken = j.MediaContext()
		if outputs == nil {
			return errors.New("output_store_unavailable")
		}
		if ref.Size > directMediaBytes {
			link, e := outputs.DownloadLink(publicURL, j.Owner, ref)
			if e != nil {
				return e
			}
			if _, e = client.SendText(ctx, reply, fmt.Sprintf("%s\n结果原件：%s（%.1f MiB）\n%s\n下载链接24小时有效。", questionIdentity(j), ref.Name, float64(ref.Size)/(1024*1024), link)); e != nil {
				return e
			}
		} else {
			reader, e := outputs.OpenBlob(ref)
			if e != nil {
				return e
			}
			data, e := io.ReadAll(io.LimitReader(reader, directMediaBytes+1))
			reader.Close()
			if e != nil || int64(len(data)) != ref.Size {
				return errors.New("output_integrity_failed")
			}
			kind := weixin.UploadFile
			_, format, e := image.DecodeConfig(bytes.NewReader(data))
			if e == nil && (format == "png" || format == "jpeg") {
				kind = weixin.UploadImage
			}
			uploaded, e := client.Upload(ctx, j.Owner, kind, data)
			if e != nil {
				return e
			}
			if kind == weixin.UploadImage {
				if captioned, ok := client.(interface {
					SendCaptionedImage(context.Context, weixin.Reply, weixin.Uploaded, string) (weixin.SendResult, error)
				}); ok {
					_, e = captioned.SendCaptionedImage(ctx, reply, uploaded, questionIdentity(j)+"\n下面是这条任务的图片；文件名和全部版本可在对应任务页查看。")
				} else {
					_, e = client.SendImage(ctx, reply, uploaded)
				}
			} else {
				_, e = client.SendFile(ctx, reply, j.ID[:8]+"_"+ref.Name, uploaded)
			}
			if e != nil {
				return e
			}
		}
		if e := store.CommitPart(j.ID, ref.ID); e != nil {
			return e
		}
		fmt.Printf("{\"type\":\"result_attachment_delivered\",\"job\":%q,\"file\":%q,\"bytes\":%d,\"download_link\":%t}\n", j.ID, ref.ID, ref.Size, ref.Size > directMediaBytes)
	}
	return nil
}
func textChunks(text string) []string {
	runes := []rune(text)
	var out []string
	for len(runes) > 2000 {
		out = append(out, string(runes[:2000]))
		runes = runes[2000:]
	}
	if len(runes) > 0 {
		out = append(out, string(runes))
	}
	return out
}
