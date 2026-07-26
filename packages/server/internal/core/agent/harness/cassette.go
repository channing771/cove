package harness

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"

	"github.com/boxify/api-go/internal/core/llm"
)

// interaction 是磁带中的一条录制记录：请求指纹 + 模型结果。
type interaction struct {
	Fingerprint string         `json:"fingerprint"`
	Result      *llm.LLMResult `json:"result"`
}

// Cassette 保存一组录制的模型交互，供确定性回放。
type Cassette struct {
	Interactions []interaction `json:"interactions"`
}

// fingerprint 由消息列表计算确定性指纹（SHA256 hex）。
//
// 只纳入消息（role/content/tool_calls），不纳入 ModelCallOption：选项难以稳定序列化，
// 且回放场景下同一组消息对应同一结果已足够稳健。
func fingerprint(messages []*llm.Message) string {
	data, err := json.Marshal(messages)
	if err != nil {
		// 消息不可序列化时退化为空指纹；调用方将命中失败而非产生错误结果。
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Append 追加一条交互。
func (c *Cassette) Append(fp string, r *llm.LLMResult) {
	c.Interactions = append(c.Interactions, interaction{Fingerprint: fp, Result: r})
}

// Find 按指纹查找录制结果，返回是否命中。
func (c *Cassette) Find(fp string) (*llm.LLMResult, bool) {
	for i := range c.Interactions {
		if c.Interactions[i].Fingerprint == fp {
			return c.Interactions[i].Result, true
		}
	}
	return nil, false
}

// Save 把磁带写入 JSON 文件。
func (c *Cassette) Save(path string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// LoadCassette 从 JSON 文件加载磁带。
func LoadCassette(path string) (*Cassette, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Cassette
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return &c, nil
}
