package queue

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// MailNotification は通知メールを非同期送信するためのキューイベント。
type MailNotification struct {
	Template          string     `json:"template,omitempty"`
	ShipmentID        uuid.UUID  `json:"shipment_id,omitempty"`
	RecipientID       uuid.UUID  `json:"recipient_id,omitempty"`
	NotificationEvent *int64     `json:"notification_event_id,omitempty"`
	NotificationType  string     `json:"notification_type,omitempty"`
	InvitationID      *uuid.UUID `json:"invitation_id,omitempty"`
	Email             string     `json:"email"`
	Token             string     `json:"token"`
	Subject           string     `json:"subject"`
	Message           *string    `json:"message,omitempty"`
	ExpiresAt         *time.Time `json:"expires_at,omitempty"`
	OrganizationName  string     `json:"organization_name,omitempty"`
	InvitationRole    string     `json:"invitation_role,omitempty"`
	InvitedByEmail    string     `json:"invited_by_email,omitempty"`
}

// Enqueuer はメール送信キュー投入の抽象。
type Enqueuer interface {
	EnqueueMail(ctx context.Context, msg MailNotification) error
}

// Consumer はワーカーが利用するメッセージ取得/ACKの抽象。
type Consumer interface {
	Receive(ctx context.Context, maxMessages int32, waitSeconds int32) ([]ReceivedMessage, error)
	Delete(ctx context.Context, receiptHandle string) error
}

// ReceivedMessage はキューから取得した1件のメッセージ。
type ReceivedMessage struct {
	MessageID     string
	Body          string
	ReceiptHandle string
}
