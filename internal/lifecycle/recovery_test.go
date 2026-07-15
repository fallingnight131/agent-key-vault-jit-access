package lifecycle

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeRecoveryRepository struct {
	items    []RecoveryItem
	finishes []bool
}

func (repository *fakeRecoveryRepository) MarkStuckAndListRecovery(context.Context, time.Time, int) ([]RecoveryItem, error) {
	return repository.items, nil
}
func (*fakeRecoveryRepository) StartReclaim(_ context.Context, id string, _ time.Time) (string, error) {
	return "reclaim-" + id, nil
}
func (repository *fakeRecoveryRepository) FinishReclaim(_ context.Context, _ string, success bool, _ time.Time, _ string) error {
	repository.finishes = append(repository.finishes, success)
	return nil
}

type fakeRevoker struct {
	fail   bool
	leases []string
}

func (revoker *fakeRevoker) RevokeLease(_ context.Context, lease string) error {
	revoker.leases = append(revoker.leases, lease)
	if revoker.fail {
		return errors.New("fixture failure")
	}
	return nil
}

func TestRecoveryRetriesLeaseAndKeepsFailureBlocked(t *testing.T) {
	repository := &fakeRecoveryRepository{items: []RecoveryItem{{ExecutionID: "static"}, {ExecutionID: "dynamic", LeaseID: "lease"}}}
	revoker := &fakeRevoker{fail: true}
	result, err := NewRecoveryService(repository, revoker).Recover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Recovered != 1 || result.Failed != 1 || len(revoker.leases) != 1 || len(repository.finishes) != 2 || !repository.finishes[0] || repository.finishes[1] {
		t.Fatalf("result=%+v leases=%v finishes=%v", result, revoker.leases, repository.finishes)
	}
}
