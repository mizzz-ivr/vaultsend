package worker

import (
	"context"
	"testing"
	"time"
)

type securityAuditOutboxStoreStub struct {
	delivered       int64
	deleted         int64
	pending         int64
	deliverLimit    int32
	cleanupLimit    int32
	cleanupBefore   time.Time
	deliverErr      error
	cleanupErr      error
	pendingCountErr error
}

func (s *securityAuditOutboxStoreStub) DeliverSecurityAuditOutboxBatch(_ context.Context, limit int32) (int64, error) {
	s.deliverLimit = limit
	return s.delivered, s.deliverErr
}

func (s *securityAuditOutboxStoreStub) DeleteProcessedSecurityAuditOutboxBefore(_ context.Context, before time.Time, limit int32) (int64, error) {
	s.cleanupBefore = before
	s.cleanupLimit = limit
	return s.deleted, s.cleanupErr
}

func (s *securityAuditOutboxStoreStub) CountPendingSecurityAuditOutbox(_ context.Context) (int64, error) {
	return s.pending, s.pendingCountErr
}

func TestSecurityAuditOutboxWorkerRunOnce(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	storeStub := &securityAuditOutboxStoreStub{delivered: 3, deleted: 2, pending: 4}
	worker := &SecurityAuditOutboxWorker{
		Store:            storeStub,
		BatchSize:        25,
		Retention:        48 * time.Hour,
		CleanupBatchSize: 40,
		Now:              func() time.Time { return now },
	}

	result, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnceに失敗しました: %v", err)
	}
	if result.Delivered != 3 || result.Deleted != 2 || result.Pending != 4 {
		t.Fatalf("結果が不正です: %#v", result)
	}
	if storeStub.deliverLimit != 25 || storeStub.cleanupLimit != 40 {
		t.Fatalf("batch sizeが反映されていません: deliver=%d cleanup=%d", storeStub.deliverLimit, storeStub.cleanupLimit)
	}
	wantBefore := now.Add(-48 * time.Hour)
	if !storeStub.cleanupBefore.Equal(wantBefore) {
		t.Fatalf("retention閾値が不正です: got=%s want=%s", storeStub.cleanupBefore, wantBefore)
	}
}

func TestSecurityAuditOutboxWorkerRequiresStore(t *testing.T) {
	worker := &SecurityAuditOutboxWorker{}
	if _, err := worker.RunOnce(context.Background()); err == nil {
		t.Fatal("store未設定が受理されました")
	}
}
