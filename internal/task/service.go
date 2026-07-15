package task

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/fallingnight/akv/internal/domain"
)

const (
	HeartbeatInterval = 15 * time.Second
	AgentLostAfter    = 45 * time.Second
)

var (
	ErrInvalidInput    = errors.New("invalid task input")
	ErrTaskUnavailable = errors.New("task unavailable")
)

type Record struct {
	ID              string
	AgentID         string
	Status          domain.TaskStatus
	CreatedAt       time.Time
	LastHeartbeatAt time.Time
	EndedAt         *time.Time
}

type Repository interface {
	CreateTask(context.Context, Record) error
	HeartbeatActiveTask(context.Context, string, string, time.Time) error
	EndActiveTask(context.Context, string, string, domain.TaskStatus, time.Time) ([]string, error)
	FindActiveTask(context.Context, string, string) (Record, error)
	MarkAgentLostBefore(context.Context, time.Time, time.Time) ([]Record, error)
}

type Service struct {
	repository Repository
	now        func() time.Time
	newID      func(time.Time) (string, error)
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository, now: time.Now, newID: uuidV7}
}

func (service *Service) Begin(ctx context.Context, agentID string) (Record, error) {
	if agentID == "" {
		return Record{}, ErrInvalidInput
	}
	now := service.now()
	id, err := service.newID(now)
	if err != nil {
		return Record{}, fmt.Errorf("generate task ID: %w", err)
	}
	record := Record{
		ID: id, AgentID: agentID, Status: domain.TaskActive,
		CreatedAt: now, LastHeartbeatAt: now,
	}
	if err := service.repository.CreateTask(ctx, record); err != nil {
		return Record{}, fmt.Errorf("create task: %w", err)
	}
	return record, nil
}

func (service *Service) Heartbeat(ctx context.Context, agentID, taskID string) error {
	if agentID == "" || taskID == "" {
		return ErrInvalidInput
	}
	if err := service.repository.HeartbeatActiveTask(ctx, taskID, agentID, service.now()); err != nil {
		return ErrTaskUnavailable
	}
	return nil
}

func (service *Service) ValidateActive(ctx context.Context, agentID, taskID string) (Record, error) {
	if agentID == "" || taskID == "" {
		return Record{}, ErrInvalidInput
	}
	record, err := service.repository.FindActiveTask(ctx, taskID, agentID)
	if err != nil || record.Status != domain.TaskActive || record.AgentID != agentID {
		return Record{}, ErrTaskUnavailable
	}
	return record, nil
}

func (service *Service) End(ctx context.Context, agentID, taskID string, outcome domain.TaskStatus) ([]string, error) {
	if agentID == "" || taskID == "" || !domain.TaskActive.CanTransitionTo(outcome) {
		return nil, ErrInvalidInput
	}
	executionIDs, err := service.repository.EndActiveTask(ctx, taskID, agentID, outcome, service.now())
	if err != nil {
		return nil, ErrTaskUnavailable
	}
	return executionIDs, nil
}

// SweepAgentLost atomically ends stale tasks and returns them for authorization reclaim.
func (service *Service) SweepAgentLost(ctx context.Context) ([]Record, error) {
	now := service.now()
	records, err := service.repository.MarkAgentLostBefore(ctx, now.Add(-AgentLostAfter), now)
	if err != nil {
		return nil, fmt.Errorf("mark agent-lost tasks: %w", err)
	}
	return records, nil
}

func uuidV7(now time.Time) (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	milliseconds := uint64(now.UnixMilli())
	var timestamp [8]byte
	binary.BigEndian.PutUint64(timestamp[:], milliseconds)
	copy(value[0:6], timestamp[2:8])
	value[6] = (value[6] & 0x0f) | 0x70
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
}
