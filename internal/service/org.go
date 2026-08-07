package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/example/vaultsend/internal/queue"
	"github.com/example/vaultsend/internal/store"
	"github.com/google/uuid"
)

type OrgStore interface {
	CreateOrg(ctx context.Context, arg store.CreateOrgParams) (store.Organization, error)
	GetOrgByID(ctx context.Context, orgID uuid.UUID) (store.Organization, error)
	GetUserOrgs(ctx context.Context, userID uuid.UUID) ([]store.Organization, error)
	AddMember(ctx context.Context, orgID uuid.UUID, userID uuid.UUID, role string) (store.OrganizationMember, error)
	RemoveMember(ctx context.Context, orgID uuid.UUID, userID uuid.UUID) error
	GetOrgMembers(ctx context.Context, orgID uuid.UUID) ([]store.OrganizationMember, error)
	GetOrganizationMember(ctx context.Context, orgID uuid.UUID, userID uuid.UUID) (store.OrganizationMember, error)
}

type OrgBillingService interface {
	GetSeatLimit(ctx context.Context, orgID uuid.UUID) (int64, error)
	GetCurrentSeatUsage(ctx context.Context, orgID uuid.UUID) (int64, error)
	SyncSeatCountWithStripe(ctx context.Context, orgID uuid.UUID) error
}

type OrgService struct {
	Store           OrgStore
	Billing         OrgBillingService
	InvitationStore OrgInvitationStore
	Queue           queue.Enqueuer
	FrontendURL     string
	InvitationTTL   time.Duration
	Now             func() time.Time
}

type OrgOutput struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	OwnerUserID uuid.UUID `json:"owner_user_id"`
}
type OrgMemberOutput struct {
	UserID uuid.UUID `json:"user_id"`
	Role   string    `json:"role"`
}

func (s *OrgService) CreateOrg(ctx context.Context, userID uuid.UUID, name string) (OrgOutput, error) {
	if userID == uuid.Nil {
		return OrgOutput{}, &APIError{Status: 401, Code: "unauthorized", Message: "ログインが必要です"}
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return OrgOutput{}, &APIError{Status: 400, Code: "invalid_name", Message: "name は必須です"}
	}
	org, err := s.Store.CreateOrg(ctx, store.CreateOrgParams{Name: name, OwnerUserID: userID})
	if err != nil {
		return OrgOutput{}, fmt.Errorf("create org: %w", err)
	}
	return OrgOutput{ID: org.ID, Name: org.Name, OwnerUserID: org.OwnerUserID}, nil
}

func (s *OrgService) ListUserOrgs(ctx context.Context, userID uuid.UUID) ([]OrgOutput, error) {
	rows, err := s.Store.GetUserOrgs(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]OrgOutput, 0, len(rows))
	for _, o := range rows {
		out = append(out, OrgOutput{ID: o.ID, Name: o.Name, OwnerUserID: o.OwnerUserID})
	}
	return out, nil
}

func (s *OrgService) GetOrg(ctx context.Context, userID, orgID uuid.UUID) (OrgOutput, []OrgMemberOutput, error) {
	if _, err := s.requireRole(ctx, userID, orgID, "member"); err != nil {
		return OrgOutput{}, nil, err
	}
	org, err := s.Store.GetOrgByID(ctx, orgID)
	if err != nil {
		return OrgOutput{}, nil, err
	}
	members, err := s.Store.GetOrgMembers(ctx, orgID)
	if err != nil {
		return OrgOutput{}, nil, err
	}
	m := make([]OrgMemberOutput, 0, len(members))
	for _, mm := range members {
		m = append(m, OrgMemberOutput{UserID: mm.UserID, Role: mm.Role})
	}
	return OrgOutput{ID: org.ID, Name: org.Name, OwnerUserID: org.OwnerUserID}, m, nil
}

func (s *OrgService) AddMember(ctx context.Context, actorID, orgID, userID uuid.UUID, role string) (OrgMemberOutput, error) {
	if _, err := s.requireRole(ctx, actorID, orgID, "admin"); err != nil {
		return OrgMemberOutput{}, err
	}
	role = strings.TrimSpace(strings.ToLower(role))
	if role != "owner" && role != "admin" && role != "member" {
		return OrgMemberOutput{}, &APIError{Status: 400, Code: "invalid_role", Message: "role が不正です"}
	}
	if _, err := s.Store.GetOrganizationMember(ctx, orgID, userID); err == nil {
		return OrgMemberOutput{}, &APIError{Status: 409, Code: "member_exists", Message: "既に所属済みです"}
	} else if !errors.Is(err, store.ErrNotFound) {
		return OrgMemberOutput{}, err
	}
	if s.Billing != nil {
		seatLimit, err := s.Billing.GetSeatLimit(ctx, orgID)
		if err != nil {
			return OrgMemberOutput{}, err
		}
		currentUsage, err := s.Billing.GetCurrentSeatUsage(ctx, orgID)
		if err != nil {
			return OrgMemberOutput{}, err
		}
		if currentUsage >= seatLimit {
			return OrgMemberOutput{}, newPlanLimitError("SEAT_LIMIT", "メンバー数の上限に達しています")
		}
	}
	m, err := s.Store.AddMember(ctx, orgID, userID, role)
	if errors.Is(err, store.ErrConflict) {
		return OrgMemberOutput{}, &APIError{Status: 409, Code: "member_exists", Message: "既に所属済みです"}
	}
	if err != nil {
		return OrgMemberOutput{}, err
	}
	if s.Billing != nil {
		if err := s.Billing.SyncSeatCountWithStripe(ctx, orgID); err != nil {
			_ = s.Store.RemoveMember(ctx, orgID, userID)
			return OrgMemberOutput{}, err
		}
	}
	return OrgMemberOutput{UserID: m.UserID, Role: m.Role}, nil
}

func (s *OrgService) RemoveMember(ctx context.Context, actorID, orgID, userID uuid.UUID) error {
	role, err := s.requireRole(ctx, actorID, orgID, "admin")
	if err != nil {
		return err
	}
	if role == "admin" && actorID == userID {
		return &APIError{Status: 409, Code: "cannot_remove_self", Message: "自身は削除できません"}
	}
	if err := s.Store.RemoveMember(ctx, orgID, userID); errors.Is(err, store.ErrNotFound) {
		return &APIError{Status: 404, Code: "member_not_found", Message: "member が見つかりません"}
	} else if err != nil {
		return err
	}
	if s.Billing != nil {
		if err := s.Billing.SyncSeatCountWithStripe(ctx, orgID); err != nil {
			return err
		}
	}
	return nil
}

func (s *OrgService) ResolveUserOrgRole(ctx context.Context, userID, orgID uuid.UUID) (string, error) {
	m, err := s.Store.GetOrganizationMember(ctx, orgID, userID)
	if err != nil {
		return "", err
	}
	return m.Role, nil
}

func (s *OrgService) AuthorizeShipmentAction(ctx context.Context, userID uuid.UUID, shipment store.Shipment, action string) error {
	if shipment.OwnerUserID != nil && *shipment.OwnerUserID == userID {
		return nil
	}
	if shipment.OrganizationID == nil {
		return &APIError{Status: 403, Code: "forbidden", Message: "権限がありません"}
	}
	role, err := s.ResolveUserOrgRole(ctx, userID, *shipment.OrganizationID)
	if errors.Is(err, store.ErrNotFound) {
		return &APIError{Status: 403, Code: "forbidden", Message: "権限がありません"}
	}
	if err != nil {
		return err
	}
	if action == "read" || action == "create" {
		return nil
	}
	if action == "delete" || action == "resend" {
		if role == "owner" || role == "admin" {
			return nil
		}
		return &APIError{Status: 403, Code: "forbidden", Message: "この操作は admin 以上が必要です"}
	}
	return nil
}

func (s *OrgService) requireRole(ctx context.Context, userID, orgID uuid.UUID, minimum string) (string, error) {
	if userID == uuid.Nil {
		return "", &APIError{Status: 401, Code: "unauthorized", Message: "ログインが必要です"}
	}
	m, err := s.Store.GetOrganizationMember(ctx, orgID, userID)
	if errors.Is(err, store.ErrNotFound) {
		return "", &APIError{Status: 403, Code: "forbidden", Message: "organization へのアクセス権がありません"}
	}
	if err != nil {
		return "", err
	}
	if minimum == "member" {
		return m.Role, nil
	}
	if minimum == "admin" && (m.Role == "admin" || m.Role == "owner") {
		return m.Role, nil
	}
	return "", &APIError{Status: 403, Code: "forbidden", Message: "admin 以上の権限が必要です"}
}
