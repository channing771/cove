package harness

import (
	"errors"
	"testing"

	coreagent "github.com/boxify/api-go/internal/core/agent"
)

func TestBudgetError_ImplementsStopReason(t *testing.T) {
	var sre coreagent.StopReasonError
	if !errors.As(error(ErrBudgetExceeded), &sre) {
		t.Fatal("ErrBudgetExceeded 应实现 StopReasonError")
	}
	if sre.AgentStopReason() != coreagent.StopBudgetExceeded {
		t.Fatalf("got %q", sre.AgentStopReason())
	}
}

func TestDeadlineError_StopReason(t *testing.T) {
	var sre coreagent.StopReasonError
	if !errors.As(error(ErrDeadlineExceeded), &sre) || sre.AgentStopReason() != coreagent.StopDeadlineExceeded {
		t.Fatal("ErrDeadlineExceeded 应映射 StopDeadlineExceeded")
	}
}

func TestToolDeniedError_StopReason(t *testing.T) {
	var sre coreagent.StopReasonError
	if !errors.As(error(ErrToolDenied), &sre) || sre.AgentStopReason() != coreagent.StopToolDenied {
		t.Fatal("ErrToolDenied 应映射 StopToolDenied")
	}
}
