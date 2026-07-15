package domain

import "testing"

func TestTaskTransitions(t *testing.T) {
	assertTransitions(t,
		[]TaskStatus{TaskActive, TaskCompleted, TaskFailed, TaskCancelled, TaskTimedOut, TaskAgentLost},
		map[transition[TaskStatus]]bool{
			{TaskActive, TaskCompleted}: true,
			{TaskActive, TaskFailed}:    true,
			{TaskActive, TaskCancelled}: true,
			{TaskActive, TaskTimedOut}:  true,
			{TaskActive, TaskAgentLost}: true,
		},
		func(from, to TaskStatus) bool { return from.CanTransitionTo(to) },
	)
}

func TestRequestTransitions(t *testing.T) {
	assertTransitions(t,
		[]RequestStatus{RequestPendingApproval, RequestRejected, RequestApprovalExpired, RequestApproved},
		map[transition[RequestStatus]]bool{
			{RequestPendingApproval, RequestRejected}:        true,
			{RequestPendingApproval, RequestApprovalExpired}: true,
			{RequestPendingApproval, RequestApproved}:        true,
		},
		func(from, to RequestStatus) bool { return from.CanTransitionTo(to) },
	)
}

func TestGrantTransitions(t *testing.T) {
	statuses := []GrantStatus{
		GrantApproved, GrantRevoked, GrantExpired, GrantExecuting, GrantSucceeded,
		GrantFailed, GrantCancelled, GrantTimedOut, GrantReclaiming, GrantReclaimed, GrantReclaimFailed,
	}
	want := map[transition[GrantStatus]]bool{
		{GrantApproved, GrantRevoked}:         true,
		{GrantApproved, GrantExpired}:         true,
		{GrantApproved, GrantExecuting}:       true,
		{GrantExecuting, GrantSucceeded}:      true,
		{GrantExecuting, GrantFailed}:         true,
		{GrantExecuting, GrantCancelled}:      true,
		{GrantExecuting, GrantTimedOut}:       true,
		{GrantSucceeded, GrantReclaiming}:     true,
		{GrantFailed, GrantReclaiming}:        true,
		{GrantCancelled, GrantReclaiming}:     true,
		{GrantTimedOut, GrantReclaiming}:      true,
		{GrantReclaiming, GrantReclaimed}:     true,
		{GrantReclaiming, GrantReclaimFailed}: true,
	}
	assertTransitions(t, statuses, want, func(from, to GrantStatus) bool { return from.CanTransitionTo(to) })
}

func TestExecutionTransitions(t *testing.T) {
	assertTransitions(t,
		[]ExecutionStatus{ExecutionExecuting, ExecutionSucceeded, ExecutionFailed, ExecutionCancelled, ExecutionTimedOut},
		map[transition[ExecutionStatus]]bool{
			{ExecutionExecuting, ExecutionSucceeded}: true,
			{ExecutionExecuting, ExecutionFailed}:    true,
			{ExecutionExecuting, ExecutionCancelled}: true,
			{ExecutionExecuting, ExecutionTimedOut}:  true,
		},
		func(from, to ExecutionStatus) bool { return from.CanTransitionTo(to) },
	)
}

func TestReclaimTransitions(t *testing.T) {
	assertTransitions(t,
		[]ReclaimStatus{Reclaiming, Reclaimed, ReclaimFailed},
		map[transition[ReclaimStatus]]bool{
			{Reclaiming, Reclaimed}:     true,
			{Reclaiming, ReclaimFailed}: true,
		},
		func(from, to ReclaimStatus) bool { return from.CanTransitionTo(to) },
	)
}

type transition[T comparable] struct {
	from T
	to   T
}

func assertTransitions[T comparable](
	t *testing.T,
	statuses []T,
	want map[transition[T]]bool,
	canTransition func(T, T) bool,
) {
	t.Helper()
	for _, from := range statuses {
		for _, to := range statuses {
			got := canTransition(from, to)
			if got != want[transition[T]{from, to}] {
				t.Errorf("CanTransitionTo(%v -> %v) = %t, want %t", from, to, got, !got)
			}
		}
	}
}
