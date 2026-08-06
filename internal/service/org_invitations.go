package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/example/vaultsend/internal/queue"
	"github.com/example/vaultsend/internal/store"
	"github.com/google/uuid"
)

const defaultOrganizationInvitationTTL = 7 * 24 * time.Hour

type OrgInvitationStore interface {
	CreateOrganizationInvitation(ctx context.Context, arg store.CreateOrganizationInvitationParams) (store.OrganizationInvitation, error)
	ListOrganizationInvitations(ctx context.Context, organizationID uuid.UUID) ([]store.OrganizationInvitation, error)
	GetOrganizationInvitationByID(ctx context.Context, organizationID, invitationID uuid.UUID) (store.OrganizationInvitation, error)
	GetOrganizationInvitationByTokenHash(ctx context.Context, tokenHash string) (store.OrganizationInvitation, error)
	CountPendingOrganizationInvitations(ctx context.Context, organizationID uuid.UUID) (int64, error)
	RevokeOrganizationInvitation(ctx context.Context, organizationID, invitationID uuid.UUID) error
	RefreshOrganizationInvitation(ctx context.Context, arg store.RefreshOrganizationInvitationParams) (store.OrganizationInvitation, error)
	MarkOrganizationInvitationQueued(ctx context.Context, invitationID uuid.UUID, queuedAt time.Time) error
	AcceptOrganizationInvitation(ctx context.Context, arg store.AcceptOrganizationInvitationParams) (store.OrganizationMember, error)
	GetUserByEmail(ctx context.Context, emailNormalized string) (store.User, error)
}

type OrganizationInvitationOutput struct {
	ID             uuid.UUID  `json:"id"`
	OrganizationID uuid.UUID  `json:"organization_id"`
	Email          string     `json:"email"`
	Role           string     `json:"role"`
	Status         string     `json:"status"`
	ExpiresAt      time.Time  `json:"expires_at"`
	LastSentAt     *time.Time `json:"last_sent_at,omitempty"`
	AcceptedAt     *time.Time `json:"accepted_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

type OrganizationInvitationInspectOutput struct {
	OrganizationID   uuid.UUID `json:"organization_id"`
	OrganizationName string    `json:"organization_name"`
	EmailMasked      string    `json:"email_masked"`
	Role             string    `json:"role"`
	Status           string    `json:"status"`
	ExpiresAt        time.Time `json:"expires_at"`
}

type OrganizationInvitationAcceptOutput struct {
	Organization OrgOutput       `json:"organization"`
	Member       OrgMemberOutput `json:"member"`
	AlreadyAccepted bool         `json:"already_accepted"`
}

func (s *OrgService) CreateInvitation(ctx context.Context, actorID uuid.UUID, actorEmail string, orgID uuid.UUID, email, role string) (OrganizationInvitationOutput, error) {
	if _, err := s.requireRole(ctx, actorID, orgID, "admin"); err != nil {
		return OrganizationInvitationOutput{}, err
	}
	invitationStore, err := s.invitationStore()
	if err != nil {
		return OrganizationInvitationOutput{}, err
	}
	if s.Queue == nil {
		return OrganizationInvitationOutput{}, &APIError{Status: 503, Code: "invitation_mail_unavailable", Message: "招待メールを送信できません"}
	}

	normalizedEmail, err := normalizeAuthEmail(email)
	if err != nil {
		return OrganizationInvitationOutput{}, &APIError{Status: 400, Code: "invalid_email", Message: "email の形式が不正です"}
	}
	role = strings.ToLower(strings.TrimSpace(role))
	if role != "admin" && role != "member" {
		return OrganizationInvitationOutput{}, &APIError{Status: 400, Code: "invalid_role", Message: "role は admin または member を指定してください"}
	}

	if user, userErr := invitationStore.GetUserByEmail(ctx, normalizedEmail); userErr == nil {
		if _, memberErr := s.Store.GetOrganizationMember(ctx, orgID, user.ID); memberErr == nil {
			return OrganizationInvitationOutput{}, &APIError{Status: 409, Code: "member_exists", Message: "このユーザーは既に所属しています"}
		} else if !errors.Is(memberErr, store.ErrNotFound) {
			return OrganizationInvitationOutput{}, memberErr
		}
	} else if !errors.Is(userErr, store.ErrNotFound) {
		return OrganizationInvitationOutput{}, userErr
	}

	if err := s.ensureInvitationSeatAvailable(ctx, invitationStore, orgID); err != nil {
		return OrganizationInvitationOutput{}, err
	}

	plainToken, tokenHash, err := generateOrganizationInvitationToken()
	if err != nil {
		return OrganizationInvitationOutput{}, fmt.Errorf("generate organization invitation token: %w", err)
	}
	now := s.invitationNow()
	invitation, err := invitationStore.CreateOrganizationInvitation(ctx, store.CreateOrganizationInvitationParams{
		OrganizationID:  orgID,
		Email:           strings.TrimSpace(email),
		EmailNormalized: normalizedEmail,
		Role:            role,
		TokenHash:       tokenHash,
		InvitedByUserID: actorID,
		ExpiresAt:       now.Add(s.invitationTTL()),
	})
	if errors.Is(err, store.ErrConflict) {
		return OrganizationInvitationOutput{}, &APIError{Status: 409, Code: "invitation_exists", Message: "このメールアドレスには有効な招待が既にあります"}
	}
	if err != nil {
		return OrganizationInvitationOutput{}, fmt.Errorf("create organization invitation: %w", err)
	}

	org, err := s.Store.GetOrgByID(ctx, orgID)
	if err != nil {
		_ = invitationStore.RevokeOrganizationInvitation(ctx, orgID, invitation.ID)
		return OrganizationInvitationOutput{}, err
	}
	if err := s.enqueueInvitation(ctx, invitation, org.Name, actorEmail, plainToken); err != nil {
		_ = invitationStore.RevokeOrganizationInvitation(ctx, orgID, invitation.ID)
		return OrganizationInvitationOutput{}, err
	}
	queuedAt := s.invitationNow()
	if markErr := invitationStore.MarkOrganizationInvitationQueued(ctx, invitation.ID, queuedAt); markErr == nil {
		invitation.LastSentAt = &queuedAt
	}
	return toOrganizationInvitationOutput(invitation, now), nil
}

func (s *OrgService) ListInvitations(ctx context.Context, actorID, orgID uuid.UUID) ([]OrganizationInvitationOutput, error) {
	if _, err := s.requireRole(ctx, actorID, orgID, "admin"); err != nil {
		return nil, err
	}
	invitationStore, err := s.invitationStore()
	if err != nil {
		return nil, err
	}
	items, err := invitationStore.ListOrganizationInvitations(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("list organization invitations: %w", err)
	}
	now := s.invitationNow()
	out := make([]OrganizationInvitationOutput, 0, len(items))
	for _, invitation := range items {
		out = append(out, toOrganizationInvitationOutput(invitation, now))
	}
	return out, nil
}

func (s *OrgService) InspectInvitation(ctx context.Context, plainToken string) (OrganizationInvitationInspectOutput, error) {
	invitation, org, err := s.resolveInvitation(ctx, plainToken)
	if err != nil {
		return OrganizationInvitationInspectOutput{}, err
	}
	return OrganizationInvitationInspectOutput{
		OrganizationID:   org.ID,
		OrganizationName: org.Name,
		EmailMasked:      maskInvitationEmail(invitation.Email),
		Role:             invitation.Role,
		Status:           invitationStatus(invitation, s.invitationNow()),
		ExpiresAt:        invitation.ExpiresAt,
	}, nil
}

func (s *OrgService) AcceptInvitation(ctx context.Context, actorID uuid.UUID, actorEmail, plainToken string) (OrganizationInvitationAcceptOutput, error) {
	invitationStore, err := s.invitationStore()
	if err != nil {
		return OrganizationInvitationAcceptOutput{}, err
	}
	invitation, org, err := s.resolveInvitation(ctx, plainToken)
	if err != nil {
		return OrganizationInvitationAcceptOutput{}, err
	}
	normalizedActorEmail, err := normalizeAuthEmail(actorEmail)
	if err != nil || normalizedActorEmail != invitation.EmailNormalized {
		return OrganizationInvitationAcceptOutput{}, &APIError{Status: 403, Code: "invitation_email_mismatch", Message: "招待先とログイン中のメールアドレスが一致しません"}
	}

	if invitation.Status == "accepted" {
		if invitation.AcceptedByUserID != nil && *invitation.AcceptedByUserID == actorID {
			member, memberErr := s.Store.GetOrganizationMember(ctx, invitation.OrganizationID, actorID)
			if memberErr != nil {
				return OrganizationInvitationAcceptOutput{}, memberErr
			}
			if s.Billing != nil {
				_ = s.Billing.SyncSeatCountWithStripe(ctx, invitation.OrganizationID)
			}
			return invitationAcceptOutput(org, member, true), nil
		}
		return OrganizationInvitationAcceptOutput{}, &APIError{Status: 409, Code: "invitation_already_accepted", Message: "この招待は既に使用されています"}
	}
	status := invitationStatus(invitation, s.invitationNow())
	if status == "expired" {
		return OrganizationInvitationAcceptOutput{}, &APIError{Status: 410, Code: "invitation_expired", Message: "招待の有効期限が切れています"}
	}
	if status == "revoked" {
		return OrganizationInvitationAcceptOutput{}, &APIError{Status: 410, Code: "invitation_revoked", Message: "招待は取り消されています"}
	}

	if _, memberErr := s.Store.GetOrganizationMember(ctx, invitation.OrganizationID, actorID); memberErr == nil {
		return OrganizationInvitationAcceptOutput{}, &APIError{Status: 409, Code: "member_exists", Message: "既にこの組織へ所属しています"}
	} else if !errors.Is(memberErr, store.ErrNotFound) {
		return OrganizationInvitationAcceptOutput{}, memberErr
	}
	if s.Billing != nil {
		seatLimit, limitErr := s.Billing.GetSeatLimit(ctx, invitation.OrganizationID)
		if limitErr != nil {
			return OrganizationInvitationAcceptOutput{}, limitErr
		}
		usage, usageErr := s.Billing.GetCurrentSeatUsage(ctx, invitation.OrganizationID)
		if usageErr != nil {
			return OrganizationInvitationAcceptOutput{}, usageErr
		}
		if usage >= seatLimit {
			return OrganizationInvitationAcceptOutput{}, newPlanLimitError("SEAT_LIMIT", "メンバー数の上限に達しています")
		}
	}

	member, err := invitationStore.AcceptOrganizationInvitation(ctx, store.AcceptOrganizationInvitationParams{
		InvitationID: invitation.ID,
		TokenHash:    invitation.TokenHash,
		UserID:       actorID,
	})
	if errors.Is(err, store.ErrConflict) {
		return OrganizationInvitationAcceptOutput{}, &APIError{Status: 409, Code: "invitation_conflict", Message: "招待を承認できませんでした。状態を再確認してください"}
	}
	if errors.Is(err, store.ErrNotFound) {
		return OrganizationInvitationAcceptOutput{}, &APIError{Status: 404, Code: "invitation_not_found", Message: "招待が見つかりません"}
	}
	if err != nil {
		return OrganizationInvitationAcceptOutput{}, fmt.Errorf("accept organization invitation: %w", err)
	}
	if s.Billing != nil {
		if syncErr := s.Billing.SyncSeatCountWithStripe(ctx, invitation.OrganizationID); syncErr != nil {
			return OrganizationInvitationAcceptOutput{}, syncErr
		}
	}
	return invitationAcceptOutput(org, member, false), nil
}

func (s *OrgService) ResendInvitation(ctx context.Context, actorID uuid.UUID, actorEmail string, orgID, invitationID uuid.UUID) (OrganizationInvitationOutput, error) {
	if _, err := s.requireRole(ctx, actorID, orgID, "admin"); err != nil {
		return OrganizationInvitationOutput{}, err
	}
	invitationStore, err := s.invitationStore()
	if err != nil {
		return OrganizationInvitationOutput{}, err
	}
	if s.Queue == nil {
		return OrganizationInvitationOutput{}, &APIError{Status: 503, Code: "invitation_mail_unavailable", Message: "招待メールを送信できません"}
	}
	current, err := invitationStore.GetOrganizationInvitationByID(ctx, orgID, invitationID)
	if errors.Is(err, store.ErrNotFound) {
		return OrganizationInvitationOutput{}, &APIError{Status: 404, Code: "invitation_not_found", Message: "招待が見つかりません"}
	}
	if err != nil {
		return OrganizationInvitationOutput{}, err
	}
	if current.Status != "pending" {
		return OrganizationInvitationOutput{}, &APIError{Status: 409, Code: "invitation_not_pending", Message: "承認済みまたは取消済みの招待は再送できません"}
	}

	plainToken, tokenHash, err := generateOrganizationInvitationToken()
	if err != nil {
		return OrganizationInvitationOutput{}, err
	}
	now := s.invitationNow()
	invitation, err := invitationStore.RefreshOrganizationInvitation(ctx, store.RefreshOrganizationInvitationParams{
		InvitationID:   invitationID,
		OrganizationID: orgID,
		TokenHash:      tokenHash,
		ExpiresAt:      now.Add(s.invitationTTL()),
	})
	if err != nil {
		return OrganizationInvitationOutput{}, err
	}
	org, err := s.Store.GetOrgByID(ctx, orgID)
	if err != nil {
		return OrganizationInvitationOutput{}, err
	}
	if err := s.enqueueInvitation(ctx, invitation, org.Name, actorEmail, plainToken); err != nil {
		return OrganizationInvitationOutput{}, err
	}
	queuedAt := s.invitationNow()
	if markErr := invitationStore.MarkOrganizationInvitationQueued(ctx, invitation.ID, queuedAt); markErr == nil {
		invitation.LastSentAt = &queuedAt
	}
	return toOrganizationInvitationOutput(invitation, now), nil
}

func (s *OrgService) RevokeInvitation(ctx context.Context, actorID, orgID, invitationID uuid.UUID) error {
	if _, err := s.requireRole(ctx, actorID, orgID, "admin"); err != nil {
		return err
	}
	invitationStore, err := s.invitationStore()
	if err != nil {
		return err
	}
	if err := invitationStore.RevokeOrganizationInvitation(ctx, orgID, invitationID); errors.Is(err, store.ErrNotFound) {
		return &APIError{Status: 404, Code: "invitation_not_found", Message: "有効な招待が見つかりません"}
	} else if err != nil {
		return err
	}
	return nil
}

func (s *OrgService) ensureInvitationSeatAvailable(ctx context.Context, invitationStore OrgInvitationStore, orgID uuid.UUID) error {
	if s.Billing == nil {
		return nil
	}
	seatLimit, err := s.Billing.GetSeatLimit(ctx, orgID)
	if err != nil {
		return err
	}
	usage, err := s.Billing.GetCurrentSeatUsage(ctx, orgID)
	if err != nil {
		return err
	}
	pending, err := invitationStore.CountPendingOrganizationInvitations(ctx, orgID)
	if err != nil {
		return err
	}
	if usage+pending >= seatLimit {
		return newPlanLimitError("SEAT_LIMIT", "招待を含むメンバー数がseat上限に達しています")
	}
	return nil
}

func (s *OrgService) resolveInvitation(ctx context.Context, plainToken string) (store.OrganizationInvitation, store.Organization, error) {
	plainToken = strings.TrimSpace(plainToken)
	if plainToken == "" || len(plainToken) > 256 {
		return store.OrganizationInvitation{}, store.Organization{}, &APIError{Status: 404, Code: "invitation_not_found", Message: "招待が見つかりません"}
	}
	invitationStore, err := s.invitationStore()
	if err != nil {
		return store.OrganizationInvitation{}, store.Organization{}, err
	}
	hash := sha256.Sum256([]byte(plainToken))
	invitation, err := invitationStore.GetOrganizationInvitationByTokenHash(ctx, hex.EncodeToString(hash[:]))
	if errors.Is(err, store.ErrNotFound) {
		return store.OrganizationInvitation{}, store.Organization{}, &APIError{Status: 404, Code: "invitation_not_found", Message: "招待が見つかりません"}
	}
	if err != nil {
		return store.OrganizationInvitation{}, store.Organization{}, err
	}
	org, err := s.Store.GetOrgByID(ctx, invitation.OrganizationID)
	if err != nil {
		return store.OrganizationInvitation{}, store.Organization{}, err
	}
	return invitation, org, nil
}

func (s *OrgService) enqueueInvitation(ctx context.Context, invitation store.OrganizationInvitation, organizationName, inviterEmail, plainToken string) error {
	subject := fmt.Sprintf("VaultSend: %s への招待", organizationName)
	invitationID := invitation.ID
	if err := s.Queue.EnqueueMail(ctx, queue.MailNotification{
		Template:          "organization_invitation",
		InvitationID:      &invitationID,
		Email:             invitation.Email,
		Token:             plainToken,
		Subject:           subject,
		OrganizationName:  organizationName,
		InvitationRole:    invitation.Role,
		InvitedByEmail:    strings.TrimSpace(inviterEmail),
		ExpiresAt:         &invitation.ExpiresAt,
	}); err != nil {
		return &APIError{Status: 503, Code: "invitation_mail_unavailable", Message: "招待メールをキューへ登録できませんでした"}
	}
	return nil
}

func (s *OrgService) invitationStore() (OrgInvitationStore, error) {
	if s.InvitationStore != nil {
		return s.InvitationStore, nil
	}
	if invitationStore, ok := s.Store.(OrgInvitationStore); ok {
		return invitationStore, nil
	}
	return nil, &APIError{Status: 503, Code: "invitation_unavailable", Message: "組織招待を利用できません"}
}

func (s *OrgService) invitationNow() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *OrgService) invitationTTL() time.Duration {
	if s.InvitationTTL > 0 {
		return s.InvitationTTL
	}
	return defaultOrganizationInvitationTTL
}

func generateOrganizationInvitationToken() (string, string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	plain := base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(plain))
	return plain, hex.EncodeToString(hash[:]), nil
}

func invitationStatus(invitation store.OrganizationInvitation, now time.Time) string {
	if invitation.Status == "pending" && !invitation.ExpiresAt.After(now) {
		return "expired"
	}
	return invitation.Status
}

func toOrganizationInvitationOutput(invitation store.OrganizationInvitation, now time.Time) OrganizationInvitationOutput {
	return OrganizationInvitationOutput{
		ID:             invitation.ID,
		OrganizationID: invitation.OrganizationID,
		Email:          invitation.Email,
		Role:           invitation.Role,
		Status:         invitationStatus(invitation, now),
		ExpiresAt:      invitation.ExpiresAt,
		LastSentAt:     invitation.LastSentAt,
		AcceptedAt:     invitation.AcceptedAt,
		CreatedAt:      invitation.CreatedAt,
	}
}

func invitationAcceptOutput(org store.Organization, member store.OrganizationMember, alreadyAccepted bool) OrganizationInvitationAcceptOutput {
	return OrganizationInvitationAcceptOutput{
		Organization: OrgOutput{ID: org.ID, Name: org.Name, OwnerUserID: org.OwnerUserID},
		Member:       OrgMemberOutput{UserID: member.UserID, Role: member.Role},
		AlreadyAccepted: alreadyAccepted,
	}
}

func maskInvitationEmail(email string) string {
	parts := strings.Split(strings.TrimSpace(email), "@")
	if len(parts) != 2 || parts[0] == "" {
		return "***"
	}
	local := []rune(parts[0])
	return string(local[0]) + "***@" + parts[1]
}
