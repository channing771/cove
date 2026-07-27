package eval

import "testing"

func TestReportByTag(t *testing.T) {
	rep := Aggregate("d", []CaseResult{
		{CaseID: "a", Tags: []string{"rag", "eval"}, Scores: []Score{{Scorer: "recall", Passed: true, Value: 1}}},
		{CaseID: "b", Tags: []string{"rag"}, Scores: []Score{{Scorer: "recall", Passed: false, Value: 0}}},
		{CaseID: "c", Tags: []string{"agent"}, Scores: []Score{{Scorer: "recall", Passed: true, Value: 1}}},
		{CaseID: "d", Scores: []Score{{Scorer: "recall", Passed: true, Value: 1}}},
	})
	byTag := rep.ByTag()

	rag, ok := byTag["rag"]
	if !ok {
		t.Fatal("missing rag tag")
	}
	if rag.Cases != 2 || rag.Passed != 1 || !near(rag.PassRate, 0.5) {
		t.Fatalf("rag agg = %+v", rag)
	}
	if agent := byTag["agent"]; agent.Cases != 1 || !near(agent.PassRate, 1) {
		t.Fatalf("agent agg = %+v", byTag["agent"])
	}
	if _, ok := byTag["eval"]; !ok {
		t.Fatal("一条用例的多个 tag 都应计入")
	}
	if _, ok := byTag[""]; ok {
		t.Fatal("无 tag 的用例不应产生空 tag 分组")
	}
}

func near(a, b float64) bool {
	d := a - b
	return d < 1e-9 && d > -1e-9
}
