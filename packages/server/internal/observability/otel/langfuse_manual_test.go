//go:build manual

// 手动集成测试：把真实 gen_ai span 经真实 OTLP/HTTP 导出到真实 Langfuse。
//
// 运行前先起 Langfuse 栈（deployments/docker-compose.observability.yml），再：
//
//	AUTH=$(printf 'pk-lf-cove-local-0000000000000000:sk-lf-cove-local-0000000000000000' | base64)
//	OTEL_TRACES_ENDPOINT=http://localhost:3000/api/public/otel/v1/traces \
//	OTEL_TRACES_HEADERS="Authorization=Basic ${AUTH}" \
//	OTEL_INSECURE=true \
//	go test ./internal/observability/otel/ -tags manual -run TestExportToLangfuse -v
//
// 之后在 Langfuse UI 或 GET /api/public/traces 应看到一条 gen_ai.agent.run trace。
package otel

import (
	"context"
	"os"
	"testing"

	"github.com/boxify/api-go/internal/config"
	"github.com/boxify/api-go/internal/core/agent/harness"
	corereact "github.com/boxify/api-go/internal/core/agent/react"
	coretool "github.com/boxify/api-go/internal/core/tool"
)

func TestExportToLangfuse(t *testing.T) {
	endpoint := os.Getenv("OTEL_TRACES_ENDPOINT")
	if endpoint == "" {
		t.Skip("设置 OTEL_TRACES_ENDPOINT 后运行")
	}
	cfg := config.OTelConfig{
		Enabled:        true,
		ServiceName:    "cove-api",
		TracesEndpoint: endpoint,
		TracesHeaders:  os.Getenv("OTEL_TRACES_HEADERS"),
		SampleRatio:    1,
		Insecure:       os.Getenv("OTEL_INSECURE") == "true",
	}

	ctx := context.Background()
	providers, err := Setup(ctx, cfg)
	if err != nil {
		t.Fatalf("otel.Setup: %v", err)
	}

	h := harness.New(finalAnswerClient{}, coretool.NewRegistry(), harness.WithTracer(providers.Tracer))
	res, err := h.Run(ctx, corereact.Input{Query: "otel langfuse smoke"})
	if err != nil {
		t.Fatalf("harness run: %v", err)
	}
	t.Logf("run stopped_by=%s answer=%q", res.StoppedBy, res.Answer)

	// Shutdown 触发 batch span processor flush，把 span 真实推送到 Langfuse。
	if err := providers.Shutdown(ctx); err != nil {
		t.Fatalf("flush/shutdown: %v", err)
	}
	t.Log("已导出 gen_ai.agent.run/gen_ai.chat span 到 Langfuse，去 /api/public/traces 核对")
}
