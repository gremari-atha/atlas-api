package websocket

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5/pgxpool"

	"atlas-api/internal/config"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for simplicity
	},
}

// Hub maintains the set of active clients and broadcasts messages to clients.
type Hub struct {
	dbPool     *pgxpool.Pool
	clients    map[string]*Client
	register   chan *Client
	unregister chan *Client
	events     map[string]map[string]*Client // eventName -> clientId -> Client
	mutex      sync.RWMutex
	dbTimeout  time.Duration
}

// NewHub constructs a new Connection Hub
func NewHub(dbPool *pgxpool.Pool) *Hub {
	return &Hub{
		dbPool:     dbPool,
		clients:    make(map[string]*Client),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		events:     make(map[string]map[string]*Client),
		dbTimeout:  config.GetEnvAsDurationMs("DB_TIMEOUT", 5000*time.Millisecond),
	}
}

// Run executes the hub registration loop
func (h *Hub) Run(ctx context.Context) {
	for {
		select {
		case client := <-h.register:
			h.mutex.Lock()
			h.clients[client.ID] = client
			h.mutex.Unlock()
			slog.Info("websocket client registered successfully", "client_id", client.ID, "name", client.Name, "tenant_id", client.TenantID)

		case client := <-h.unregister:
			h.mutex.Lock()
			if _, ok := h.clients[client.ID]; ok {
				delete(h.clients, client.ID)
				close(client.Send)
				// Clean subscriptions
				for eventName, subs := range h.events {
					delete(subs, client.ID)
					if len(subs) == 0 {
						delete(h.events, eventName)
					}
				}
				slog.Info("websocket client unregistered", "client_id", client.ID, "name", client.Name)
			}
			h.mutex.Unlock()

		case <-ctx.Done():
			return
		}
	}
}

// newUUID generates a crypto-random hex UUID-like string
func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// AuthenticateAndUpgrade upgrades HTTP request to WS and performs JWT authentication
func (h *Hub) AuthenticateAndUpgrade(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	name := r.URL.Query().Get("connection_name")
	connType := r.URL.Query().Get("connection_type")

	if token == "" || name == "" || connType == "" {
		http.Error(w, "missing required connection params (token, connection_name, connection_type)", http.StatusBadRequest)
		return
	}

	// 1. Decode token unverified to extract tenant_id
	claims := jwt.MapClaims{}
	parser := jwt.NewParser()
	_, _, err := parser.ParseUnverified(token, &claims)
	if err != nil {
		http.Error(w, "malformed token", http.StatusUnauthorized)
		return
	}

	tenantID, _ := claims["tenant_id"].(string)
	if tenantID == "" {
		http.Error(w, "invalid token payload: missing tenant_id", http.StatusUnauthorized)
		return
	}

	// 2. Query tenant secret from master.tenant
	var secret string
	err = h.dbPool.QueryRow(r.Context(), "SELECT secret FROM master.tenant WHERE id = $1", tenantID).Scan(&secret)
	if err != nil {
		slog.Error("failed to retrieve tenant credentials on ws handshake", "tenant_id", tenantID, "err", err)
		http.Error(w, "unauthorized tenant", http.StatusUnauthorized)
		return
	}

	// 3. Verify token signature with secret
	parsedToken, err := jwt.Parse(token, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(secret), nil
	})

	if err != nil || !parsedToken.Valid {
		http.Error(w, "invalid token signature", http.StatusUnauthorized)
		return
	}

	// 4. Upgrade connection
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("websocket upgrade failed", "err", err)
		return
	}

	clientID := newUUID()
	client := &Client{
		ID:             clientID,
		Hub:            h,
		Conn:           conn,
		Name:           name,
		TenantID:       tenantID,
		ConnectionType: connType,
		Inflight:       0,
		ConnectedAt:    time.Now().UnixMilli(),
		Send:           make(chan []byte, 256),
	}

	h.register <- client

	// Start pump routines
	go client.writePump()
	go client.readPump()

	// Send initial connected payload
	connectedPayload := map[string]interface{}{
		"event": "connected",
		"data": map[string]string{
			"id": clientID,
		},
	}
	bytes, _ := json.Marshal(connectedPayload)
	client.Send <- bytes
}

// BroadcastEvent sends an event broadcast frame to all clients subscribed to eventName
func (h *Hub) BroadcastEvent(eventName string, payload interface{}) {
	h.mutex.RLock()
	defer h.mutex.RUnlock()

	subs := h.events[eventName]
	if len(subs) == 0 {
		return
	}

	msg := map[string]interface{}{
		"event": "event",
		"data": map[string]interface{}{
			"eventName": eventName,
			"payload":   payload,
		},
	}

	bytes, err := json.Marshal(msg)
	if err != nil {
		slog.Error("failed to serialize broadcast event payload", "event", eventName, "err", err)
		return
	}

	for _, client := range subs {
		select {
		case client.Send <- bytes:
		default:
			// Buffer full, unregistering or skipping
			slog.Warn("client send buffer full, dropping message", "client_id", client.ID)
		}
	}
}

// DispatchTask selects an available BOT for the tenant and sends the task dispatch frame.
// Returns the dispatched bot client ID, or empty string if failed/no bot available.
func (h *Hub) DispatchTask(ctx context.Context, taskId string, tenantId string, module string, taskType string, payload interface{}) (string, error) {
	h.mutex.Lock()
	bot := h.getAvailableBot(tenantId)
	if bot == nil {
		h.mutex.Unlock()
		// Replicate NestJS behavior: Update database status to FAILED if no bot available
		isDbTask := regexp.MustCompile(`^\d+$`).MatchString(taskId)
		if isDbTask {
			_, err := h.dbPool.Exec(ctx,
				"UPDATE master.task_queue SET status = 'FAILED', error_message = 'no bot available to handle the task', updated_at = NOW() WHERE id = $1",
				taskId,
			)
			if err != nil {
				slog.Error("failed to update task queue on no bot available", "task_id", taskId, "err", err)
			}
		}

		h.BroadcastEvent("task:status:"+taskId, map[string]interface{}{
			"taskId":        taskId,
			"status":        "FAILED",
			"error_message": "no bot available to handle the task",
		})
		return "", fmt.Errorf("no bot available to handle the task")
	}

	bot.Inflight++
	h.mutex.Unlock()

	dispatchMsg := map[string]interface{}{
		"event": "task-dispatch",
		"data": map[string]interface{}{
			"taskId":     taskId,
			"module":     module,
			"type":       taskType,
			"maxRetries": 0,
			"payload":    payload,
		},
	}

	bytes, err := json.Marshal(dispatchMsg)
	if err != nil {
		return "", err
	}

	select {
	case bot.Send <- bytes:
		return bot.ID, nil
	default:
		return "", fmt.Errorf("failed to write to bot send channel")
	}
}

func (h *Hub) getAvailableBot(tenantId string) *Client {
	var candidates []*Client
	for _, c := range h.clients {
		if c.TenantID == tenantId && c.ConnectionType == "BOT" {
			candidates = append(candidates, c)
		}
	}

	if len(candidates) == 0 {
		return nil
	}

	if len(candidates) == 1 {
		return candidates[0]
	}

	// Sort candidates by inflight task count ascending, then by connectedAt timestamp ascending
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Inflight != candidates[j].Inflight {
			return candidates[i].Inflight < candidates[j].Inflight
		}
		return candidates[i].ConnectedAt < candidates[j].ConnectedAt
	})

	return candidates[0]
}

// handleIncomingEvent routes parsed WS frames from clients to appropriate handlers
func (h *Hub) handleIncomingEvent(client *Client, msg IncomingWSMessage) {
	switch msg.Event {
	case "task-accept":
		var data struct {
			TaskID string `json:"taskId"`
		}
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			return
		}
		h.handleTaskAccepted(client, data.TaskID)

	case "task-reject":
		var data struct {
			TaskID  string `json:"taskId"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			return
		}
		h.handleTaskRejected(client, data.TaskID, data.Message)

	case "task-done":
		var data struct {
			TaskID  string `json:"taskId"`
			Status  string `json:"status"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			return
		}
		h.handleTaskDone(client, data.TaskID, data.Status, data.Message)

	case "subscribe-event":
		var data struct {
			EventName string `json:"eventName"`
		}
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			return
		}
		if data.EventName == "" {
			h.sendError(client, "subscribe-event-error", "eventName in body required")
			return
		}
		h.subscribe(client, data.EventName)

	case "unsubscribe-event":
		var data struct {
			EventName string `json:"eventName"`
		}
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			return
		}
		if data.EventName == "" {
			h.sendError(client, "unsubscribe-event-error", "eventName in body required")
			return
		}
		h.unsubscribe(client, data.EventName)
	}
}

func (h *Hub) sendError(client *Client, event string, message string) {
	payload := map[string]interface{}{
		"event": event,
		"data": map[string]string{
			"message": message,
		},
	}
	bytes, _ := json.Marshal(payload)
	select {
	case client.Send <- bytes:
	default:
	}
}

func (h *Hub) subscribe(client *Client, eventName string) {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	subs := h.events[eventName]
	if subs == nil {
		subs = make(map[string]*Client)
		h.events[eventName] = subs
	}
	subs[client.ID] = client
	slog.Debug("client subscribed to event", "client_id", client.ID, "event", eventName)
}

func (h *Hub) unsubscribe(client *Client, eventName string) {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	subs := h.events[eventName]
	if subs != nil {
		delete(subs, client.ID)
		if len(subs) == 0 {
			delete(h.events, eventName)
		}
	}
	slog.Debug("client unsubscribed from event", "client_id", client.ID, "event", eventName)
}

func (h *Hub) handleTaskAccepted(client *Client, taskId string) {
	isDbTask := regexp.MustCompile(`^\d+$`).MatchString(taskId)
	if isDbTask {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), h.dbTimeout)
			defer cancel()
			_, err := h.dbPool.Exec(ctx,
				"UPDATE master.task_queue SET status = 'DISPATCHED', updated_at = NOW() WHERE id = $1",
				taskId,
			)
			if err != nil {
				slog.Error("failed to update task queue to DISPATCHED", "task_id", taskId, "err", err)
			}
		}()
	}

	h.BroadcastEvent("task:status:"+taskId, map[string]interface{}{
		"taskId": taskId,
		"status": "DISPATCHED",
	})
}

func (h *Hub) handleTaskRejected(client *Client, taskId string, message string) {
	h.mutex.Lock()
	client.Inflight--
	if client.Inflight < 0 {
		client.Inflight = 0
	}
	h.mutex.Unlock()

	isDbTask := regexp.MustCompile(`^\d+$`).MatchString(taskId)
	if isDbTask {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), h.dbTimeout)
			defer cancel()
			_, err := h.dbPool.Exec(ctx,
				"UPDATE master.task_queue SET status = 'FAILED', error_message = $1, updated_at = NOW() WHERE id = $2",
				message, taskId,
			)
			if err != nil {
				slog.Error("failed to update task queue to FAILED on reject", "task_id", taskId, "err", err)
			}
		}()
	}

	h.BroadcastEvent("task:status:"+taskId, map[string]interface{}{
		"taskId":        taskId,
		"status":        "FAILED",
		"error_message": message,
	})
}

func (h *Hub) handleTaskDone(client *Client, taskId string, status string, message string) {
	h.mutex.Lock()
	client.Inflight--
	if client.Inflight < 0 {
		client.Inflight = 0
	}
	h.mutex.Unlock()

	isDbTask := regexp.MustCompile(`^\d+$`).MatchString(taskId)
	if isDbTask {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), h.dbTimeout)
			defer cancel()
			_, err := h.dbPool.Exec(ctx,
				"UPDATE master.task_queue SET status = $1, error_message = $2, updated_at = NOW() WHERE id = $3",
				status, message, taskId,
			)
			if err != nil {
				slog.Error("failed to update task queue on done", "task_id", taskId, "err", err)
			}
		}()
	}

	h.BroadcastEvent("task:status:"+taskId, map[string]interface{}{
		"taskId":        taskId,
		"status":        status,
		"error_message": message,
	})
}
