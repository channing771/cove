package chat

import (
	"testing"
	"time"

	"github.com/boxify/api-go/internal/config"
)

func TestMsDuration(t *testing.T) {
	if msDuration(0) != 0 || msDuration(-5) != 0 {
		t.Fatal("非正数应返回 0")
	}
	if msDuration(200) != 200*time.Millisecond {
		t.Fatalf("200ms 映射错误: %v", msDuration(200))
	}
}

func TestHarnessOptions_NonEmpty(t *testing.T) {
	hc := config.HarnessConfig{Enabled: true, RetryMaxAttempts: 3, RetryBaseMs: 200, TimeoutMs: 1000, MaxToolCalls: 20}
	opts := harnessOptions(hc, nil, 0.7, "you are cove", nil)
	if len(opts) == 0 {
		t.Fatal("harnessOptions 不应为空")
	}
}
