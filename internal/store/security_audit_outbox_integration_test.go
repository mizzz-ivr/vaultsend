//go:build integration

package store

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSecurityAuditOutboxAtomicityAndDelivery(t *testing.T) {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_URL が未設定のためintegration testをスキップします")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("PostgreSQL poolの作成に失敗しました: %v", err)
	}
	t.Cleanup(pool.Close)
	base := New(pool)
	queries := NewTransactionalAuditQueries(base)

	now := time.Now().UTC().Truncate(time.Microsecond)
	rollbackShipment := createAuditTestShipment(t, ctx, base, "audit rollback", "draft", now)
	invalidCtx := auditOutboxTestContext(now, true, nil)
	if err := queries.DeleteShipment(invalidCtx, rollbackShipment.ID); err == nil {
		t.Fatal("outbox INSERT失敗時にshipment削除が成功しました")
	}
	storedRollbackShipment, err := base.GetShipment(ctx, rollbackShipment.ID)
	if err != nil {
		t.Fatalf("rollback確認用shipmentの取得に失敗しました: %v", err)
	}
	if storedRollbackShipment.Status != "draft" {
		t.Fatalf("outbox失敗時に業務更新がcommitされています: %s", storedRollbackShipment.Status)
	}

	eventIDs := make([]uuid.UUID, 0, 3)
	finalizeShipment := createAuditTestShipment(t, ctx, base, "audit finalize", "draft", now)
	finalizeCtx := auditOutboxTestContext(now, false, &eventIDs)
	if _, err := queries.FinalizeShipment(finalizeCtx, FinalizeShipmentParams{
		ShipmentID:       finalizeShipment.ID,
		ExpectedStatuses: []string{"draft"},
		Title:            "audit finalized",
		ShareMode:        "url_shared",
		Status:           "sent",
		ExpiresAt:        now.Add(24 * time.Hour),
		MaxDownloads:     10,
	}); err != nil {
		t.Fatalf("shipment finalizeに失敗しました: %v", err)
	}
	if !SecurityAuditOutboxEnqueued(finalizeCtx) {
		t.Fatal("shipment finalizeでoutbox enqueue状態が記録されていません")
	}

	uploadShipment := createAuditTestShipment(t, ctx, base, "audit upload", "uploading", now)
	uploadSession, err := base.CreateUploadSession(ctx, CreateUploadSessionParams{
		ShipmentID:        &uploadShipment.ID,
		StorageBucket:     "audit-test",
		StorageKey:        "audit/" + uuid.NewString(),
		MultipartUploadID: "upload-" + uuid.NewString(),
		PartSizeBytes:     8 * 1024 * 1024,
		Status:            "uploading",
		ExpiresAt:         now.Add(time.Hour),
		FileName:          "audit.txt",
		ContentType:       "text/plain",
		FileSizeBytes:     10,
		ChecksumSha256:    strings.Repeat("b", 64),
	})
	if err != nil {
		t.Fatalf("upload session作成に失敗しました: %v", err)
	}
	uploadCtx := auditOutboxTestContext(now, false, &eventIDs)
	if _, err := queries.CreateFileAndMarkUploadCompleted(uploadCtx, CreateFileAndMarkUploadCompletedParams{
		UploadSessionID: uploadSession.ID,
		CreateFile: CreateFileParams{
			ShipmentID:     uploadShipment.ID,
			OriginalName:   "audit.txt",
			SizeBytes:      10,
			MimeType:       "text/plain",
			StorageBucket:  "audit-test",
			StorageKey:     "audit/" + uuid.NewString(),
			ChecksumSha256: strings.Repeat("b", 64),
			UploadStatus:   "completed",
		},
	}); err != nil {
		t.Fatalf("upload completeに失敗しました: %v", err)
	}

	deleteShipment := createAuditTestShipment(t, ctx, base, "audit delete", "sent", now)
	deleteCtx := auditOutboxTestContext(now, false, &eventIDs)
	if err := queries.DeleteShipment(deleteCtx, deleteShipment.ID); err != nil {
		t.Fatalf("shipment deleteに失敗しました: %v", err)
	}

	pending, err := base.CountPendingSecurityAuditOutbox(ctx)
	if err != nil {
		t.Fatalf("pending outbox件数の取得に失敗しました: %v", err)
	}
	if pending != 3 {
		t.Fatalf("pending outbox件数が不正です: got=%d want=3", pending)
	}
	var beforeDelivery int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM security_audit_events WHERE id = ANY($1::uuid[])`, eventIDs).Scan(&beforeDelivery); err != nil {
		t.Fatalf("配送前監査件数の取得に失敗しました: %v", err)
	}
	if beforeDelivery != 0 {
		t.Fatalf("worker配送前に監査ログへ直接INSERTされています: %d", beforeDelivery)
	}

	delivered, err := base.DeliverSecurityAuditOutboxBatch(ctx, 10)
	if err != nil {
		t.Fatalf("outbox配送に失敗しました: %v", err)
	}
	if delivered != 3 {
		t.Fatalf("配送件数が不正です: got=%d want=3", delivered)
	}
	deliveredAgain, err := base.DeliverSecurityAuditOutboxBatch(ctx, 10)
	if err != nil {
		t.Fatalf("outbox再配送に失敗しました: %v", err)
	}
	if deliveredAgain != 0 {
		t.Fatalf("処理済みoutboxが再配送されました: %d", deliveredAgain)
	}
	var finalCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM security_audit_events WHERE id = ANY($1::uuid[])`, eventIDs).Scan(&finalCount); err != nil {
		t.Fatalf("配送後監査件数の取得に失敗しました: %v", err)
	}
	if finalCount != 3 {
		t.Fatalf("冪等配送後の監査件数が不正です: got=%d want=3", finalCount)
	}

	_, _ = pool.Exec(ctx, `DELETE FROM security_audit_outbox WHERE id = ANY($1::uuid[])`, eventIDs)
	_, _ = pool.Exec(ctx, `DELETE FROM upload_sessions WHERE id = $1`, uploadSession.ID)
	_, _ = pool.Exec(ctx, `DELETE FROM shipments WHERE id = ANY($1::uuid[])`, []uuid.UUID{
		rollbackShipment.ID,
		finalizeShipment.ID,
		uploadShipment.ID,
		deleteShipment.ID,
	})
}

func createAuditTestShipment(t *testing.T, ctx context.Context, queries *Queries, title, status string, now time.Time) Shipment {
	t.Helper()
	shipment, err := queries.CreateShipment(ctx, CreateShipmentParams{
		OwnerType:    "anonymous",
		Status:       status,
		ShareMode:    "url_shared",
		Title:        title,
		MaxDownloads: 10,
		ExpiresAt:    now.Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("shipment作成に失敗しました: %v", err)
	}
	return shipment
}

func auditOutboxTestContext(now time.Time, invalid bool, eventIDs *[]uuid.UUID) context.Context {
	state := &SecurityAuditOutboxState{}
	state.Prepare = func(event SecurityAuditOutboxEvent) (CreateSecurityAuditEventParams, error) {
		eventID := uuid.New()
		if eventIDs != nil {
			*eventIDs = append(*eventIDs, eventID)
		}
		eventType := event.EventType
		if invalid {
			eventType = "INVALID EVENT TYPE"
		}
		resourceType := event.ResourceType
		statusCode := int32(event.StatusCode)
		return CreateSecurityAuditEventParams{
			ID:             eventID,
			OccurredAt:     now,
			EventType:      eventType,
			Severity:       event.Severity,
			Outcome:        event.Outcome,
			ActorType:      "anonymous",
			OrganizationID: event.OrganizationID,
			ResourceType:   &resourceType,
			ResourceID:     event.ResourceID,
			SourceService:  "api",
			StatusCode:     &statusCode,
			Details:        json.RawMessage(`{"schema_version":"1"}`),
			IntegrityKeyID: "integration-test",
			IntegrityHMAC:  strings.Repeat("a", 64),
		}, nil
	}
	return WithSecurityAuditOutboxState(context.Background(), state)
}
