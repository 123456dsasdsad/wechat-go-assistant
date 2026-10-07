package main

import (
	"context"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/maintenance"
	"github.com/123456dsasdsad/wechat-go-assistant/weixin"
	"time"
)

func deliverMaintenance(ctx context.Context, client *liveResultSender, reports *maintenance.Store, owner string) {
	for pause(ctx, 15*time.Second) {
		for _, r := range reports.Pending(time.Now()) {
			_, e := client.SendText(ctx, weixin.Reply{ToUserID: owner, ClientID: "go-maintenance-" + r.ID, RunID: "maintenance-" + r.ID}, r.Text)
			category := ""
			if e != nil {
				category = "wechat_not_accepted"
			}
			reports.Receipt(r.ID, e == nil, category, time.Now().UTC())
		}
	}
}
