package harness

import (
	"testing"

	"github.com/boxify/api-go/internal/core/llm"
)

func TestChainClient_PreservesToolCallingInterface(t *testing.T) {
	base := toolCallingClient{scriptedClient: &scriptedClient{}}
	wrapped := chainClient(base, func(c llm.Client) llm.Client { return &passthrough{c} })
	if _, ok := wrapped.(llm.ToolCallingClient); !ok {
		t.Fatal("链装饰后应仍实现 ToolCallingClient")
	}
}

func TestChainClient_NoOptionalWhenBaseLacksIt(t *testing.T) {
	wrapped := chainClient(&scriptedClient{}, func(c llm.Client) llm.Client { return &passthrough{c} })
	if _, ok := wrapped.(llm.ToolCallingClient); ok {
		t.Fatal("base 不支持工具调用时不应虚假暴露 ToolCallingClient")
	}
}

func TestChainClient_NoMiddlewareReturnsBase(t *testing.T) {
	base := &scriptedClient{}
	if got := chainClient(base); got != llm.Client(base) {
		t.Fatal("无中间件时应原样返回 base")
	}
}

// passthrough 是恒等中间件包装体，用于验证接口保留。
type passthrough struct{ llm.Client }
