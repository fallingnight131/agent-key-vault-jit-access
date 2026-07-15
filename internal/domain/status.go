package domain

// TaskStatus is the lifecycle state of an AKV-managed task session.
type TaskStatus string

const (
	TaskActive    TaskStatus = "ACTIVE"
	TaskCompleted TaskStatus = "COMPLETED"
	TaskFailed    TaskStatus = "FAILED"
	TaskCancelled TaskStatus = "CANCELLED"
	TaskTimedOut  TaskStatus = "TIMED_OUT"
	TaskAgentLost TaskStatus = "AGENT_LOST"
)

func (status TaskStatus) CanTransitionTo(next TaskStatus) bool {
	if status != TaskActive {
		return false
	}
	switch next {
	case TaskCompleted, TaskFailed, TaskCancelled, TaskTimedOut, TaskAgentLost:
		return true
	default:
		return false
	}
}

// RequestStatus is the immutable authorization decision lifecycle.
type RequestStatus string

const (
	RequestPendingApproval RequestStatus = "PENDING_APPROVAL"
	RequestRejected        RequestStatus = "REJECTED"
	RequestApprovalExpired RequestStatus = "APPROVAL_EXPIRED"
	RequestApproved        RequestStatus = "APPROVED"
)

func (status RequestStatus) CanTransitionTo(next RequestStatus) bool {
	if status != RequestPendingApproval {
		return false
	}
	switch next {
	case RequestRejected, RequestApprovalExpired, RequestApproved:
		return true
	default:
		return false
	}
}

// GrantStatus covers consumption, execution outcome, and mandatory reclaim.
type GrantStatus string

const (
	GrantApproved      GrantStatus = "APPROVED"
	GrantRevoked       GrantStatus = "REVOKED"
	GrantExpired       GrantStatus = "GRANT_EXPIRED"
	GrantExecuting     GrantStatus = "EXECUTING"
	GrantSucceeded     GrantStatus = "SUCCEEDED"
	GrantFailed        GrantStatus = "FAILED"
	GrantCancelled     GrantStatus = "CANCELLED"
	GrantTimedOut      GrantStatus = "TIMED_OUT"
	GrantReclaiming    GrantStatus = "RECLAIMING"
	GrantReclaimed     GrantStatus = "RECLAIMED"
	GrantReclaimFailed GrantStatus = "RECLAIM_FAILED"
)

func (status GrantStatus) CanTransitionTo(next GrantStatus) bool {
	switch status {
	case GrantApproved:
		return next == GrantRevoked || next == GrantExpired || next == GrantExecuting
	case GrantExecuting:
		return next == GrantSucceeded || next == GrantFailed || next == GrantCancelled || next == GrantTimedOut
	case GrantSucceeded, GrantFailed, GrantCancelled, GrantTimedOut:
		return next == GrantReclaiming
	case GrantReclaiming:
		return next == GrantReclaimed || next == GrantReclaimFailed
	default:
		return false
	}
}

// ExecutionStatus describes a single already-claimed execution.
type ExecutionStatus string

const (
	ExecutionExecuting ExecutionStatus = "EXECUTING"
	ExecutionSucceeded ExecutionStatus = "SUCCEEDED"
	ExecutionFailed    ExecutionStatus = "FAILED"
	ExecutionCancelled ExecutionStatus = "CANCELLED"
	ExecutionTimedOut  ExecutionStatus = "TIMED_OUT"
)

func (status ExecutionStatus) CanTransitionTo(next ExecutionStatus) bool {
	if status != ExecutionExecuting {
		return false
	}
	switch next {
	case ExecutionSucceeded, ExecutionFailed, ExecutionCancelled, ExecutionTimedOut:
		return true
	default:
		return false
	}
}

// ReclaimStatus describes cleanup of sessions and derived credentials.
type ReclaimStatus string

const (
	Reclaiming    ReclaimStatus = "RECLAIMING"
	Reclaimed     ReclaimStatus = "RECLAIMED"
	ReclaimFailed ReclaimStatus = "RECLAIM_FAILED"
)

func (status ReclaimStatus) CanTransitionTo(next ReclaimStatus) bool {
	return status == Reclaiming && (next == Reclaimed || next == ReclaimFailed)
}
