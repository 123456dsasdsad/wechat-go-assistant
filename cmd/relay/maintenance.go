package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/maintenance"
	"github.com/123456dsasdsad/wechat-go-assistant/weixin"
	"time"
)

type maintenanceSender interface {
	SendText(context.Context, weixin.Reply, string) (weixin.SendResult, error)
}

func maintenanceSendError(err error) string {
	if err == nil {
		return ""
	}
	var api *weixin.APIError
	if errors.As(err, &api) {
		return fmt.Sprintf("wechat_ret_%d_errcode_%d", api.Ret, api.Code)
	}
	return "wechat_not_accepted"
}

func deliverMaintenanceReport(ctx context.Context, client maintenanceSender, reports *maintenance.Store, r maintenance.Report, owner string) error {
	var sendErr error
	for i, chunk := range textChunks(r.Text) {
		if i < r.SentChunks {
			continue
		}
		_, sendErr = client.SendText(ctx, weixin.Reply{ToUserID: owner, ClientID: fmt.Sprintf("go-maintenance-%s-%d", r.ID, i), RunID: "maintenance-" + r.ID}, chunk)
		if sendErr != nil {
			break
		}
		if sendErr = reports.CommitChunk(r.ID, i+1); sendErr != nil {
			break
		}
	}
	if e := reports.Receipt(r.ID, sendErr == nil, maintenanceSendError(sendErr), time.Now().UTC()); e != nil {
		return e
	}
	return sendErr
}

func deliverMaintenance(ctx context.Context, client *liveResultSender, reports *maintenance.Store, owner string) {
	for pause(ctx, 15*time.Second) {
		for _, r := range reports.Pending(time.Now()) {
			deliverMaintenanceReport(ctx, client, reports, r, owner)
		}
	}
}
