package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Organization struct {
	ID          uuid.UUID
	Name        string
	OwnerUserID uuid.UUID
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type OrganizationMember struct {
	ID             uuid.UUID
	OrganizationID uuid.UUID
	UserID         uuid.UUID
	Role           string
	CreatedAt      time.Time
}

type CreateOrgParams struct {
	Name        string
	OwnerUserID uuid.UUID
}

func (q *Queries) CreateOrg(ctx context.Context, arg CreateOrgParams) (Organization, error) {
	tx, err := q.db.Begin(ctx)
	if err != nil {
		return Organization{}, err
	}
	defer tx.Rollback(ctx)

	const orgQuery = `INSERT INTO organizations (name, owner_user_id) VALUES ($1,$2)
	RETURNING id, name, owner_user_id, created_at, updated_at`
	var org Organization
	if err := tx.QueryRow(ctx, orgQuery, arg.Name, arg.OwnerUserID).Scan(&org.ID, &org.Name, &org.OwnerUserID, &org.CreatedAt, &org.UpdatedAt); err != nil {
		return Organization{}, err
	}
	const memberQuery = `INSERT INTO organization_members (organization_id, user_id, role) VALUES ($1,$2,'owner')`
	if _, err := tx.Exec(ctx, memberQuery, org.ID, arg.OwnerUserID); err != nil {
		return Organization{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Organization{}, err
	}
	return org, nil
}

func (q *Queries) GetOrgByID(ctx context.Context, orgID uuid.UUID) (Organization, error) {
	const query = `SELECT id, name, owner_user_id, created_at, updated_at FROM organizations WHERE id=$1`
	var org Organization
	err := q.db.QueryRow(ctx, query, orgID).Scan(&org.ID, &org.Name, &org.OwnerUserID, &org.CreatedAt, &org.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Organization{}, ErrNotFound
	}
	return org, err
}

func (q *Queries) UpdateOrganizationName(ctx context.Context, orgID uuid.UUID, name string) (Organization, error) {
	const query = `UPDATE organizations
	SET name=$2, updated_at=NOW()
	WHERE id=$1
	RETURNING id, name, owner_user_id, created_at, updated_at`
	var org Organization
	err := q.db.QueryRow(ctx, query, orgID, name).Scan(&org.ID, &org.Name, &org.OwnerUserID, &org.CreatedAt, &org.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Organization{}, ErrNotFound
	}
	return org, err
}

func (q *Queries) TransferOrganizationOwnership(ctx context.Context, orgID, currentOwnerID, targetUserID uuid.UUID) (Organization, error) {
	if currentOwnerID == targetUserID {
		return Organization{}, ErrConflict
	}

	tx, err := q.db.Begin(ctx)
	if err != nil {
		return Organization{}, err
	}
	defer tx.Rollback(ctx)

	var ownerID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT owner_user_id FROM organizations WHERE id=$1 FOR UPDATE`, orgID).Scan(&ownerID); errors.Is(err, pgx.ErrNoRows) {
		return Organization{}, ErrNotFound
	} else if err != nil {
		return Organization{}, err
	}
	if ownerID != currentOwnerID {
		return Organization{}, ErrConflict
	}

	var targetRole string
	if err := tx.QueryRow(ctx, `SELECT role FROM organization_members WHERE organization_id=$1 AND user_id=$2 FOR UPDATE`, orgID, targetUserID).Scan(&targetRole); errors.Is(err, pgx.ErrNoRows) {
		return Organization{}, ErrNotFound
	} else if err != nil {
		return Organization{}, err
	}
	if targetRole != "admin" && targetRole != "member" {
		return Organization{}, ErrConflict
	}

	cmd, err := tx.Exec(ctx, `UPDATE organization_members SET role='admin' WHERE organization_id=$1 AND user_id=$2 AND role='owner'`, orgID, currentOwnerID)
	if err != nil {
		return Organization{}, err
	}
	if cmd.RowsAffected() != 1 {
		return Organization{}, ErrConflict
	}

	cmd, err = tx.Exec(ctx, `UPDATE organization_members SET role='owner' WHERE organization_id=$1 AND user_id=$2 AND role IN ('admin','member')`, orgID, targetUserID)
	if err != nil {
		return Organization{}, err
	}
	if cmd.RowsAffected() != 1 {
		return Organization{}, ErrConflict
	}

	var org Organization
	if err := tx.QueryRow(ctx, `UPDATE organizations
		SET owner_user_id=$2, updated_at=NOW()
		WHERE id=$1
		RETURNING id, name, owner_user_id, created_at, updated_at`, orgID, targetUserID).Scan(
		&org.ID,
		&org.Name,
		&org.OwnerUserID,
		&org.CreatedAt,
		&org.UpdatedAt,
	); err != nil {
		return Organization{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Organization{}, err
	}
	return org, nil
}

func (q *Queries) LeaveOrganization(ctx context.Context, orgID, userID uuid.UUID) error {
	tx, err := q.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var ownerID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT owner_user_id FROM organizations WHERE id=$1 FOR UPDATE`, orgID).Scan(&ownerID); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if ownerID == userID {
		return ErrConflict
	}

	cmd, err := tx.Exec(ctx, `DELETE FROM organization_members WHERE organization_id=$1 AND user_id=$2`, orgID, userID)
	if err != nil {
		return err
	}
	if cmd.RowsAffected() == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}

func (q *Queries) GetUserOrgs(ctx context.Context, userID uuid.UUID) ([]Organization, error) {
	const query = `SELECT o.id, o.name, o.owner_user_id, o.created_at, o.updated_at
	FROM organizations o
	INNER JOIN organization_members om ON om.organization_id = o.id
	WHERE om.user_id = $1
	ORDER BY o.created_at DESC`
	rows, err := q.db.Query(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Organization, 0)
	for rows.Next() {
		var org Organization
		if err := rows.Scan(&org.ID, &org.Name, &org.OwnerUserID, &org.CreatedAt, &org.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, org)
	}
	return items, rows.Err()
}

func (q *Queries) AddMember(ctx context.Context, orgID uuid.UUID, userID uuid.UUID, role string) (OrganizationMember, error) {
	const query = `INSERT INTO organization_members (organization_id, user_id, role) VALUES ($1,$2,$3)
	RETURNING id, organization_id, user_id, role, created_at`
	var m OrganizationMember
	err := q.db.QueryRow(ctx, query, orgID, userID, role).Scan(&m.ID, &m.OrganizationID, &m.UserID, &m.Role, &m.CreatedAt)
	if isUniqueViolation(err) {
		return OrganizationMember{}, ErrConflict
	}
	return m, err
}

func (q *Queries) RemoveMember(ctx context.Context, orgID uuid.UUID, userID uuid.UUID) error {
	tx, err := q.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var ownerID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT owner_user_id FROM organizations WHERE id=$1 FOR UPDATE`, orgID).Scan(&ownerID); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if ownerID == userID {
		return ErrConflict
	}

	cmd, err := tx.Exec(ctx, `DELETE FROM organization_members WHERE organization_id=$1 AND user_id=$2`, orgID, userID)
	if err != nil {
		return err
	}
	if cmd.RowsAffected() == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}

func (q *Queries) GetOrgMembers(ctx context.Context, orgID uuid.UUID) ([]OrganizationMember, error) {
	const query = `SELECT id, organization_id, user_id, role, created_at
	FROM organization_members WHERE organization_id=$1 ORDER BY created_at ASC`
	rows, err := q.db.Query(ctx, query, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]OrganizationMember, 0)
	for rows.Next() {
		var m OrganizationMember
		if err := rows.Scan(&m.ID, &m.OrganizationID, &m.UserID, &m.Role, &m.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, m)
	}
	return items, rows.Err()
}

func (q *Queries) GetOrganizationMember(ctx context.Context, orgID uuid.UUID, userID uuid.UUID) (OrganizationMember, error) {
	const query = `SELECT id, organization_id, user_id, role, created_at FROM organization_members WHERE organization_id=$1 AND user_id=$2`
	var m OrganizationMember
	err := q.db.QueryRow(ctx, query, orgID, userID).Scan(&m.ID, &m.OrganizationID, &m.UserID, &m.Role, &m.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return OrganizationMember{}, ErrNotFound
	}
	return m, err
}

func (q *Queries) CountOrganizationMembers(ctx context.Context, orgID uuid.UUID) (int64, error) {
	const query = `SELECT COUNT(1) FROM organization_members WHERE organization_id=$1`
	var total int64
	if err := q.db.QueryRow(ctx, query, orgID).Scan(&total); err != nil {
		return 0, err
	}
	return total, nil
}

func (q *Queries) ListShipmentsAccessibleByUser(ctx context.Context, userID uuid.UUID, limit int32, offset int32) ([]ShipmentListItem, error) {
	const query = `
SELECT
    s.id,
    s.title,
    s.share_mode,
    s.status,
    s.created_at,
    s.expires_at,
    s.max_downloads,
    COUNT(DISTINCT f.id)::int4 AS file_count,
    COUNT(de.id) FILTER (WHERE de.result = 'success')::int4 AS download_count,
    MAX(de.created_at) FILTER (WHERE de.result = 'success') AS last_download_at,
    s.current_downloads
FROM shipments s
LEFT JOIN files f ON f.shipment_id = s.id
LEFT JOIN download_events de ON de.shipment_id = s.id
LEFT JOIN organization_members om ON om.organization_id = s.organization_id
WHERE s.owner_user_id = $1 OR om.user_id = $1
GROUP BY s.id
ORDER BY s.created_at DESC, s.id DESC
LIMIT $2 OFFSET $3`
	rows, err := q.db.Query(ctx, query, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]ShipmentListItem, 0, limit)
	for rows.Next() {
		var item ShipmentListItem
		if err := rows.Scan(
			&item.ID,
			&item.Title,
			&item.ShareMode,
			&item.Status,
			&item.CreatedAt,
			&item.ExpiresAt,
			&item.MaxDownloads,
			&item.FileCount,
			&item.DownloadCount,
			&item.LastDownloadAt,
			&item.CurrentDownloads,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (q *Queries) CountShipmentsAccessibleByUser(ctx context.Context, userID uuid.UUID) (int64, error) {
	const query = `SELECT COUNT(DISTINCT s.id)
FROM shipments s
LEFT JOIN organization_members om ON om.organization_id = s.organization_id
WHERE s.owner_user_id = $1 OR om.user_id = $1`
	var total int64
	if err := q.db.QueryRow(ctx, query, userID).Scan(&total); err != nil {
		return 0, err
	}
	return total, nil
}
