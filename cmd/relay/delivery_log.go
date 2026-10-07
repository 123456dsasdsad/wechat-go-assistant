package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/123456dsasdsad/wechat-go-assistant/weixin"
	"strings"
)

// Keep retry diagnostics useful without recording signed URLs or message content.
func deliveryRetryLog(id string, err error) string {
	entry := struct {
		Type      string `json:"type"`
		ID        string `json:"id"`
		Category  string `json:"category"`
		Operation string `json:"operation,omitempty"`
		Ret       int    `json:"ret,omitempty"`
		Code      int    `json:"code,omitempty"`
		HTTP      int    `json:"http,omitempty"`
	}{Type: "reply_retry", ID: id, Category: "delivery_failed"}
	var api *weixin.APIError
	switch {
	case errors.As(err, &api):
		entry.Category, entry.Operation, entry.Ret, entry.Code = "weixin_api", api.Operation, api.Ret, api.Code
	case errors.Is(err, context.DeadlineExceeded):
		entry.Category = "timeout"
	case errors.Is(err, context.Canceled):
		entry.Category = "canceled"
	default:
		if message := err.Error(); strings.HasPrefix(message, "CDN upload returned HTTP ") {
			var status int
			if _, e := fmt.Sscanf(message, "CDN upload returned HTTP %d", &status); e == nil && status >= 100 && status <= 599 {
				entry.Category, entry.HTTP = "cdn_upload_http", status
			}
		}
	}
	data, _ := json.Marshal(entry)
	return string(data)
}
