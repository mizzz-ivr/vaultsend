package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/example/vaultsend/internal/queue"
	"github.com/example/vaultsend/internal/store"
	"github.com/google/uuid"
)

type fakeInvitationStore struct {
	orgStore    *fakeOrgStore
	invitations map[uuid.UUID]store.OrganizationInvitation
	users       map[string]store.User
	pending     int64
	lastCreate  store.CreateOrganizationInvitationParams
	acceptCalls int
}

func (f *fakeInvitationStore) CreateOrganizationInvitation(_ context.Context, arg store.CreateOrganizationInvitationParams) (store.OrganizationInvitation, error) {
	f.lastCreate = arg
	for _, invitation := range f.invitations {
		if invitation.OrganizationID == arg.OrganizationID && invitation.EmailNormalized == arg.EmailNormalized && invitation.Status == "pending" {
			return store.OrganizationInvitation{}, store.ErrConflict
		}
	}
	invitation := store.OrganizationInvitation{
		ID:              uuid.New(),
		OrganizationID:  arg.OrganizationID,
		Email:           arg.Email,
		EmailNormalized: arg.EmailNormalized,
		Role:            arg.Role,
		TokenHash:       arg.TokenHash,
		Status:          "pending",
		InvitedByUserID: arg.InvitedByUserID,
		ExpiresAt:       arg.ExpiresAt,
		CreatedAt:       time.Now().UTC(),
	}
	f.invitations[invitation.ID] = invitation
	return invitation, nil
}

func (f *fakeInvitationStore) ListOrganizationInvitations(_ context.Context, organizationID uuid.UUID) ([]store.OrganizationInvitation, error) {
	items := make([]store.OrganizationInvitation, 0)
	for _, invitation := range f.invitations {
		if invitation.OrganizationID == organizationID {
			items = append(items, invitation)
		}
	}
	return items, nil
}

func (f *fakeInvitationStore) GetOrganizationInvitationByID(_ context.Context, organizationID, invitationID uuid.UUID) (store.OrganizationInvitation, error) {
	invitation, ok := f.invitations[invitationID]
	if !ok || invitation.OrganizationID != organizationID {
		return store.OrganizationInvitation{}, store.ErrNotFound
	}
	return invitation, nil
}

func (f *fakeInvitationStore) GetOrganizationInvitationByTokenHash(_ context.Context, tokenHash string) (store.OrganizationInvitation, error) {
	for _, invitation := range f.invitations {
		if invitation.TokenHash == tokenHash {
			return invitation, nil
		}
	}
	return store.OrganizationInvitation{}, store.ErrNotFound
}

func (f *fakeInvitationStore) CountPendingOrganizationInvitations(_ context.Context, _ uuid.UUID) (int64, error) {
	return f.pending, nil
}

func (f *fakeInvitationStore) RevokeOrganizationInvitation(_ context.Context, organizationID, invitationID uuid.UUID) error {
	invitation, ok := f.invitations[invitationID]
	if !ok || invitation.OrganizationID != organizationID || invitation.Status != "pending" {
		return store.ErrNotFound
	}
	invitation.Status = "revoked"
	f.invitations[invitationID] = invitation
	return nil
}

func (f *fakeInvitationStore) RefreshOrganizationInvitation(_ context.Context, arg store.RefreshOrganizationInvitationParams) (store.OrganizationInvitation, error) {
	invitation, ok := f.invitations[arg.InvitationID]
	if !ok || invitation.OrganizationID != arg.OrganizationID || invitation.Status != "pending" {
		return store.OrganizationInvitation{}, store.ErrNotFound
	}
	invitation.TokenHash = arg.TokenHash
	invitation.ExpiresAt = arg.ExpiresAt
	invitation.LastSentAt = nil
	f.invitations[arg.InvitationID] = invitation
	return invitation, nil
}

func (f *fakeInvitationStore) MarkOrganizationInvitationQueued(_ context.Context, invitationID uuid.UUID, queuedAt time.Time) error {
	invitation, ok := f.invitations[invitationID]
	if !ok {
		return store.ErrNotFound
	}
	invitation.LastSentAt = &queuedAt
	f.invitations[invitationID] = invitation
	return nil
}

func (f *fakeInvitationStore) AcceptOrganizationInvitation(_ context.Context, arg store.AcceptOrganizationInvitationParams) (store.OrganizationMember, error) {
	invitation, ok := f.invitations[arg.InvitationID]
	if !ok || invitation.TokenHash != arg.TokenHash || invitation.Status != "pending" {
		return store.OrganizationMember{}, store.ErrConflict
	}
	f.acceptCalls++
	member := store.OrganizationMember{
		ID:             uuid.New(),
		OrganizationID: invitation.OrganizationID,
		UserID:         arg.UserID,
		Role:           invitation.Role,
	}
	f.orgStore.members[arg.UserID] = member
	invitation.Status = "accepted"
	invitation.AcceptedByUserID = &arg.UserID
	acceptedAt := time.Now().UTC()
	invitation.AcceptedAt = &acceptedAt
	f.invitations[arg.InvitationID] = invitation
	return member, nil
}

func (f *fakeInvitationStore) GetUserByEmail(_ context.Context, emailNormalized string) (store.User, error) {
	user, ok := f.users[emailNormalized]
	if !ok {
		return store.User{}, store.ErrNotFound
	}
	return user, nil
}

type fakeInvitationQueue struct {
	messages []queue.MailNotification
	err      error
}

func (f *fakeInvitationQueue) EnqueueMail(_ context.Context, message queue.MailNotification) error {
	if f.err != nil {
		return f.err
	}
	f.messages = append(f.messages, message)
	return nil
}

func TestOrgInvitationCreateStoresOnlyHashedTokenAndQueuesPlainToken(t *testing.T) {
	owner := uuid.New()
	orgID := uuid.New()
	now := time.Date(2026, 8, 6, 0, 0, 0, 0, time.UTC)
	orgStore := &fakeOrgStore{
		org: store.Organization{ID: orgID, Name: "VaultSend開発部", OwnerUserID: owner},
		members: map[uuid.UUID]store.OrganizationMember{
			owner: {OrganizationID: orgID, UserID: owner, Role: "owner"},
		},
	}
	invitationStore := &fakeInvitationStore{
		orgStore:    orgStore,
		invitations: map[uuid.UUID]store.OrganizationInvitation{},
		users:       map[string]store.User{},
	}
	mailQueue := &fakeInvitationQueue{}
	svc := &OrgService{
		Store:           orgStore,
		Billing:         &fakeOrgBilling{seatLimit: 5, usage: 1},
		InvitationStore: invitationStore,
		Queue:           mailQueue,
		Now:             func() time.Time { return now },
	}

	out, err := svc.CreateInvitation(context.Background(), owner, "owner@example.com", orgID, " Member@Example.COM ", "member")
	if err != nil {
		t.Fatal(err)
	}
	if out.Email != "Member@Example.COM" || out.Status != "pending" {
		t.Fatalf("unexpected output: %#v", out)
	}
	if invitationStore.lastCreate.EmailNormalized != "member@example.com" {
		t.Fatalf("email normalization mismatch: %q", invitationStore.lastCreate.EmailNormalized)
	}
	if len(invitationStore.lastCreate.TokenHash) != 64 {
		t.Fatalf("token hash length mismatch: %d", len(invitationStore.lastCreate.TokenHash))
	}
	if invitationStore.lastCreate.ExpiresAt != now.Add(defaultOrganizationInvitationTTL) {
		t.Fatalf("expiration mismatch: %s", invitationStore.lastCreate.ExpiresAt)
	}
	if len(mailQueue.messages) != 1 {
		t.Fatalf("expected one queued message, got %d", len(mailQueue.messages))
	}
	plainToken := mailQueue.messages[0].Token
	if plainToken == "" || plainToken == invitationStore.lastCreate.TokenHash {
		t.Fatal("plain token must be queued and must not equal stored hash")
	}
	hash := sha256.Sum256([]byte(plainToken))
	if hex.EncodeToString(hash[:]) != invitationStore.lastCreate.TokenHash {
		t.Fatal("stored hash does not match queued token")
	}
}

func TestOrgInvitationCreateRejectsSeatLimitIncludingPendingInvitations(t *testing.T) {
	owner := uuid.New()
	orgID := uuid.New()
	orgStore := &fakeOrgStore{
		org: store.Organization{ID: orgID, Name: "Team", OwnerUserID: owner},
		members: map[uuid.UUID]store.OrganizationMember{
			owner: {OrganizationID: orgID, UserID: owner, Role: "owner"},
		},
	}
	invitationStore := &fakeInvitationStore{
		orgStore: orgStore, invitations: map[uuid.UUID]store.OrganizationInvitation{}, users: map[string]store.User{}, pending: 1,
	}
	svc := &OrgService{
		Store: orgStore, Billing: &fakeOrgBilling{seatLimit: 2, usage: 1}, InvitationStore: invitationStore, Queue: &fakeInvitationQueue{},
	}

	_, err := svc.CreateInvitation(context.Background(), owner, "owner@example.com", orgID, "member@example.com", "member")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "SEAT_LIMIT" {
		t.Fatalf("expected seat limit API error, got %#v", err)
	}
}

func TestOrgInvitationAcceptRequiresMatchingEmailAndAddsMember(t *testing.T) {
	owner := uuid.New()
	invitee := uuid.New()
	orgID := uuid.New()
	invitationID := uuid.New()
	plainToken := "organization-invitation-token"
	hash := sha256.Sum256([]byte(plainToken))
	tokenHash := hex.EncodeToString(hash[:])
	now := time.Date(2026, 8, 6, 0, 0, 0, 0, time.UTC)
	orgStore := &fakeOrgStore{
		org: store.Organization{ID: orgID, Name: "Team", OwnerUserID: owner},
		members: map[uuid.UUID]store.OrganizationMember{
			owner: {OrganizationID: orgID, UserID: owner, Role: "owner"},
		},
	}
	invitationStore := &fakeInvitationStore{
		orgStore: orgStore,
		invitations: map[uuid.UUID]store.OrganizationInvitation{
			invitationID: {
				ID: invitationID, OrganizationID: orgID, Email: "invitee@example.com", EmailNormalized: "invitee@example.com",
				Role: "member", TokenHash: tokenHash, Status: "pending", ExpiresAt: now.Add(time.Hour),
			},
		},
		users: map[string]store.User{},
	}
	billing := &fakeOrgBilling{seatLimit: 5, usage: 1}
	svc := &OrgService{Store: orgStore, Billing: billing, InvitationStore: invitationStore, Now: func() time.Time { return now }}

	_, err := svc.AcceptInvitation(context.Background(), invitee, "other@example.com", plainToken)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "invitation_email_mismatch" {
		t.Fatalf("expected email mismatch, got %#v", err)
	}
	if invitationStore.acceptCalls != 0 {
		t.Fatal("mismatched user must not consume invitation")
	}

	out, err := svc.AcceptInvitation(context.Background(), invitee, "Invitee@Example.com", plainToken)
	if err != nil {
		t.Fatal(err)
	}
	if out.Member.UserID != invitee || out.Member.Role != "member" || out.AlreadyAccepted {
		t.Fatalf("unexpected accept output: %#v", out)
	}
	if invitationStore.acceptCalls != 1 || billing.syncCalls != 1 {
		t.Fatalf("accept/sync calls mismatch: accept=%d sync=%d", invitationStore.acceptCalls, billing.syncCalls)
	}
}

func TestOrgInvitationResendRotatesToken(t *testing.T) {
	owner := uuid.New()
	orgID := uuid.New()
	invitationID := uuid.New()
	oldHash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	now := time.Date(2026, 8, 6, 0, 0, 0, 0, time.UTC)
	orgStore := &fakeOrgStore{
		org: store.Organization{ID: orgID, Name: "Team", OwnerUserID: owner},
		members: map[uuid.UUID]store.OrganizationMember{
			owner: {OrganizationID: orgID, UserID: owner, Role: "owner"},
		},
	}
	invitationStore := &fakeInvitationStore{
		orgStore: orgStore,
		invitations: map[uuid.UUID]store.OrganizationInvitation{
			invitationID: {ID: invitationID, OrganizationID: orgID, Email: "member@example.com", EmailNormalized: "member@example.com", Role: "member", TokenHash: oldHash, Status: "pending", ExpiresAt: now.Add(time.Hour)},
		},
		users: map[string]store.User{},
	}
	mailQueue := &fakeInvitationQueue{}
	svc := &OrgService{Store: orgStore, InvitationStore: invitationStore, Queue: mailQueue, Now: func() time.Time { return now }}

	out, err := svc.ResendInvitation(context.Background(), owner, "owner@example.com", orgID, invitationID)
	if err != nil {
		t.Fatal(err)
	}
	updated := invitationStore.invitations[invitationID]
	if updated.TokenHash == oldHash {
		t.Fatal("resend must rotate token hash")
	}
	if out.ExpiresAt != now.Add(defaultOrganizationInvitationTTL) || len(mailQueue.messages) != 1 {
		t.Fatalf("unexpected resend result: out=%#v messages=%d", out, len(mailQueue.messages))
	}
}
