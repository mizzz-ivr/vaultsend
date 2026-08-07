package mail

import (
	"strings"
	"testing"
	"time"

	"github.com/example/vaultsend/internal/queue"
)

func TestBuildOrganizationInvitation(t *testing.T) {
	expiresAt := time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC)
	body, err := BuildOrganizationInvitation("https://vaultsend.example.com/", queue.MailNotification{
		Template:         "organization_invitation",
		Email:            "member@example.com",
		Token:            "safe-token",
		Subject:          "VaultSend: Team A への招待",
		OrganizationName: "Team A",
		InvitationRole:   "admin",
		InvitedByEmail:   "owner@example.com",
		ExpiresAt:        &expiresAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"Team A",
		"管理者",
		"owner@example.com",
		"https://vaultsend.example.com/invite/safe-token",
		"2026-08-13T00:00:00Z",
	} {
		if !strings.Contains(body.Text, expected) {
			t.Fatalf("text bodyに%qが含まれていません: %s", expected, body.Text)
		}
		if !strings.Contains(body.HTML, expected) {
			t.Fatalf("html bodyに%qが含まれていません: %s", expected, body.HTML)
		}
	}
}

func TestBuildOrganizationInvitationRejectsIncompletePayload(t *testing.T) {
	_, err := BuildOrganizationInvitation("https://vaultsend.example.com", queue.MailNotification{})
	if err == nil {
		t.Fatal("不完全なpayloadでエラーを期待しました")
	}
}
