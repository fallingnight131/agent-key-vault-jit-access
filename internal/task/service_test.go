package task

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fallingnight/akv/internal/domain"
)

type fakeRepository struct {
	records map[string]Record
}

func (repository *fakeRepository) CreateTask(_ context.Context, record Record) error {
	repository.records[record.ID] = record
	return nil
}

func (repository *fakeRepository) HeartbeatActiveTask(_ context.Context, taskID, agentID string, at time.Time) error {
	record, ok := repository.records[taskID]
	if !ok || record.AgentID != agentID || record.Status != domain.TaskActive {
		return ErrTaskUnavailable
	}
	record.LastHeartbeatAt = at
	repository.records[taskID] = record
	return nil
}

func (repository *fakeRepository) EndActiveTask(_ context.Context, taskID, agentID string, status domain.TaskStatus, at time.Time) ([]string, error) {
	record, ok := repository.records[taskID]
	if !ok || record.AgentID != agentID || record.Status != domain.TaskActive {
		return nil, ErrTaskUnavailable
	}
	record.Status, record.EndedAt = status, &at
	repository.records[taskID] = record
	return nil, nil
}

func (repository *fakeRepository) FindActiveTask(_ context.Context, taskID, agentID string) (Record, error) {
	record, ok := repository.records[taskID]
	if !ok || record.AgentID != agentID || record.Status != domain.TaskActive {
		return Record{}, ErrTaskUnavailable
	}
	return record, nil
}

func (repository *fakeRepository) MarkAgentLostBefore(_ context.Context, cutoff, endedAt time.Time) ([]Record, error) {
	var stale []Record
	for id, record := range repository.records {
		if record.Status == domain.TaskActive && !record.LastHeartbeatAt.After(cutoff) {
			record.Status, record.EndedAt = domain.TaskAgentLost, &endedAt
			repository.records[id] = record
			stale = append(stale, record)
		}
	}
	return stale, nil
}

func TestBeginCreatesServerUUIDv7(t *testing.T) {
	repository, service, now := newTestService()
	service.newID = uuidV7

	record, err := service.Begin(context.Background(), "agent-a")
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	compact := strings.ReplaceAll(record.ID, "-", "")
	decoded, err := hex.DecodeString(compact)
	if err != nil || len(decoded) != 16 {
		t.Fatalf("task ID %q is not UUID: %v", record.ID, err)
	}
	if version := decoded[6] >> 4; version != 7 {
		t.Fatalf("UUID version = %d, want 7", version)
	}
	if record.CreatedAt != now || record.LastHeartbeatAt != now || repository.records[record.ID].AgentID != "agent-a" {
		t.Fatalf("record = %+v", record)
	}
}

func TestTaskOwnershipAndTerminalState(t *testing.T) {
	_, service, _ := newTestService()
	record, err := service.Begin(context.Background(), "agent-a")
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	if err := service.Heartbeat(context.Background(), "agent-b", record.ID); !errors.Is(err, ErrTaskUnavailable) {
		t.Fatalf("cross-agent Heartbeat() error = %v", err)
	}
	if _, err := service.End(context.Background(), "agent-a", record.ID, domain.TaskCompleted); err != nil {
		t.Fatalf("End() error = %v", err)
	}
	if err := service.Heartbeat(context.Background(), "agent-a", record.ID); !errors.Is(err, ErrTaskUnavailable) {
		t.Fatalf("terminal Heartbeat() error = %v", err)
	}
	if _, err := service.ValidateActive(context.Background(), "agent-a", record.ID); !errors.Is(err, ErrTaskUnavailable) {
		t.Fatalf("terminal ValidateActive() error = %v", err)
	}
}

func TestSweepAgentLostUsesFortyFiveSecondBoundary(t *testing.T) {
	repository, service, now := newTestService()
	stale := Record{ID: "stale", AgentID: "agent-a", Status: domain.TaskActive, LastHeartbeatAt: now.Add(-AgentLostAfter)}
	fresh := Record{ID: "fresh", AgentID: "agent-b", Status: domain.TaskActive, LastHeartbeatAt: now.Add(-AgentLostAfter + time.Nanosecond)}
	repository.records[stale.ID], repository.records[fresh.ID] = stale, fresh

	lost, err := service.SweepAgentLost(context.Background())
	if err != nil {
		t.Fatalf("SweepAgentLost() error = %v", err)
	}
	if len(lost) != 1 || lost[0].ID != stale.ID || lost[0].Status != domain.TaskAgentLost {
		t.Fatalf("lost = %+v", lost)
	}
	if repository.records[fresh.ID].Status != domain.TaskActive {
		t.Fatal("fresh task was ended")
	}
}

func newTestService() (*fakeRepository, *Service, time.Time) {
	now := time.Date(2026, 7, 15, 1, 2, 3, 0, time.UTC)
	repository := &fakeRepository{records: make(map[string]Record)}
	service := NewService(repository)
	service.now = func() time.Time { return now }
	service.newID = func(time.Time) (string, error) { return "task-id", nil }
	return repository, service, now
}
