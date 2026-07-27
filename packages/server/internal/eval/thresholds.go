package eval

import (
	"encoding/json"
	"fmt"
	"os"
)

// ThresholdProfile 是按用例 ID 组织的阈值集合:case_id → {阈值键: 值}。
//
// 存在的理由:golden 标注是**客观事实**(哪些文档确实相关),阈值是**对某一具体配置的
// 期望**(该配置在这批用例上应达到的水平)。二者混在同一份数据集里,换检索配置/向量模型
// 就得改动 golden,既易出错也让数据集失去"事实"属性。拆开后,一份数据集可配多套阈值剖面
// (如词形嵌入器一套、语义嵌入器一套),各自门禁互不牵连。
type ThresholdProfile map[string]map[string]float64

// LoadThresholds 从 JSON 文件载入阈值剖面。
func LoadThresholds(path string) (ThresholdProfile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read thresholds: %w", err)
	}
	var p ThresholdProfile
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("parse thresholds: %w", err)
	}
	return p, nil
}

// ApplyTo 把阈值合并进数据集各用例的 Expect。
//
// 剖面中出现数据集里不存在的 case_id 会报错(挡住改名/拼写错误导致的"阈值静默失效")。
// 已存在的同名键会被剖面覆盖。
func (p ThresholdProfile) ApplyTo(ds *Dataset) error {
	if ds == nil {
		return fmt.Errorf("eval: nil dataset")
	}
	index := make(map[string]*Case, len(ds.Cases))
	for i := range ds.Cases {
		index[ds.Cases[i].ID] = &ds.Cases[i]
	}
	for id, thresholds := range p {
		c, ok := index[id]
		if !ok {
			return fmt.Errorf("eval: 阈值剖面含未知用例 %q", id)
		}
		if c.Expect == nil {
			c.Expect = map[string]any{}
		}
		for key, value := range thresholds {
			c.Expect[key] = value
		}
	}
	return nil
}
