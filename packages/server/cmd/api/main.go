package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/boxify/api-go/internal/config"
	otelobs "github.com/boxify/api-go/internal/observability/otel"
	"github.com/boxify/api-go/internal/observability/xlog"
	"github.com/boxify/api-go/internal/svc"
	httptransport "github.com/boxify/api-go/internal/transport/http"
)

func main() {
	cfg := config.Load()
	xlog.Configure(xlog.Config{
		Env:   cfg.App.Env,
		Level: slog.LevelInfo,
		Color: true,
	})

	ctx := context.Background()

	// 装配 LLM 可观测（OTel/OTLP）。失败不阻断启动，退化为 noop。
	providers, err := otelobs.Setup(ctx, cfg.Observability.OTel)
	if err != nil {
		slog.Warn("初始化 OTel 可观测失败，退化为无遥测", "错误", err)
		providers = nil
	}
	defer func() {
		if providers == nil {
			return
		}
		if err := providers.Shutdown(ctx); err != nil {
			slog.Error("关闭 OTel 可观测失败", "错误", err)
		}
	}()

	svcCtx, err := svc.New(ctx, cfg)
	if err != nil {
		slog.Error("初始化服务上下文失败", "错误", err)
		os.Exit(1)
	}
	if providers != nil {
		svcCtx.SetObservability(providers.Metrics, providers.Tracer)
	}

	defer func() {
		if err := svcCtx.Close(ctx); err != nil {
			slog.Error("关闭服务上下文失败", "错误", err)
		}
	}()

	router := httptransport.NewRouter(httptransport.Dependencies{
		Svc: svcCtx,
	})

	server := &http.Server{
		Addr:              cfg.HTTPAddr(),
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}
	slog.Info("API 服务启动中", "地址", cfg.HTTPAddr())
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("API 服务异常停止", "错误", err)
		os.Exit(1)
	}
}
