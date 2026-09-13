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

	for _, status := range []OperationStatus{StatusSucceeded, StatusFailed, StatusCanceled} {
		if !status.IsTerminal() {
			t.Fatalf("%q must be terminal", status)
		}
	}
	for _, status := range []OperationStatus{StatusQueued, StatusDispatching, StatusRunning, StatusRetryWait} {
		if status.IsTerminal() {
			t.Fatalf("%q must be non-terminal", status)
		}
	}
}
