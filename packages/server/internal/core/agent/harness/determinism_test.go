package harness

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/boxify/api-go/internal/core/llm"
)

func TestCassette_SaveLoadFind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.json")
	c := &Cassette{}
	fp := fingerprint([]*llm.Message{llm.UserMessage("hi")})
	c.Append(fp, &llm.LLMResult{Text: "hello"})
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadCassette(path)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := loaded.Find(fp)
	if !ok || got.Text != "hello" {
		t.Fatalf("ok=%v got=%v", ok, got)
	}
}

func TestFingerprint_Deterministic(t *testing.T) {
	m := []*llm.Message{llm.UserMessage("hi")}
	if fingerprint(m) != fingerprint(m) {
		t.Fatal("指纹应确定性")
	}
	if fingerprint(m) == fingerprint([]*llm.Message{llm.UserMessage("bye")}) {
		t.Fatal("不同消息指纹应不同")
	}
}

func TestReplayClient_ReturnsRecorded(t *testing.T) {
	c := &Cassette{}
	msgs := []*llm.Message{llm.UserMessage("hi")}
	c.Append(fingerprint(msgs), &llm.LLMResult{Text: "recorded"})
	base := &scriptedClient{invokeResult: func(context.Context) (*llm.LLMResult, error) {
		t.Fatal("replay 命中时不应调用底层")
		return nil, nil
	}}
	client := chainClient(base, withReplay(c, true))
	res, err := client.InvokeResult(context.Background(), msgs)
	if err != nil || res.Text != "recorded" {
		t.Fatalf("err=%v res=%v", err, res)
	}
}

func TestReplayClient_StrictMiss(t *testing.T) {
	base := &scriptedClient{}
	client := chainClient(base, withReplay(&Cassette{}, true))
	if _, err := client.InvokeResult(context.Background(), []*llm.Message{llm.UserMessage("x")}); err != ErrReplayMiss {
		t.Fatalf("严格模式未命中应返回 ErrReplayMiss, got %v", err)
	}
}

func TestRecordClient_AppendsInteraction(t *testing.T) {
	c := &Cassette{}
	base := &scriptedClient{invokeResult: func(context.Context) (*llm.LLMResult, error) {
		return &llm.LLMResult{Text: "live"}, nil
	}}
	client := chainClient(base, withRecord(c))
	_, _ = client.InvokeResult(context.Background(), []*llm.Message{llm.UserMessage("hi")})
	if len(c.Interactions) != 1 {
		t.Fatalf("应录制 1 条, got %d", len(c.Interactions))
	}
}
