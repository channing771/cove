package config

import "testing"

func TestOTelConfig_Defaults(t *testing.T) {
	cfg := defaultConfig()
	if cfg.Observability.OTel.Enabled {
		t.Fatal("OTel 默认应关闭，避免影响现有部署")
	}
	if cfg.Observability.OTel.ServiceName != "cove-api" {
		t.Fatalf("默认 service name 错误: %q", cfg.Observability.OTel.ServiceName)
	}
	if cfg.Observability.OTel.SampleRatio != 1 {
		t.Fatalf("默认采样率应为 1, got %v", cfg.Observability.OTel.SampleRatio)
	}
}

func TestOTelConfig_EnvOverride(t *testing.T) {
	t.Setenv("OTEL_ENABLED", "true")
	t.Setenv("OTEL_TRACES_ENDPOINT", "http://langfuse:3000/api/public/otel/v1/traces")
	t.Setenv("OTEL_SAMPLE_RATIO", "0.25")
	cfg := defaultConfig()
	applyEnv(&cfg)
	if !cfg.Observability.OTel.Enabled {
		t.Fatal("OTEL_ENABLED 未生效")
	}
	if cfg.Observability.OTel.TracesEndpoint == "" {
		t.Fatal("OTEL_TRACES_ENDPOINT 未生效")
	}
	if cfg.Observability.OTel.SampleRatio != 0.25 {
		t.Fatalf("OTEL_SAMPLE_RATIO 未生效, got %v", cfg.Observability.OTel.SampleRatio)
	}
}
