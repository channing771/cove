package memory

import (
	"strings"
	"testing"
)

// 验证内置 Prompter 能渲染全部四个模板，并正确注入业务变量与 sprig 函数。
func TestBuiltinPrompterRendersAllTemplates(t *testing.T) {
	p := NewBuiltinPrompter()

	stmt, err := p.StatementExtract(&StatementPromptInput{Content: "用户在腾讯工作", Context: "对话背景"})
	if err != nil {
		t.Fatalf("StatementExtract() error = %v", err)
	}
	if !strings.Contains(stmt, "用户在腾讯工作") || !strings.Contains(stmt, "对话背景") {
		t.Fatalf("StatementExtract() missing injected content: %q", stmt)
	}

	// EntityTypes/Predicates 走 range，ValidAt 为空时 sprig default 应产出 NULL。
	triplet, err := p.TripletExtract(&TripletPromptInput{
		Statement:   "用户住在巴黎",
		EntityTypes: []string{"生命体"},
		Predicates:  []string{"位于"},
	})
	if err != nil {
		t.Fatalf("TripletExtract() error = %v", err)
	}
	for _, want := range []string{"用户住在巴黎", "生命体", "位于", "NULL"} {
		if !strings.Contains(triplet, want) {
			t.Fatalf("TripletExtract() missing %q in: %q", want, triplet)
		}
	}

	// 浮点/布尔特征转字符串，别名走 sprig join。
	dedup, err := p.DedupEntity(&DedupPromptInput{
		EntityA: DedupEntityPromptInput{Name: "小张", Aliases: []string{"张三", "老张"}},
		EntityB: DedupEntityPromptInput{Name: "张三"},
		Context: DedupPromptContext{NameTextSim: 0.85, NameEmbedSim: 0.9, NameContains: true},
	})
	if err != nil {
		t.Fatalf("DedupEntity() error = %v", err)
	}
	for _, want := range []string{"小张", "张三, 老张", "0.85", "true"} {
		if !strings.Contains(dedup, want) {
			t.Fatalf("DedupEntity() missing %q in: %q", want, dedup)
		}
	}

	meta, err := p.GenerateCommunityMetadata(&CommunityMetadataPromptInput{
		Members: []*CommunityMemberPromptInput{{Name: "复旦大学", Description: "用户就读的大学"}},
	})
	if err != nil {
		t.Fatalf("GenerateCommunityMetadata() error = %v", err)
	}
	if !strings.Contains(meta, "复旦大学") || !strings.Contains(meta, "主题名称") {
		t.Fatalf("GenerateCommunityMetadata() missing content: %q", meta)
	}
}

// 验证 nil 输入不会 panic，仍能渲染模板骨架。
func TestBuiltinPrompterHandlesNilInput(t *testing.T) {
	p := NewBuiltinPrompter()
	if _, err := p.StatementExtract(nil); err != nil {
		t.Fatalf("StatementExtract(nil) error = %v", err)
	}
	if _, err := p.TripletExtract(nil); err != nil {
		t.Fatalf("TripletExtract(nil) error = %v", err)
	}
	if _, err := p.DedupEntity(nil); err != nil {
		t.Fatalf("DedupEntity(nil) error = %v", err)
	}
	if _, err := p.GenerateCommunityMetadata(nil); err != nil {
		t.Fatalf("GenerateCommunityMetadata(nil) error = %v", err)
	}
}
