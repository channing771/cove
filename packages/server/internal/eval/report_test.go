package eval

import (
	"bytes"
	"encoding/json"
	"testing"
)

func mkReport(caseID, scorer string, passed bool) *Report {
	return &Report{
		Dataset: "d",
		Cases: []CaseResult{{
			CaseID: caseID,
			Passed: passed,
			Scores: []Score{{Scorer: scorer, Passed: passed}},
		}},
	}
}

func TestDiffDetectsNewFailure(t *testing.T) {
	base := mkReport("a", "contains", true)
	cur := mkReport("a", "contains", false)
	reg := cur.Diff(base)
	if len(reg.NewFailures) != 1 || reg.NewFailures[0].CaseID != "a" {
		t.Fatalf("regressions = %+v", reg.NewFailures)
	}
	// 反向:基线本就失败 → 不算回归
	if len(mkReport("a", "contains", true).Diff(mkReport("a", "contains", false)).NewFailures) != 0 {
		t.Fatal("baseline-failed should not count as new failure")
	}
}

func TestWriteJSONRoundTrip(t *testing.T) {
	rep := mkReport("a", "contains", true)
	rep.PassRate = 1
	var buf bytes.Buffer
	if err := rep.WriteJSON(&buf); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	var back Report
	if err := json.Unmarshal(buf.Bytes(), &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Cases[0].CaseID != "a" || back.PassRate != 1 {
		t.Fatalf("roundtrip = %+v", back)
	}
}
