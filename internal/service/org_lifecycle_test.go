package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/example/vaultsend/internal/store"
	"github.com/google/uuid"
)

type fakeOrgLifecycleStore struct {
	*fakeOrgStore
	updateCalls   int
	transferCalls int
	leaveCalls    int
}

func (f *fakeOrgLifecycleStore) UpdateOrganizationName(ctx context.Context, orgID uuid.UUID, name string) (store.Organization, error) {
	if f.org.ID != orgID {
		return store.Organization{}, store.ErrNotFound
	}
	f.updateCalls++
	f.org.Name = name
	return f.org, nil
}

func (f *fakeOrgLifecycleStore) TransferOrganizationOwnership(ctx context.Context, orgID, currentOwnerID, targetUserID uuid.UUID) (store.Organization, error) {
	if f.org.ID != orgID {
		return store.Organization{}, store.ErrNotFound
	}
	if currentOwnerID == targetUserID || f.org.OwnerUserID != currentOwnerID {
		return store.Organization{}, store.ErrConflict
	}
	target, ok := f.members[targetUserID]
	if !ok {
		return store.Organization{}, store.ErrNotFound
	}
	if target.Role != "admin" && target.Role != "member" {
		return store.Organization{}, store.ErrConflict
	}
	current := f.members[currentOwnerID]
	current.Role = "admin"
	f.members[currentOwnerID] = current
	target.Role = "owner"
	f.members[targetUserID] = target
	f.org.OwnerUserID = targetUserID
	f.transferCalls++
	return f.org, nil
}

func (f *fakeOrgLifecycleStore) LeaveOrganization(ctx context.Context, orgID, userID uuid.UUID) error {
	if f.org.ID != orgID {
		return store.ErrNotFound
	}
	if f.org.OwnerUserID == userID {
		return store.ErrConflict
	}
	if _, ok := f.members[userID]; !ok {
		return store.ErrNotFound
	}
	delete(f.members, userID)
	f.leaveCalls++
	return nil
}

func TestOrgUpdateOrganization_RoleAndValidation(t *testing.T) {
	base := &fakeOrgStore{members: map[uuid.UUID]store.OrganizationMember{}}
	fs := &fakeOrgLifecycleStore{fakeOrgStore: base}
	svc := &OrgService{Store: fs}
	owner := uuid.New()
	org, err := svc.CreateOrg(context.Background(), owner, "Team A")
	if err != nil {
		t.Fatal(err)
	}
	admin := uuid.New()
	member := uuid.New()
	base.members[admin] = store.OrganizationMember{OrganizationID: org.ID, UserID: admin, Role: "admin"}
	base.members[member] = store.OrganizationMember{OrganizationID: org.ID, UserID: member, Role: "member"}

	updated, err := svc.UpdateOrganization(context.Background(), admin, org.ID, "  Team A Renamed  ")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "Team A Renamed" || fs.updateCalls != 1 {
		t.Fatalf("unexpected update: %#v calls=%d", updated, fs.updateCalls)
	}

	if _, err := svc.UpdateOrganization(context.Background(), member, org.ID, "Member Rename"); err == nil {
		t.Fatal("memberによるrenameは拒否されるべきです")
	}
	if _, err := svc.UpdateOrganization(context.Background(), owner, org.ID, strings.Repeat("あ", 121)); err == nil {
		t.Fatal("120文字超過のnameは拒否されるべきです")
	} else if apiErr, ok := err.(*APIError); !ok || apiErr.Code != "invalid_name" {
		t.Fatalf("unexpected error: %#v", err)
	}
}

func TestOrgTransferOwnership_UpdatesSingleOwner(t *testing.T) {
	base := &fakeOrgStore{members: map[uuid.UUID]store.OrganizationMember{}}
	fs := &fakeOrgLifecycleStore{fakeOrgStore: base}
	svc := &OrgService{Store: fs}
	owner := uuid.New()
	org, err := svc.CreateOrg(context.Background(), owner, "Transfer Team")
	if err != nil {
		t.Fatal(err)
	}
	target := uuid.New()
	base.members[target] = store.OrganizationMember{OrganizationID: org.ID, UserID: target, Role: "member"}

	out, err := svc.TransferOwnership(context.Background(), owner, org.ID, target)
	if err != nil {
		t.Fatal(err)
	}
	if out.Organization.OwnerUserID != target || out.NewOwner.Role != "owner" || out.PreviousOwner.Role != "admin" {
		t.Fatalf("unexpected output: %#v", out)
	}
	if base.members[owner].Role != "admin" || base.members[target].Role != "owner" || fs.transferCalls != 1 {
		t.Fatalf("roles were not transferred: owner=%#v target=%#v", base.members[owner], base.members[target])
	}

	if _, err := svc.TransferOwnership(context.Background(), owner, org.ID, owner); err == nil {
		t.Fatal("旧ownerは移譲後にowner操作できないため拒否されるべきです")
	}
	if _, err := svc.TransferOwnership(context.Background(), target, org.ID, target); err == nil {
		t.Fatal("self transferは拒否されるべきです")
	} else if apiErr, ok := err.(*APIError); !ok || apiErr.Code != "cannot_transfer_to_self" {
		t.Fatalf("unexpected self transfer error: %#v", err)
	}
}

func TestOrgLeaveOrganization_OwnerBlockedAndMemberLeaves(t *testing.T) {
	base := &fakeOrgStore{members: map[uuid.UUID]store.OrganizationMember{}}
	fs := &fakeOrgLifecycleStore{fakeOrgStore: base}
	billing := &fakeOrgBilling{seatLimit: 5, usage: 2}
	svc := &OrgService{Store: fs, Billing: billing}
	owner := uuid.New()
	org, err := svc.CreateOrg(context.Background(), owner, "Leave Team")
	if err != nil {
		t.Fatal(err)
	}
	member := uuid.New()
	base.members[member] = store.OrganizationMember{OrganizationID: org.ID, UserID: member, Role: "member"}

	if err := svc.LeaveOrganization(context.Background(), owner, org.ID); err == nil {
		t.Fatal("ownerの退出は拒否されるべきです")
	} else if apiErr, ok := err.(*APIError); !ok || apiErr.Code != "owner_must_transfer" {
		t.Fatalf("unexpected owner leave error: %#v", err)
	}

	if err := svc.LeaveOrganization(context.Background(), member, org.ID); err != nil {
		t.Fatal(err)
	}
	if _, exists := base.members[member]; exists || fs.leaveCalls != 1 || billing.syncCalls != 1 {
		t.Fatalf("member leave mismatch: exists=%v leaveCalls=%d syncCalls=%d", exists, fs.leaveCalls, billing.syncCalls)
	}
}

func TestOrgOwnerInvariant_AddAndRemovePaths(t *testing.T) {
	base := &fakeOrgStore{members: map[uuid.UUID]store.OrganizationMember{}}
	svc := &OrgService{Store: base}
	owner := uuid.New()
	org, err := svc.CreateOrg(context.Background(), owner, "Invariant Team")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := svc.AddMember(context.Background(), owner, org.ID, uuid.New(), "owner"); err == nil {
		t.Fatal("通常メンバー追加APIでownerを付与できてはいけません")
	} else if apiErr, ok := err.(*APIError); !ok || apiErr.Code != "invalid_role" {
		t.Fatalf("unexpected add owner error: %#v", err)
	}

	admin := uuid.New()
	base.members[admin] = store.OrganizationMember{OrganizationID: org.ID, UserID: admin, Role: "admin"}
	if err := svc.RemoveMember(context.Background(), admin, org.ID, owner); err == nil {
		t.Fatal("adminがownerを削除できてはいけません")
	} else if apiErr, ok := err.(*APIError); !ok || apiErr.Code != "cannot_remove_owner" {
		t.Fatalf("unexpected remove owner error: %#v", err)
	}
}

func TestOrgLifecycleStoreMissing(t *testing.T) {
	base := &fakeOrgStore{members: map[uuid.UUID]store.OrganizationMember{}}
	svc := &OrgService{Store: base}
	owner := uuid.New()
	org, err := svc.CreateOrg(context.Background(), owner, "No Lifecycle Store")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateOrganization(context.Background(), owner, org.ID, "Renamed"); err == nil {
		t.Fatal("lifecycle store未実装時はerrorになるべきです")
	} else if errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unexpected store error: %v", err)
	}
}
