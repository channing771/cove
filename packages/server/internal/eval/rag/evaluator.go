package rag

import (
	"context"
	"errors"

	"github.com/boxify/api-go/internal/eval"
)

// Evaluator 用一组 RAG 打分器在数据集上评估一个检索 Runner,产出复用的 eval.Report。
type Evaluator struct {
	Runner  *RetrievalRunner
	Scorers []Scorer
}

// Run 逐用例跑检索与全部打分器,经 eval.Aggregate 聚合成 Report(可用其 Diff/门禁/落盘)。
func (e *Evaluator) Run(ctx context.Context, ds *eval.Dataset) (*eval.Report, error) {
	if ds == nil {
		return nil, errors.New("rag: nil dataset")
	}
	if e.Runner == nil {
		return nil, errors.New("rag: nil runner")
	}
	cases := make([]eval.CaseResult, 0, len(ds.Cases))
	for _, c := range ds.Cases {
		record, err := e.Runner.Run(ctx, c)
		if err != nil {
			return nil, err
		}
		cr := eval.CaseResult{
			CaseID:    c.ID,
			Tags:      c.Tags,
			LatencyMS: record.Latency.Milliseconds(),
		}
		for _, s := range e.Scorers {
			cr.Scores = append(cr.Scores, s.Score(ctx, c, record))
		}
		cases = append(cases, cr)
	}
	return eval.Aggregate(ds.Name, cases), nil
}
