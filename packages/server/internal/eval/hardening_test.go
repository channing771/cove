package eval

import (
	"context"
	"testing"
)

func TestEvaluatorRunNilGuards(t *testing.T) {
	if _, err := (&Evaluator{Runner: stubRunner{}}).Run(context.Background(), nil); err == nil {
		t.Fatal("nil dataset should error")
	}
	if _, err := (&Evaluator{}).Run(context.Background(), &Dataset{Name: "x"}); err == nil {
		t.Fatal("nil runner should error")
	}
}

func TestHarnessRunnerNilClient(t *testing.T) {
	if _, err := (&HarnessRunner{}).Run(context.Background(), Case{ID: "a"}); err == nil {
		t.Fatal("nil client should error")
	}
}
