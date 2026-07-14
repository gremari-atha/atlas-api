package redisrelay

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"atlas-api/internal/websocket"
	"github.com/redis/go-redis/v9"
)

type EmailEvent struct {
	TenantID      string `json:"tenant_id"`
	From          string `json:"from"`
	Date          string `json:"date"`
	Subject       string `json:"subject"`
	ExtractMethod string `json:"extract_method"`
	Data          string `json:"data"`
}

type EventRelay struct {
	redisClient *redis.Client
	wsHub       *websocket.Hub
}

func NewEventRelay(redisClient *redis.Client, wsHub *websocket.Hub) *EventRelay {
	return &EventRelay{
		redisClient: redisClient,
		wsHub:       wsHub,
	}
}

// Start spawns a background goroutine subscribing to Redis Pub/Sub events
func (er *EventRelay) Start(ctx context.Context) {
	pubsub := er.redisClient.Subscribe(ctx, "email_events:broadcast")

	go func() {
		defer pubsub.Close()
		slog.Info("Redis WebSocket Event Relay started, listening to channel: email_events:broadcast")

		ch := pubsub.Channel()
		for {
			select {
			case <-ctx.Done():
				slog.Info("Redis WebSocket Event Relay shutting down...")
				return
			case msg, ok := <-ch:
				if !ok {
					return
				}

				var event EmailEvent
				if err := json.Unmarshal([]byte(msg.Payload), &event); err != nil {
					slog.Error("Failed to unmarshal Redis broadcast event", "err", err)
					continue
				}

				// Sanitize the email address matching existing emailforward format (e.g. info_netflix_com)
				replacer := strings.NewReplacer(".", "_", "@", "_")
				sanitizedEmail := replacer.Replace(strings.ToLower(event.From))
				
				eventName := sanitizedEmail

				slog.Debug("Relaying email event to WebSockets", "eventName", eventName, "tenantID", event.TenantID)

				// Broadcast via Monolith WebSocket Hub
				er.wsHub.BroadcastEvent(eventName, map[string]interface{}{
					"from":           event.From,
					"date":           event.Date,
					"subject":        event.Subject,
					"extract_method": event.ExtractMethod,
					"data":           event.Data,
				})
			}
		}
	}()
}
