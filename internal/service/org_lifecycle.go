package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/example/vaultsend/internal/store"
	"github.com/google/uuid"
)

const maxOrganizationNameLength = 120

type OrgLifecycleStore interface {
	UpdateOrganizationName(ctx context.Context, orgID uuid.UUID, name string) (store.Organization, error)
	TransferOrganizationOwnership(ctx context.Context, orgID, currentOwnerID, targetUserID uuid.UUID) (store.Organization, error)
	LeaveOrganization(ctx context.Context, orgID, userID uuid.UUID) error
}

type OwnerTransferOutput struct {
	Organization  OrgOutput       `json:"organization"`
	PreviousOwner OrgMemberOutput `json:"previous_owner"`
	NewOwner      OrgMemberOutput `json:"new_owner"`
}

func normalizeOrganizationName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", &APIError{Status: 400, Code: "invalid_name", Message: "name は必須です"}
	}
	if len([]rune(name)) > maxOrganizationNameLength {
		return "", &APIError{Status: 400, Code: "invalid_name", Message: "name は120文字以内で指定してください"}
	}
	return name, nil
}

func (s *OrgService) lifecycleStore() (OrgLifecycleStore, error) {
	lifecycleStore, ok := s.Store.(OrgLifecycleStore)
	if !ok {
		return nil, fmt.Errorf("organization lifecycle store is not configured")
	}
	return lifecycleStore, nil
}

func (s *OrgService) UpdateOrganization(ctx context.Context, actorID, orgID uuid.UUID, name string) (OrgOutput, error) {
	if _, err := s.requireRole(ctx, actorID, orgID, "admin"); err != nil {
		return OrgOutput{}, err
	}
	normalizedName, err := normalizeOrganizationName(name)
	if err != nil {
		return OrgOutput{}, err
	}
	lifecycleStore, err := s.lifecycleStore()
	if err != nil {
		return OrgOutput{}, err
	}
	org, err := lifecycleStore.UpdateOrganizationName(ctx, orgID, normalizedName)
	if errors.Is(err, store.ErrNotFound) {
		return OrgOutput{}, &APIError{Status: 404, Code: "organization_not_found", Message: "organization が見つかりません"}
	}
	if err != nil {
		return OrgOutput{}, err
	}
	return OrgOutput{ID: org.ID, Name: org.Name, OwnerUserID: org.OwnerUserID}, nil
}

func (s *OrgService) TransferOwnership(ctx context.Context, actorID, orgID, targetUserID uuid.UUID) (OwnerTransferOutput, error) {
	if _, err := s.requireRole(ctx, actorID, orgID, "owner"); err != nil {
		return OwnerTransferOutput{}, err
	}
	if targetUserID == uuid.Nil {
		return OwnerTransferOutput{}, &APIError{Status: 400, Code: "invalid_user_id", Message: "target_user_id が不正です"}
	}
	if targetUserID == actorID {
		return OwnerTransferOutput{}, &APIError{Status: 409, Code: "cannot_transfer_to_self", Message: "自身へオーナー権限を移譲することはできません"}
	}
	target, err := s.Store.GetOrganizationMember(ctx, orgID, targetUserID)
	if errors.Is(err, store.ErrNotFound) {
		return OwnerTransferOutput{}, &APIError{Status: 404, Code: "member_not_found", Message: "移譲先メンバーが見つかりません"}
	}
	if err != nil {
		return OwnerTransferOutput{}, err
	}
	if target.Role != "admin" && target.Role != "member" {
		return OwnerTransferOutput{}, &APIError{Status: 409, Code: "invalid_owner_state", Message: "移譲先メンバーの権限状態が不正です"}
	}

	lifecycleStore, err := s.lifecycleStore()
	if err != nil {
		return OwnerTransferOutput{}, err
	}
	org, err := lifecycleStore.TransferOrganizationOwnership(ctx, orgID, actorID, targetUserID)
	if errors.Is(err, store.ErrNotFound) {
		return OwnerTransferOutput{}, &APIError{Status: 404, Code: "member_not_found", Message: "移譲先メンバーが見つかりません"}
	}
	if errors.Is(err, store.ErrConflict) {
		return OwnerTransferOutput{}, &APIError{Status: 409, Code: "ownership_changed", Message: "オーナー状態が更新されています。画面を再読み込みしてください"}
	}
	if err != nil {
		return OwnerTransferOutput{}, err
	}
	return OwnerTransferOutput{
		Organization:  OrgOutput{ID: org.ID, Name: org.Name, OwnerUserID: org.OwnerUserID},
		PreviousOwner: OrgMemberOutput{UserID: actorID, Role: "admin"},
		NewOwner:      OrgMemberOutput{UserID: targetUserID, Role: "owner"},
	}, nil
}

func (s *OrgService) LeaveOrganization(ctx context.Context, userID, orgID uuid.UUID) error {
	role, err := s.requireRole(ctx, userID, orgID, "member")
	if err != nil {
		return err
	}
	if role == "owner" {
		return &APIError{Status: 409, Code: "owner_must_transfer", Message: "オーナーは権限を移譲してから退出してください"}
	}
	lifecycleStore, err := s.lifecycleStore()
	if err != nil {
		return err
	}
	if err := lifecycleStore.LeaveOrganization(ctx, orgID, userID); errors.Is(err, store.ErrConflict) {
		return &APIError{Status: 409, Code: "owner_must_transfer", Message: "オーナーは権限を移譲してから退出してください"}
	} else if errors.Is(err, store.ErrNotFound) {
		return &APIError{Status: 404, Code: "member_not_found", Message: "member が見つかりません"}
	} else if err != nil {
		return err
	}

	if s.Billing != nil {
		if err := s.Billing.SyncSeatCountWithStripe(ctx, orgID); err != nil {
			return &APIError{
				Status:  502,
				Code:    "seat_sync_failed_after_leave",
				Message: "組織からの退出は完了しましたが、課金seatの同期に失敗しました。管理者に確認してください",
			}
		}
	}
	return nil
}
