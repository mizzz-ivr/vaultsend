package mail

import (
	"fmt"
	"html/template"
	"strings"
	"time"

	"github.com/example/vaultsend/internal/queue"
)

type shipmentMailTemplateData struct {
	Subject      string
	Message      string
	DownloadURL  string
	ExpiresAtJST string
}

type organizationInvitationTemplateData struct {
	OrganizationName string
	InvitedByEmail    string
	Role              string
	InvitationURL     string
	ExpiresAt         string
}

var shipmentHTMLTemplate = template.Must(template.New("shipment_html").Parse(`<!doctype html>
<html>
  <body>
    <p>Secure Send からファイル共有のお知らせです。</p>
    <p><strong>件名:</strong> {{.Subject}}</p>
    {{if .Message}}<p><strong>メッセージ:</strong><br>{{.Message}}</p>{{end}}
    <p><a href="{{.DownloadURL}}">ダウンロードページを開く</a></p>
    {{if .ExpiresAtJST}}<p>有効期限: {{.ExpiresAtJST}}</p>{{end}}
    <p>※このメールに心当たりがない場合は破棄してください。</p>
  </body>
</html>`))

var organizationInvitationHTMLTemplate = template.Must(template.New("organization_invitation_html").Parse(`<!doctype html>
<html>
  <body>
    <p>VaultSend の組織へ招待されました。</p>
    <p><strong>組織:</strong> {{.OrganizationName}}</p>
    <p><strong>権限:</strong> {{.Role}}</p>
    {{if .InvitedByEmail}}<p><strong>招待者:</strong> {{.InvitedByEmail}}</p>{{end}}
    <p><a href="{{.InvitationURL}}">招待を確認する</a></p>
    {{if .ExpiresAt}}<p>有効期限: {{.ExpiresAt}}</p>{{end}}
    <p>招待先と同じメールアドレスのアカウントでログインしてください。</p>
    <p>※このメールに心当たりがない場合は破棄してください。</p>
  </body>
</html>`))

func BuildShipmentNotification(frontendURL string, msg queue.MailNotification) (Body, error) {
	base := strings.TrimRight(frontendURL, "/")
	downloadURL := fmt.Sprintf("%s/r/%s", base, msg.Token)
	expires := ""
	if msg.ExpiresAt != nil {
		expires = msg.ExpiresAt.UTC().Format(time.RFC3339)
	}
	content := ""
	if msg.Message != nil {
		content = *msg.Message
	}
	data := shipmentMailTemplateData{
		Subject:      msg.Subject,
		Message:      content,
		DownloadURL:  downloadURL,
		ExpiresAtJST: expires,
	}

	var htmlBuilder strings.Builder
	if err := shipmentHTMLTemplate.Execute(&htmlBuilder, data); err != nil {
		return Body{}, fmt.Errorf("execute html template: %w", err)
	}

	text := fmt.Sprintf("Secure Send からファイル共有のお知らせです。\n\n件名: %s\n", data.Subject)
	if data.Message != "" {
		text += fmt.Sprintf("メッセージ:\n%s\n\n", data.Message)
	}
	text += fmt.Sprintf("ダウンロードURL: %s\n", data.DownloadURL)
	if data.ExpiresAtJST != "" {
		text += fmt.Sprintf("有効期限: %s\n", data.ExpiresAtJST)
	}
	text += "\n※このメールに心当たりがない場合は破棄してください。\n"

	return Body{Text: text, HTML: htmlBuilder.String()}, nil
}

func BuildOrganizationInvitation(frontendURL string, msg queue.MailNotification) (Body, error) {
	base := strings.TrimRight(frontendURL, "/")
	if base == "" || strings.TrimSpace(msg.Token) == "" || strings.TrimSpace(msg.OrganizationName) == "" {
		return Body{}, fmt.Errorf("organization invitation payload is incomplete")
	}
	role := "メンバー"
	if msg.InvitationRole == "admin" {
		role = "管理者"
	}
	expires := ""
	if msg.ExpiresAt != nil {
		expires = msg.ExpiresAt.UTC().Format(time.RFC3339)
	}
	data := organizationInvitationTemplateData{
		OrganizationName: msg.OrganizationName,
		InvitedByEmail:    msg.InvitedByEmail,
		Role:              role,
		InvitationURL:     fmt.Sprintf("%s/invite/%s", base, msg.Token),
		ExpiresAt:         expires,
	}

	var htmlBuilder strings.Builder
	if err := organizationInvitationHTMLTemplate.Execute(&htmlBuilder, data); err != nil {
		return Body{}, fmt.Errorf("execute organization invitation html template: %w", err)
	}
	text := fmt.Sprintf("VaultSend の組織へ招待されました。\n\n組織: %s\n権限: %s\n", data.OrganizationName, data.Role)
	if data.InvitedByEmail != "" {
		text += fmt.Sprintf("招待者: %s\n", data.InvitedByEmail)
	}
	text += fmt.Sprintf("招待URL: %s\n", data.InvitationURL)
	if data.ExpiresAt != "" {
		text += fmt.Sprintf("有効期限: %s\n", data.ExpiresAt)
	}
	text += "\n招待先と同じメールアドレスのアカウントでログインしてください。\n"
	text += "※このメールに心当たりがない場合は破棄してください。\n"
	return Body{Text: text, HTML: htmlBuilder.String()}, nil
}
