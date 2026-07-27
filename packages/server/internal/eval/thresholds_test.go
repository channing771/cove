package eval

import (
	"os"
	"path/filepath"
	"testing"
)

func TestThresholdProfileApplyTo(t *testing.T) {
	ds := &Dataset{Name: "d", Cases: []Case{
		{ID: "a", Expect: map[string]any{"relevant_ids": []any{"x"}, "recall_min": 0.5}},
		{ID: "b"},
	}}
	p := ThresholdProfile{
		"a": {"recall_min": 1.0, "ndcg_min": 0.9},
		"b": {"mrr_min": 0.75},
	}
	if err := p.ApplyTo(ds); err != nil {
		t.Fatalf("ApplyTo: %v", err)
	}
	if got := ds.Cases[0].Expect["recall_min"]; got != 1.0 {
		t.Fatalf("recall_min = %v, want 1.0(剖面应覆盖原值)", got)
	}
	if got := ds.Cases[0].Expect["ndcg_min"]; got != 0.9 {
		t.Fatalf("ndcg_min = %v", got)
	}
	if ds.Cases[0].Expect["relevant_ids"] == nil {
		t.Fatal("golden 标注不应被剖面清掉")
	}
	if got := ds.Cases[1].Expect["mrr_min"]; got != 0.75 {
		t.Fatalf("Expect 为 nil 的用例也应能写入: %v", got)
	}
	// 合并后应能被 ExpectFloat 正常读取(即打分器可用)
	if v, ok := ExpectFloat(ds.Cases[0], "ndcg_min"); !ok || v != 0.9 {
		t.Fatalf("ExpectFloat = %v %v", v, ok)
	}
}

func TestThresholdProfileRejectsUnknownCase(t *testing.T) {
	ds := &Dataset{Name: "d", Cases: []Case{{ID: "a"}}}
	err := ThresholdProfile{"typo-id": {"recall_min": 1}}.ApplyTo(ds)
	if err == nil {
		t.Fatal("未知 case_id 应报错,避免阈值静默失效")
	}
}

func TestLoadThresholds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "p.json")
	if err := os.WriteFile(path, []byte(`{"a":{"recall_min":1.0,"f1_min":0.8}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := LoadThresholds(path)
	if err != nil {
		t.Fatalf("LoadThresholds: %v", err)
	}
	if p["a"]["recall_min"] != 1.0 || p["a"]["f1_min"] != 0.8 {
		t.Fatalf("profile = %+v", p)
	}
	if _, err := LoadThresholds(filepath.Join(dir, "missing.json")); err == nil {
		t.Fatal("缺失文件应报错")
	}
}
