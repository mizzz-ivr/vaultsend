package store

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// TransactionalAuditQueries は既存Queriesを埋め込み、監査outbox対応が必要な更新処理だけを上書きする。
type TransactionalAuditQueries struct {
	*Queries
}

func NewTransactionalAuditQueries(queries *Queries) *TransactionalAuditQueries {
	return &TransactionalAuditQueries{Queries: queries}
}

func (q *TransactionalAuditQueries) FinalizeShipment(ctx context.Context, arg FinalizeShipmentParams) (ShipmentFinalizeResult, error) {
	tx, err := q.db.Begin(ctx)
	if err != nil {
		return ShipmentFinalizeResult{}, err
	}
	defer tx.Rollback(ctx)

	const lockQuery = `SELECT id, owner_type, owner_user_id, organization_id, status, share_mode, title, message, password_hash, max_downloads,
       current_downloads, expires_at, sent_at, revoked_at, deleted_at, created_at, updated_at
FROM shipments WHERE id = $1 FOR UPDATE`
	var current Shipment
	if err := scanShipment(tx.QueryRow(ctx, lockQuery, arg.ShipmentID), &current); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ShipmentFinalizeResult{}, ErrNotFound
		}
		return ShipmentFinalizeResult{}, err
	}
	if len(arg.ExpectedStatuses) > 0 {
		statusAllowed := false
		for _, status := range arg.ExpectedStatuses {
			if current.Status == status {
				statusAllowed = true
				break
			}
		}
		if !statusAllowed {
			return ShipmentFinalizeResult{}, ErrConflict
		}
	}

	const updateShipment = `
UPDATE shipments
SET title=$2, message=$3, share_mode=$4, status=$5::shipment_status, expires_at=$6, max_downloads=$7, password_hash=$8, owner_user_id=COALESCE($9, owner_user_id), organization_id=COALESCE($10, organization_id), sent_at=CASE WHEN $5::shipment_status = 'sent'::shipment_status THEN now() ELSE sent_at END
WHERE id=$1
RETURNING id, owner_type, owner_user_id, organization_id, status, share_mode, title, message, password_hash, max_downloads,
          current_downloads, expires_at, sent_at, revoked_at, deleted_at, created_at, updated_at`
	var shipment Shipment
	if err := scanShipment(tx.QueryRow(
		ctx,
		updateShipment,
		arg.ShipmentID,
		arg.Title,
		arg.Message,
		arg.ShareMode,
		arg.Status,
		arg.ExpiresAt,
		arg.MaxDownloads,
		arg.PasswordHash,
		arg.OwnerUserID,
		arg.OrganizationID,
	), &shipment); err != nil {
		return ShipmentFinalizeResult{}, err
	}

	if len(arg.FileIDs) > 0 {
		const attachFiles = `UPDATE files SET shipment_id = $1 WHERE id = ANY($2::uuid[])`
		cmd, execErr := tx.Exec(ctx, attachFiles, arg.ShipmentID, arg.FileIDs)
		if execErr != nil {
			return ShipmentFinalizeResult{}, execErr
		}
		if int(cmd.RowsAffected()) != len(arg.FileIDs) {
			return ShipmentFinalizeResult{}, ErrNotFound
		}
	}

	recipients := make([]Recipient, 0, len(arg.Recipients))
	if len(arg.Recipients) > 0 {
		created, createErr := q.createRecipients(ctx, tx, arg.ShipmentID, arg.Recipients)
		if createErr != nil {
			if isUniqueViolation(createErr) {
				return ShipmentFinalizeResult{}, ErrConflict
			}
			return ShipmentFinalizeResult{}, createErr
		}
		recipients = created
	}

	if len(arg.AccessTokens) > 0 {
		recipientMap := map[string]uuid.UUID{}
		for _, recipient := range recipients {
			recipientMap[recipient.EmailNormalized] = recipient.ID
		}
		for i := range arg.AccessTokens {
			if key := arg.AccessTokens[i].RecipientEmailNormalized; key != "" {
				recipientID, ok := recipientMap[key]
				if !ok {
					return ShipmentFinalizeResult{}, ErrConflict
				}
				arg.AccessTokens[i].RecipientID = &recipientID
			}
		}
		if err := q.createAccessTokens(ctx, tx, arg.ShipmentID, arg.AccessTokens); err != nil {
			if isUniqueViolation(err) {
				return ShipmentFinalizeResult{}, ErrConflict
			}
			return ShipmentFinalizeResult{}, err
		}
	}

	files, err := q.getFilesByShipmentID(ctx, tx, arg.ShipmentID)
	if err != nil {
		return ShipmentFinalizeResult{}, err
	}

	resourceID := shipment.ID
	auditEvent, auditEnabled, err := PrepareSecurityAuditOutboxEvent(ctx, SecurityAuditOutboxEvent{
		EventType:      "shipment.create",
		Severity:       "info",
		Outcome:        "success",
		OrganizationID: shipment.OrganizationID,
		ResourceType:   "shipment",
		ResourceID:     &resourceID,
		StatusCode:     201,
		Details: map[string]string{
			"share_mode": shipment.ShareMode,
		},
	})
	if err != nil {
		return ShipmentFinalizeResult{}, err
	}
	if auditEnabled {
		if err := createSecurityAuditOutboxEvent(ctx, tx, auditEvent); err != nil {
			return ShipmentFinalizeResult{}, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return ShipmentFinalizeResult{}, err
	}
	if auditEnabled {
		MarkSecurityAuditOutboxEnqueued(ctx)
	}
	return ShipmentFinalizeResult{Shipment: shipment, Files: files, Recipients: recipients}, nil
}

func (q *TransactionalAuditQueries) DeleteShipment(ctx context.Context, shipmentID uuid.UUID) error {
	tx, err := q.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	const deleteShipment = `
UPDATE shipments
SET status = 'deleted',
    deleted_at = COALESCE(deleted_at, now())
WHERE id = $1
  AND status NOT IN ('deleted', 'revoked')
RETURNING organization_id`
	var organizationID *uuid.UUID
	if err := tx.QueryRow(ctx, deleteShipment, shipmentID).Scan(&organizationID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConflict
		}
		return err
	}

	const revokeTokens = `
UPDATE access_tokens
SET status = 'revoked',
    revoked_at = COALESCE(revoked_at, now())
WHERE shipment_id = $1
  AND status <> 'revoked'`
	if _, err := tx.Exec(ctx, revokeTokens, shipmentID); err != nil {
		return err
	}

	resourceID := shipmentID
	auditEvent, auditEnabled, err := PrepareSecurityAuditOutboxEvent(ctx, SecurityAuditOutboxEvent{
		EventType:      "shipment.delete",
		Severity:       "warning",
		Outcome:        "success",
		OrganizationID: organizationID,
		ResourceType:   "shipment",
		ResourceID:     &resourceID,
		StatusCode:     200,
	})
	if err != nil {
		return err
	}
	if auditEnabled {
		if err := createSecurityAuditOutboxEvent(ctx, tx, auditEvent); err != nil {
			return err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if auditEnabled {
		MarkSecurityAuditOutboxEnqueued(ctx)
	}
	return nil
}

func (q *TransactionalAuditQueries) CreateFileAndMarkUploadCompleted(ctx context.Context, arg CreateFileAndMarkUploadCompletedParams) (File, error) {
	tx, err := q.db.Begin(ctx)
	if err != nil {
		return File{}, err
	}
	defer tx.Rollback(ctx)

	file, err := q.createFile(ctx, tx, arg.CreateFile)
	if err != nil {
		return File{}, err
	}
	if err := q.markUploadSessionCompleted(ctx, tx, MarkUploadSessionCompletedParams{ID: arg.UploadSessionID, FileID: file.ID}); err != nil {
		return File{}, err
	}

	var organizationID *uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT organization_id FROM shipments WHERE id = $1`, arg.CreateFile.ShipmentID).Scan(&organizationID); err != nil {
		return File{}, err
	}
	resourceID := arg.UploadSessionID
	auditEvent, auditEnabled, err := PrepareSecurityAuditOutboxEvent(ctx, SecurityAuditOutboxEvent{
		EventType:      "upload.complete",
		Severity:       "info",
		Outcome:        "success",
		OrganizationID: organizationID,
		ResourceType:   "upload",
		ResourceID:     &resourceID,
		StatusCode:     200,
		Details: map[string]string{
			"shipment_id": arg.CreateFile.ShipmentID.String(),
		},
	})
	if err != nil {
		return File{}, err
	}
	if auditEnabled {
		if err := createSecurityAuditOutboxEvent(ctx, tx, auditEvent); err != nil {
			return File{}, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return File{}, err
	}
	if auditEnabled {
		MarkSecurityAuditOutboxEnqueued(ctx)
	}
	return file, nil
}
