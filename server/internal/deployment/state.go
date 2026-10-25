package deployment

import "fmt"

// TransitionAction indicates how a proposed state transition should be handled.
type TransitionAction string

const (
	ActionApply                    TransitionAction = "apply"
	ActionIgnoreIdempotent         TransitionAction = "ignore_idempotent"
	ActionIgnoreOutOfOrder         TransitionAction = "ignore_out_of_order"
	ActionRejectTerminalRegression TransitionAction = "reject_terminal_regression"
	ActionRejectIllegal            TransitionAction = "reject_illegal"
)

// TransitionResult contains the evaluation verdict of an operation status change.
type TransitionResult struct {
	Allowed       bool             `json:"allowed"`
	Action        TransitionAction `json:"action"`
	CurrentStatus OperationStatus  `json:"current_status"`
	TargetStatus  OperationStatus  `json:"target_status"`
	Reason        string           `json:"reason"`
}

// StatusRank returns the monotonic lifecycle rank of a status to detect out-of-order arrivals.
func StatusRank(s OperationStatus) int {
	switch s {
	case StatusPending:
		return 10
	case StatusQueued:
		return 20
	case StatusBuilding:
		return 30
	case StatusDeploying:
		return 40
	case StatusReady, StatusSucceeded, StatusFailed, StatusCanceled, StatusCancelled:
		return 100
	case StatusRetryWait:
		return 15
	case StatusDispatching:
		return 25
	case StatusRunning:
		return 35
	default:
		return 0
	}
}

// CanTransition reports whether transitioning from status 'from' to 'to' is permitted.
func CanTransition(from, to OperationStatus) bool {
	if from == to {
		return true
	}

	switch from {
	case StatusPending:
		return to == StatusQueued || to == StatusBuilding || to == StatusDeploying || to == StatusReady || to == StatusSucceeded || to == StatusFailed || to == StatusCancelled
	case StatusQueued:
		return to == StatusBuilding || to == StatusDeploying || to == StatusReady || to == StatusSucceeded || to == StatusDispatching || to == StatusFailed || to == StatusCanceled || to == StatusCancelled
	case StatusBuilding:
		return to == StatusDeploying || to == StatusReady || to == StatusSucceeded || to == StatusFailed || to == StatusCancelled
	case StatusDeploying:
		return to == StatusReady || to == StatusSucceeded || to == StatusFailed || to == StatusCancelled
	case StatusDispatching:
		return to == StatusRunning || to == StatusRetryWait || to == StatusFailed || to == StatusCanceled
	case StatusRunning:
		return to == StatusSucceeded || to == StatusRetryWait || to == StatusFailed || to == StatusCanceled
	case StatusRetryWait:
		return to == StatusDispatching || to == StatusCanceled
	case StatusReady, StatusSucceeded, StatusFailed, StatusCanceled, StatusCancelled:
		return false
	default:
		return false
	}
}

// ValidateTransition returns an error if transitioning from 'from' to 'to' is illegal.
func ValidateTransition(from, to OperationStatus) error {
	if CanTransition(from, to) {
		return nil
	}
	return fmt.Errorf("%w: %q -> %q", ErrInvalidTransition, from, to)
}

// EvaluateTransition assesses an incoming observed status against the current status.
// It enforces terminal state immutability, detects out-of-order webhook delivery,
// and identifies idempotent duplicate observations.
func EvaluateTransition(current, observed OperationStatus) TransitionResult {
	if current.IsTerminal() {
		if observed == current {
			return TransitionResult{
				Allowed:       false,
				Action:        ActionIgnoreIdempotent,
				CurrentStatus: current,
				TargetStatus:  current,
				Reason:        "operation is already in terminal status",
			}
		}
		return TransitionResult{
			Allowed:       false,
			Action:        ActionRejectTerminalRegression,
			CurrentStatus: current,
			TargetStatus:  current,
			Reason:        fmt.Sprintf("cannot modify terminal status %q to %q", current, observed),
		}
	}

	if current == observed {
		return TransitionResult{
			Allowed:       false,
			Action:        ActionIgnoreIdempotent,
			CurrentStatus: current,
			TargetStatus:  current,
			Reason:        fmt.Sprintf("operation is already in status %q", current),
		}
	}

	if CanTransition(current, observed) {
		return TransitionResult{
			Allowed:       true,
			Action:        ActionApply,
			CurrentStatus: current,
			TargetStatus:  observed,
			Reason:        fmt.Sprintf("valid state transition from %q to %q", current, observed),
		}
	}

	if StatusRank(observed) <= StatusRank(current) {
		return TransitionResult{
			Allowed:       false,
			Action:        ActionIgnoreOutOfOrder,
			CurrentStatus: current,
			TargetStatus:  current,
			Reason:        fmt.Sprintf("superseded out-of-order status %q received while in status %q", observed, current),
		}
	}

	return TransitionResult{
		Allowed:       false,
		Action:        ActionRejectIllegal,
		CurrentStatus: current,
		TargetStatus:  current,
		Reason:        fmt.Sprintf("illegal state transition from %q to %q", current, observed),
	}
}
