package webpush

import (
	"context"
	"time"

	"mindfs/server/internal/notify"
)

type Payload = notify.Payload
type SessionNotification = notify.SessionNotification
type ScheduledNotification = notify.ScheduledNotification

func (s *Service) NotifySession(ctx context.Context, in SessionNotification) {
	if s == nil || !s.Enabled() {
		return
	}
	payload := BuildSessionPayload(in)
	s.sendAsync(ctx, firstNonEmpty(in.EventID, payload.Tag), payload)
}

func testPayload() Payload {
	eventID := "test:" + time.Now().UTC().Format(time.RFC3339Nano)
	return Payload{
		Type:  "test",
		Title: "MindFS",
		Body:  "通知已开启",
		URL:   "./",
		Data:  map[string]any{"type": "test", "eventId": eventID},
	}
}

func BuildSessionPayload(in SessionNotification) Payload {
	return notify.BuildSessionPayload(in)
}

func BuildScheduledPayload(in ScheduledNotification) Payload {
	return notify.BuildScheduledPayload(in)
}
