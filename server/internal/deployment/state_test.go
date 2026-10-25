package deployment

import "testing"

func TestCanTransition(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		from OperationStatus
		to   OperationStatus
		want bool
	}{
		{name: "queued dispatches", from: StatusQueued, to: StatusDispatching, want: true},
		{name: "queued accepts immediately ready provider", from: StatusQueued, to: StatusReady, want: true},
		{name: "pending accepts immediately ready provider", from: StatusPending, to: StatusReady, want: true},
		{name: "queued fails", from: StatusQueued, to: StatusFailed, want: true},
		{name: "queued cancels", from: StatusQueued, to: StatusCanceled, want: true},
		{name: "dispatch starts running", from: StatusDispatching, to: StatusRunning, want: true},
		{name: "dispatch retries", from: StatusDispatching, to: StatusRetryWait, want: true},
		{name: "running succeeds", from: StatusRunning, to: StatusSucceeded, want: true},
		{name: "running fails", from: StatusRunning, to: StatusFailed, want: true},
		{name: "running retries", from: StatusRunning, to: StatusRetryWait, want: true},
		{name: "retry dispatches", from: StatusRetryWait, to: StatusDispatching, want: true},
		{name: "same state is idempotent", from: StatusRunning, to: StatusRunning, want: true},
		{name: "running cannot regress", from: StatusRunning, to: StatusDispatching, want: false},
		{name: "success is terminal", from: StatusSucceeded, to: StatusRunning, want: false},
		{name: "failure is terminal", from: StatusFailed, to: StatusRetryWait, want: false},
		{name: "canceled is terminal", from: StatusCanceled, to: StatusDispatching, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := CanTransition(tt.from, tt.to); got != tt.want {
				t.Fatalf("CanTransition(%q, %q) = %v, want %v", tt.from, tt.to, got, tt.want)
			}
		})
	}
}

func TestTerminalStatuses(t *testing.T) {
	t.Parallel()

	for _, status := range []OperationStatus{StatusReady, StatusSucceeded, StatusFailed, StatusCanceled, StatusCancelled} {
		if !status.IsTerminal() {
			t.Fatalf("%q must be terminal", status)
		}
	}
	for _, status := range []OperationStatus{StatusPending, StatusQueued, StatusBuilding, StatusDeploying, StatusDispatching, StatusRunning, StatusRetryWait} {
		if status.IsTerminal() {
			t.Fatalf("%q must be non-terminal", status)
		}
	}
}

func TestNormalizedLifecycle(t *testing.T) {
	t.Parallel()

	steps := []OperationStatus{StatusPending, StatusQueued, StatusBuilding, StatusDeploying, StatusReady}
	for i := 1; i < len(steps); i++ {
		if !CanTransition(steps[i-1], steps[i]) {
			t.Fatalf("normalized transition %q -> %q should be allowed", steps[i-1], steps[i])
		}
	}
	if got := EvaluateTransition(StatusReady, StatusDeploying); got.Action != ActionRejectTerminalRegression {
		t.Fatalf("ready regression action = %q, want %q", got.Action, ActionRejectTerminalRegression)
	}
}

func TestStatusRank(t *testing.T) {
	t.Parallel()

	if StatusRank(StatusQueued) >= StatusRank(StatusDispatching) {
		t.Fatalf("queued rank must be less than dispatching")
	}
	if StatusRank(StatusDispatching) >= StatusRank(StatusRunning) {
		t.Fatalf("dispatching rank must be less than running")
	}
	if StatusRank(StatusRunning) >= StatusRank(StatusSucceeded) {
		t.Fatalf("running rank must be less than succeeded")
	}
	if StatusRank(StatusSucceeded) != StatusRank(StatusFailed) || StatusRank(StatusFailed) != StatusRank(StatusCanceled) {
		t.Fatalf("all terminal statuses must share maximum rank")
	}
}

func TestEvaluateTransition(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		current     OperationStatus
		observed    OperationStatus
		wantAllowed bool
		wantAction  TransitionAction
		wantTarget  OperationStatus
	}{
		// Valid progressive transitions
		{
			name:        "queued to dispatching",
			current:     StatusQueued,
			observed:    StatusDispatching,
			wantAllowed: true,
			wantAction:  ActionApply,
			wantTarget:  StatusDispatching,
		},
		{
			name:        "dispatching to running",
			current:     StatusDispatching,
			observed:    StatusRunning,
			wantAllowed: true,
			wantAction:  ActionApply,
			wantTarget:  StatusRunning,
		},
		{
			name:        "running to succeeded",
			current:     StatusRunning,
			observed:    StatusSucceeded,
			wantAllowed: true,
			wantAction:  ActionApply,
			wantTarget:  StatusSucceeded,
		},
		{
			name:        "running to failed",
			current:     StatusRunning,
			observed:    StatusFailed,
			wantAllowed: true,
			wantAction:  ActionApply,
			wantTarget:  StatusFailed,
		},
		{
			name:        "running to retry_wait",
			current:     StatusRunning,
			observed:    StatusRetryWait,
			wantAllowed: true,
			wantAction:  ActionApply,
			wantTarget:  StatusRetryWait,
		},
		{
			name:        "retry_wait to dispatching",
			current:     StatusRetryWait,
			observed:    StatusDispatching,
			wantAllowed: true,
			wantAction:  ActionApply,
			wantTarget:  StatusDispatching,
		},
		{
			name:        "queued to canceled",
			current:     StatusQueued,
			observed:    StatusCanceled,
			wantAllowed: true,
			wantAction:  ActionApply,
			wantTarget:  StatusCanceled,
		},

		// Terminal state immutability
		{
			name:        "succeeded rejects regression to running",
			current:     StatusSucceeded,
			observed:    StatusRunning,
			wantAllowed: false,
			wantAction:  ActionRejectTerminalRegression,
			wantTarget:  StatusSucceeded,
		},
		{
			name:        "succeeded rejects mutation to failed",
			current:     StatusSucceeded,
			observed:    StatusFailed,
			wantAllowed: false,
			wantAction:  ActionRejectTerminalRegression,
			wantTarget:  StatusSucceeded,
		},
		{
			name:        "failed rejects regression to running",
			current:     StatusFailed,
			observed:    StatusRunning,
			wantAllowed: false,
			wantAction:  ActionRejectTerminalRegression,
			wantTarget:  StatusFailed,
		},
		{
			name:        "canceled rejects regression to dispatching",
			current:     StatusCanceled,
			observed:    StatusDispatching,
			wantAllowed: false,
			wantAction:  ActionRejectTerminalRegression,
			wantTarget:  StatusCanceled,
		},

		// Out-of-order webhook delivery handling
		{
			name:        "running ignores delayed dispatching webhook",
			current:     StatusRunning,
			observed:    StatusDispatching,
			wantAllowed: false,
			wantAction:  ActionIgnoreOutOfOrder,
			wantTarget:  StatusRunning,
		},
		{
			name:        "running ignores delayed queued webhook",
			current:     StatusRunning,
			observed:    StatusQueued,
			wantAllowed: false,
			wantAction:  ActionIgnoreOutOfOrder,
			wantTarget:  StatusRunning,
		},
		{
			name:        "dispatching ignores delayed queued webhook",
			current:     StatusDispatching,
			observed:    StatusQueued,
			wantAllowed: false,
			wantAction:  ActionIgnoreOutOfOrder,
			wantTarget:  StatusDispatching,
		},

		// Idempotent duplicate delivery
		{
			name:        "running ignores identical running observation",
			current:     StatusRunning,
			observed:    StatusRunning,
			wantAllowed: false,
			wantAction:  ActionIgnoreIdempotent,
			wantTarget:  StatusRunning,
		},
		{
			name:        "succeeded ignores identical succeeded observation",
			current:     StatusSucceeded,
			observed:    StatusSucceeded,
			wantAllowed: false,
			wantAction:  ActionIgnoreIdempotent,
			wantTarget:  StatusSucceeded,
		},
		{
			name:        "queued ignores identical queued observation",
			current:     StatusQueued,
			observed:    StatusQueued,
			wantAllowed: false,
			wantAction:  ActionIgnoreIdempotent,
			wantTarget:  StatusQueued,
		},

		// Provider adapters may report a terminal result without intermediate states.
		{
			name:        "queued accepts immediately succeeded provider",
			current:     StatusQueued,
			observed:    StatusSucceeded,
			wantAllowed: true,
			wantAction:  ActionApply,
			wantTarget:  StatusSucceeded,
		},
		// Retry waits must still follow the internal dispatch lifecycle.
		{
			name:        "retry_wait cannot jump to succeeded",
			current:     StatusRetryWait,
			observed:    StatusSucceeded,
			wantAllowed: false,
			wantAction:  ActionRejectIllegal,
			wantTarget:  StatusRetryWait,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res := EvaluateTransition(tt.current, tt.observed)
			if res.Allowed != tt.wantAllowed {
				t.Fatalf("EvaluateTransition(%q, %q).Allowed = %v, want %v", tt.current, tt.observed, res.Allowed, tt.wantAllowed)
			}
			if res.Action != tt.wantAction {
				t.Fatalf("EvaluateTransition(%q, %q).Action = %q, want %q", tt.current, tt.observed, res.Action, tt.wantAction)
			}
			if res.TargetStatus != tt.wantTarget {
				t.Fatalf("EvaluateTransition(%q, %q).TargetStatus = %q, want %q", tt.current, tt.observed, res.TargetStatus, tt.wantTarget)
			}
		})
	}
}
