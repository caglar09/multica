package deployment

import "fmt"

func CanTransition(from, to OperationStatus) bool {
	if from == to {
		return true
	}

	switch from {
	case StatusQueued:
		return to == StatusDispatching || to == StatusCanceled
	case StatusDispatching:
		return to == StatusRunning || to == StatusRetryWait || to == StatusFailed || to == StatusCanceled
	case StatusRunning:
		return to == StatusSucceeded || to == StatusRetryWait || to == StatusFailed || to == StatusCanceled
	case StatusRetryWait:
		return to == StatusDispatching || to == StatusCanceled
	case StatusSucceeded, StatusFailed, StatusCanceled:
		return false
	default:
		return false
	}
}

func ValidateTransition(from, to OperationStatus) error {
	if CanTransition(from, to) {
		return nil
	}
	return fmt.Errorf("%w: %q -> %q", ErrInvalidTransition, from, to)
}
