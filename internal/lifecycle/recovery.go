package lifecycle

import (
	"context"
	"time"
)

const RecoveryBatch = 100

type RecoveryItem struct {
	ExecutionID string
	LeaseID     string
}

type RecoveryResult struct {
	Candidates int
	Recovered  int
	Failed     int
}

type RecoveryRepository interface {
	MarkStuckAndListRecovery(context.Context, time.Time, int) ([]RecoveryItem, error)
	StartReclaim(context.Context, string, time.Time) (string, error)
	FinishReclaim(context.Context, string, bool, time.Time, string) error
}

type LeaseRevoker interface {
	RevokeLease(context.Context, string) error
}

type RecoveryService struct {
	repository RecoveryRepository
	revoker    LeaseRevoker
	now        func() time.Time
}

func NewRecoveryService(repository RecoveryRepository, revoker LeaseRevoker) *RecoveryService {
	return &RecoveryService{repository: repository, revoker: revoker, now: time.Now}
}

func (service *RecoveryService) Recover(ctx context.Context) (RecoveryResult, error) {
	now := service.now()
	items, err := service.repository.MarkStuckAndListRecovery(ctx, now, RecoveryBatch)
	if err != nil {
		return RecoveryResult{}, err
	}
	result := RecoveryResult{Candidates: len(items)}
	for _, item := range items {
		reclaimID, err := service.repository.StartReclaim(ctx, item.ExecutionID, service.now())
		if err != nil {
			result.Failed++
			continue
		}
		cleanupError := error(nil)
		if item.LeaseID != "" {
			cleanupError = service.revoker.RevokeLease(ctx, item.LeaseID)
		}
		success := cleanupError == nil
		code := ""
		if !success {
			code = "LEASE_REVOKE_FAILED"
		}
		if err := service.repository.FinishReclaim(ctx, reclaimID, success, service.now(), code); err != nil || !success {
			result.Failed++
		} else {
			result.Recovered++
		}
	}
	return result, nil
}
