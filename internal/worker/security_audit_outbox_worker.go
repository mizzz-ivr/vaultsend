package worker

import (
	"context"
	"errors"
	"log"
	"time"
)

type SecurityAuditOutboxStore interface {
	DeliverSecurityAuditOutboxBatch(ctx context.Context, limit int32) (int64, error)
	DeleteProcessedSecurityAuditOutboxBefore(ctx context.Context, before time.Time, limit int32) (int64, error)
	CountPendingSecurityAuditOutbox(ctx context.Context) (int64, error)
}

type SecurityAuditOutboxWorker struct {
	Store            SecurityAuditOutboxStore
	Interval         time.Duration
	BatchSize        int32
	Retention        time.Duration
	CleanupBatchSize int32
	Now              func() time.Time
}

type SecurityAuditOutboxRunResult struct {
	Delivered int64
	Deleted   int64
	Pending   int64
}

func (w *SecurityAuditOutboxWorker) Run(ctx context.Context) error {
	if w.Interval <= 0 {
		w.Interval = 2 * time.Second
	}
	ticker := time.NewTicker(w.Interval)
	defer ticker.Stop()

	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		if _, err := w.RunOnce(ctx); err != nil {
			log.Printf("event=security_audit_outbox_run_failed error=%q", err.Error())
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (w *SecurityAuditOutboxWorker) RunOnce(ctx context.Context) (SecurityAuditOutboxRunResult, error) {
	if w.Store == nil {
		return SecurityAuditOutboxRunResult{}, errors.New("security audit outbox worker: store is required")
	}
	if w.BatchSize <= 0 {
		w.BatchSize = 100
	}
	if w.Retention <= 0 {
		w.Retention = 7 * 24 * time.Hour
	}
	if w.CleanupBatchSize <= 0 {
		w.CleanupBatchSize = 500
	}

	result := SecurityAuditOutboxRunResult{}
	delivered, err := w.Store.DeliverSecurityAuditOutboxBatch(ctx, w.BatchSize)
	if err != nil {
		return result, err
	}
	result.Delivered = delivered

	deleted, err := w.Store.DeleteProcessedSecurityAuditOutboxBefore(ctx, w.now().Add(-w.Retention), w.CleanupBatchSize)
	if err != nil {
		return result, err
	}
	result.Deleted = deleted

	pending, err := w.Store.CountPendingSecurityAuditOutbox(ctx)
	if err != nil {
		return result, err
	}
	result.Pending = pending
	if delivered > 0 || deleted > 0 || pending > 0 {
		log.Printf(
			"event=security_audit_outbox_processed delivered=%d deleted=%d pending=%d",
			result.Delivered,
			result.Deleted,
			result.Pending,
		)
	}
	return result, nil
}

func (w *SecurityAuditOutboxWorker) now() time.Time {
	if w.Now != nil {
		return w.Now().UTC()
	}
	return time.Now().UTC()
}
