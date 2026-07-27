package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/boxify/api-go/internal/core/llm"
)

// Case 表示一条评估用例。
//
// Expect 为自由期望,由各打分器各取所需(如 reference 答案、应调工具、预算阈值)。
// Cassette 非空时按相对数据集文件目录解析为绝对路径,供运行器回放。
type Case struct {
	ID       string         `json:"id"`
	Query    string         `json:"query"`
	Messages []*llm.Message `json:"messages,omitempty"`
	Tags     []string       `json:"tags,omitempty"`
	Expect   map[string]any `json:"expect,omitempty"`
	Cassette string         `json:"cassette,omitempty"`
}

// Dataset 表示一组评估用例。
type Dataset struct {
	Name  string `json:"name"`
	Cases []Case `json:"cases"`
}

// LoadDataset 从 JSON 文件载入数据集,校验用例 ID 非空且唯一,并把非空相对
// Cassette 路径解析为相对该文件目录的绝对路径。
func LoadDataset(path string) (*Dataset, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read dataset: %w", err)
	}
	var ds Dataset
	if err := json.Unmarshal(raw, &ds); err != nil {
		return nil, fmt.Errorf("parse dataset: %w", err)
	}
	dir := filepath.Dir(path)
	seen := make(map[string]bool, len(ds.Cases))
	for i := range ds.Cases {
		c := &ds.Cases[i]
		if c.ID == "" {
			return nil, fmt.Errorf("case %d: empty id", i)
		}
		if seen[c.ID] {
			return nil, fmt.Errorf("duplicate case id %q", c.ID)
		}
		seen[c.ID] = true
		if c.Cassette != "" && !filepath.IsAbs(c.Cassette) {
			abs, err := filepath.Abs(filepath.Join(dir, c.Cassette))
			if err != nil {
				return nil, fmt.Errorf("resolve cassette path: %w", err)
			}
			c.Cassette = abs
		}
	}
	return &ds, nil
}
