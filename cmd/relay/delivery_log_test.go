package main

import (
	"errors"
	"github.com/123456dsasdsad/wechat-go-assistant/weixin"
	"strings"
	"testing"
)

func TestDeliveryDiagnosticsRetainCodeAndHideUntrustedErrorText(t *testing.T) {
	api := deliveryRetryLog("job", &weixin.APIError{Operation: "sendMessage", Ret: -1, Code: 123})
	if !strings.Contains(api, `"code":123`) || !strings.Contains(api, `"operation":"sendMessage"`) {
		t.Fatal("API error code omitted")
	}
	secret := deliveryRetryLog("job", errors.New("https://example.invalid/?token=private-token"))
	if strings.Contains(secret, "private-token") || strings.Contains(secret, "example.invalid") {
		t.Fatal("untrusted error text leaked")
	}
	cdn := deliveryRetryLog("job", errors.New("CDN upload returned HTTP 429"))
	if !strings.Contains(cdn, `"http":429`) {
		t.Fatal("CDN status omitted")
	}
}
