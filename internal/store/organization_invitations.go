package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type OrganizationInvitation struct {
	ID               uuid.UUID
	OrganizationID   uuid.UUID
	Email            string
	EmailNormalized  string
	Role             string
	TokenHash        string
	Status           string
	InvitedByUserID  uuid.UUID
	AcceptedByUserID *uuid.UUID
	ExpiresAt        time.Time
	LastSentAt       *time.Time
	AcceptedAt       *time.Time
	RevokedAt        *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type CreateOrganizationInvitationParams struct {
	OrganizationID  uuid.UUID
	Email           string
	EmailNormalized string
	Role            string
	TokenHash       string
	InvitedByUserID uuid.UUID
	ExpiresAt       time.Time
	SeatLimit       int64
}

type RefreshOrganizationInvitationParams struct {
	InvitationID   uuid.UUID
	OrganizationID uuid.UUID
	TokenHash      string
	ExpiresAt      time.Time
}

type AcceptOrganizationInvitationParams struct {
	InvitationID uuid.UUID
	TokenHash    string
	UserID       uuid.UUID
	SeatLimit    int64
}

func (q *Queries) CreateOrganizationInvitation(ctx context.Context, arg CreateOrganizationInvitationParams) (OrganizationInvitation, error) {
	tx, err := q.db.Begin(ctx)
	if err != nil {
		return OrganizationInvitation{}, err
	}
	defer tx.Rollback(ctx)

	if err := lockOrganization(ctx, tx, arg.OrganizationID); err != nil {
		return OrganizationInvitation{}, err
	}

	const expireStale = `
UPDATE organization_invitations
SET status='revoked', revoked_at=now()
WHERE organization_id=$1 AND status='pending' AND expires_at <= now()`
	if _, err := tx.Exec(ctx, expireStale, arg.OrganizationID); err != nil {
		return OrganizationInvitation{}, err
	}
	seatLimit, err := resolveOrganizationSeatLimit(ctx, tx, arg.OrganizationID, arg.SeatLimit)
	if err != nil {
		return OrganizationInvitation{}, err
	}
	const countSeats = `
SELECT
    (SELECT COUNT(1) FROM organization_members WHERE organization_id=$1)
  + (SELECT COUNT(1) FROM organization_invitations WHERE organization_id=$1 AND status='pending' AND expires_at > now())`
	var reservedSeats int64
	if err := tx.QueryRow(ctx, countSeats, arg.OrganizationID).Scan(&reservedSeats); err != nil {
		return OrganizationInvitation{}, err
	}
	if reservedSeats >= seatLimit {
		return OrganizationInvitation{}, ErrOrganizationSeatLimit
	}

	const query = `
INSERT INTO organization_invitations (
    organization_id, email, email_normalized, role, token_hash, invited_by_user_id, expires_at
) VALUES ($1,$2,$3,$4,$5,$6,$7)
RETURNING id, organization_id, email, email_normalized, role, token_hash, status, invited_by_user_id,
          accepted_by_user_id, expires_at, last_sent_at, accepted_at, revoked_at, created_at, updated_at`
	var invitation OrganizationInvitation
	err = scanOrganizationInvitation(tx.QueryRow(ctx, query,
		arg.OrganizationID,
		arg.Email,
		arg.EmailNormalized,
		arg.Role,
		arg.TokenHash,
		arg.InvitedByUserID,
		arg.ExpiresAt,
	), &invitation)
	if isUniqueViolation(err) {
		return OrganizationInvitation{}, ErrConflict
	}
	if err != nil {
		return OrganizationInvitation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return OrganizationInvitation{}, err
	}
	return invitation, nil
}

func (q *Queries) ListOrganizationInvitations(ctx context.Context, organizationID uuid.UUID) ([]OrganizationInvitation, error) {
	const query = `
SELECT id, organization_id, email, email_normalized, role, token_hash, status, invited_by_user_id,
       accepted_by_user_id, expires_at, last_sent_at, accepted_at, revoked_at, created_at, updated_at
FROM organization_invitations
WHERE organization_id=$1
ORDER BY created_at DESC, id DESC`
	rows, err := q.db.Query(ctx, query, organizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]OrganizationInvitation, 0)
	for rows.Next() {
		var invitation OrganizationInvitation
		if err := scanOrganizationInvitation(rows, &invitation); err != nil {
			return nil, err
		}
		items = append(items, invitation)
	}
	return items, rows.Err()
}

func (q *Queries) GetOrganizationInvitationByID(ctx context.Context, organizationID, invitationID uuid.UUID) (OrganizationInvitation, error) {
	const query = `
SELECT id, organization_id, email, email_normalized, role, token_hash, status, invited_by_user_id,
       accepted_by_user_id, expires_at, last_sent_at, accepted_at, revoked_at, created_at, updated_at
FROM organization_invitations
WHERE organization_id=$1 AND id=$2`
	var invitation OrganizationInvitation
	err := scanOrganizationInvitation(q.db.QueryRow(ctx, query, organizationID, invitationID), &invitation)
	if errors.Is(err, pgx.ErrNoRows) {
		return OrganizationInvitation{}, ErrNotFound
	}
	return invitation, err
}

func (q *Queries) GetOrganizationInvitationByTokenHash(ctx context.Context, tokenHash string) (OrganizationInvitation, error) {
	const query = `
SELECT id, organization_id, email, email_normalized, role, token_hash, status, invited_by_user_id,
       accepted_by_user_id, expires_at, last_sent_at, accepted_at, revoked_at, created_at, updated_at
FROM organization_invitations
WHERE token_hash=$1`
	var invitation OrganizationInvitation
	err := scanOrganizationInvitation(q.db.QueryRow(ctx, query, tokenHash), &invitation)
	if errors.Is(err, pgx.ErrNoRows) {
		return OrganizationInvitation{}, ErrNotFound
	}
	return invitation, err
}

func (q *Queries) CountPendingOrganizationInvitations(ctx context.Context, organizationID uuid.UUID) (int64, error) {
	const query = `
SELECT COUNT(1)
FROM organization_invitations
WHERE organization_id=$1 AND status='pending' AND expires_at > now()`
	var count int64
	if err := q.db.QueryRow(ctx, query, organizationID).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (q *Queries) RevokeOrganizationInvitation(ctx context.Context, organizationID, invitationID uuid.UUID) error {
	const query = `
UPDATE organization_invitations
SET status='revoked', revoked_at=now()
WHERE organization_id=$1 AND id=$2 AND status='pending'`
	cmd, err := q.db.Exec(ctx, query, organizationID, invitationID)
	if err != nil {
		return err
	}
	if cmd.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (q *Queries) RefreshOrganizationInvitation(ctx context.Context, arg RefreshOrganizationInvitationParams) (OrganizationInvitation, error) {
	const query = `
UPDATE organization_invitations
SET token_hash=$3, expires_at=$4, last_sent_at=NULL
WHERE organization_id=$1 AND id=$2 AND status='pending'
RETURNING id, organization_id, email, email_normalized, role, token_hash, status, invited_by_user_id,
          accepted_by_user_id, expires_at, last_sent_at, accepted_at, revoked_at, created_at, updated_at`
	var invitation OrganizationInvitation
	err := scanOrganizationInvitation(q.db.QueryRow(ctx, query,
		arg.OrganizationID,
		arg.InvitationID,
		arg.TokenHash,
		arg.ExpiresAt,
	), &invitation)
	if errors.Is(err, pgx.ErrNoRows) {
		return OrganizationInvitation{}, ErrNotFound
	}
	if isUniqueViolation(err) {
		return OrganizationInvitation{}, ErrConflict
	}
	return invitation, err
}

func (q *Queries) MarkOrganizationInvitationQueued(ctx context.Context, invitationID uuid.UUID, queuedAt time.Time) error {
	const query = `UPDATE organization_invitations SET last_sent_at=$2 WHERE id=$1 AND status='pending'`
	cmd, err := q.db.Exec(ctx, query, invitationID, queuedAt)
	if err != nil {
		return err
	}
	if cmd.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (q *Queries) AcceptOrganizationInvitation(ctx context.Context, arg AcceptOrganizationInvitationParams) (OrganizationMember, error) {
	tx, err := q.db.Begin(ctx)
	if err != nil {
		return OrganizationMember{}, err
	}
	defer tx.Rollback(ctx)

	const lockInvitation = `
SELECT id, organization_id, email, email_normalized, role, token_hash, status, invited_by_user_id,
       accepted_by_user_id, expires_at, last_sent_at, accepted_at, revoked_at, created_at, updated_at
FROM organization_invitations
WHERE id=$1 AND token_hash=$2
FOR UPDATE`
	var invitation OrganizationInvitation
	if err := scanOrganizationInvitation(tx.QueryRow(ctx, lockInvitation, arg.InvitationID, arg.TokenHash), &invitation); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return OrganizationMember{}, ErrNotFound
		}
		return OrganizationMember{}, err
	}
	if invitation.Status != "pending" || !invitation.ExpiresAt.After(time.Now().UTC()) {
		return OrganizationMember{}, ErrConflict
	}
	if err := lockOrganization(ctx, tx, invitation.OrganizationID); err != nil {
		return OrganizationMember{}, err
	}
	seatLimit, err := resolveOrganizationSeatLimit(ctx, tx, invitation.OrganizationID, arg.SeatLimit)
	if err != nil {
		return OrganizationMember{}, err
	}
	const countMembers = `SELECT COUNT(1) FROM organization_members WHERE organization_id=$1`
	var members int64
	if err := tx.QueryRow(ctx, countMembers, invitation.OrganizationID).Scan(&members); err != nil {
		return OrganizationMember{}, err
	}
	if members >= seatLimit {
		return OrganizationMember{}, ErrOrganizationSeatLimit
	}

	const memberQuery = `
INSERT INTO organization_members (organization_id, user_id, role)
VALUES ($1,$2,$3)
RETURNING id, organization_id, user_id, role, created_at`
	var member OrganizationMember
	if err := tx.QueryRow(ctx, memberQuery, invitation.OrganizationID, arg.UserID, invitation.Role).Scan(
		&member.ID,
		&member.OrganizationID,
		&member.UserID,
		&member.Role,
		&member.CreatedAt,
	); err != nil {
		if isUniqueViolation(err) {
			return OrganizationMember{}, ErrConflict
		}
		return OrganizationMember{}, err
	}

	const acceptQuery = `
UPDATE organization_invitations
SET status='accepted', accepted_by_user_id=$2, accepted_at=now()
WHERE id=$1 AND status='pending'`
	cmd, err := tx.Exec(ctx, acceptQuery, invitation.ID, arg.UserID)
	if err != nil {
		return OrganizationMember{}, err
	}
	if cmd.RowsAffected() != 1 {
		return OrganizationMember{}, ErrConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return OrganizationMember{}, err
	}
	return member, nil
}

func lockOrganization(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID) error {
	var lockedID uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id FROM organizations WHERE id=$1 FOR UPDATE`, organizationID).Scan(&lockedID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func resolveOrganizationSeatLimit(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, explicitLimit int64) (int64, error) {
	if explicitLimit > 0 {
		return explicitLimit, nil
	}
	const query = `
SELECT COALESCE((
    SELECT CASE
        WHEN plan='pro' AND status IN ('active','trialing','past_due') THEN GREATEST(seat_count, 1)
        ELSE 1
    END
    FROM subscriptions
    WHERE organization_id=$1
    ORDER BY updated_at DESC
    LIMIT 1
), 1)`
	var seatLimit int64
	if err := tx.QueryRow(ctx, query, organizationID).Scan(&seatLimit); err != nil {
		return 0, err
	}
	return seatLimit, nil
}

type invitationScanner interface {
	Scan(dest ...any) error
}

func scanOrganizationInvitation(row invitationScanner, invitation *OrganizationInvitation) error {
	return row.Scan(
		&invitation.ID,
		&invitation.OrganizationID,
		&invitation.Email,
		&invitation.EmailNormalized,
		&invitation.Role,
		&invitation.TokenHash,
		&invitation.Status,
		&invitation.InvitedByUserID,
		&invitation.AcceptedByUserID,
		&invitation.ExpiresAt,
		&invitation.LastSentAt,
		&invitation.AcceptedAt,
		&invitation.RevokedAt,
		&invitation.CreatedAt,
		&invitation.UpdatedAt,
	)
}
