package maintenance

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestMidnightInFlightUsageAttributedWhenCompleted(t *testing.T) {
	start, _ := time.ParseInLocation("2006-01-02", "2026-10-07", Beijing)
	line := fmt.Sprintf(`{"type":"usage","requestId":"cross-midnight","requestedAtMs":%d,"latencyMs":2000,"model":"gpt-6.1-sol","usage":{"inputTokens":100,"outputTokens":10}}`, start.Add(-time.Second).UnixMilli())
	previous, _ := Summarize(strings.NewReader(line), "2026-10-06")
	current, _ := Summarize(strings.NewReader(line), "2026-10-07")
	if previous.Total != 0 || current.Total != 110 {
		t.Fatal("cross-midnight inference omitted or counted twice")
	}
}
