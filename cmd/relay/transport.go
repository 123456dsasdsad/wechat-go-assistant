package main

import (
	"context"
	"github.com/123456dsasdsad/wechat-go-assistant/weixin"
)

type relayTransport interface {
	resultSender
	messageClient
	Drain(context.Context, *weixin.State, weixin.Handler, weixin.Commit) error
}
