package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/example/vaultsend/internal/mail"
	"github.com/example/vaultsend/internal/queue"
	"github.com/example/vaultsend/internal/store"
)

// MailWorker は SQS から通知イベントを読み出して SES 送信する。
type MailWorker struct {
	Queue       queue.Consumer
	Mailer      mail.Sender
	EventStore  NotificationEventStore
	FrontendURL string
	MaxMessages int32
	WaitSeconds int32
}

type NotificationEventStore interface {
	UpdateNotificationEventStatus(ctx context.Context, arg store.UpdateNotificationEventStatusParams) error
}

func (w *MailWorker) Run(ctx context.Context) error {
	if w.MaxMessages <= 0 {
		w.MaxMessages = 5
	}
	if w.WaitSeconds <= 0 {
		w.WaitSeconds = 20
	}

	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		messages, err := w.Queue.Receive(ctx, w.MaxMessages, w.WaitSeconds)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("poll mail queue: %w", err)
		}
		for _, m := range messages {
			if err := w.handleMessage(ctx, m); err != nil {
				log.Printf("mail worker: handle message failed id=%s err=%v", m.MessageID, err)
			}
		}
	}
}

func (w *MailWorker) handleMessage(ctx context.Context, m queue.ReceivedMessage) error {
	var payload queue.MailNotification
	if err := json.Unmarshal([]byte(m.Body), &payload); err != nil {
		if delErr := w.Queue.Delete(ctx, m.ReceiptHandle); delErr != nil {
			return fmt.Errorf("decode message: %w; delete poison message: %v", err, delErr)
		}
		return fmt.Errorf("decode message: %w", err)
	}

	body, err := w.buildBody(payload)
	if err != nil {
		return fmt.Errorf("build mail body: %w", err)
	}
	if err := w.Mailer.SendEmail(ctx, payload.Email, payload.Subject, body); err != nil {
		w.markNotificationFailed(ctx, payload, err)
		return fmt.Errorf("send email: %w", err)
	}
	w.markNotificationSent(ctx, payload)
	if err := w.Queue.Delete(ctx, m.ReceiptHandle); err != nil {
		return fmt.Errorf("delete message: %w", err)
	}
	return nil
}

func (w *MailWorker) buildBody(payload queue.MailNotification) (mail.Body, error) {
	if payload.Template == "organization_invitation" {
		return mail.BuildOrganizationInvitation(w.FrontendURL, payload)
	}
	return mail.BuildShipmentNotification(w.FrontendURL, payload)
}

func (w *MailWorker) markNotificationSent(ctx context.Context, payload queue.MailNotification) {
	if w.EventStore == nil || payload.NotificationEvent == nil {
		return
	}
	now := time.Now().UTC()
	if err := w.EventStore.UpdateNotificationEventStatus(ctx, store.UpdateNotificationEventStatusParams{
		EventID: *payload.NotificationEvent,
		Status:  "sent",
		SentAt:  &now,
	}); err != nil {
		log.Printf("mail worker: failed to update notification event to sent event_id=%d err=%v", *payload.NotificationEvent, err)
	}
}

func (w *MailWorker) markNotificationFailed(ctx context.Context, payload queue.MailNotification, sendErr error) {
	if w.EventStore == nil || payload.NotificationEvent == nil {
		return
	}
	now := time.Now().UTC()
	msg := sendErr.Error()
	if err := w.EventStore.UpdateNotificationEventStatus(ctx, store.UpdateNotificationEventStatusParams{
		EventID:      *payload.NotificationEvent,
		Status:       "failed",
		ErrorMessage: &msg,
		FailedAt:     &now,
	}); err != nil {
		log.Printf("mail worker: failed to update notification event to failed event_id=%d err=%v", *payload.NotificationEvent, err)
	}
}
