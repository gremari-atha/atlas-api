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
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5/pgxpool"

	"atlas-api/internal/config"
	"atlas-api/internal/middleware"
	"atlas-api/internal/response"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for simplicity
	},
}

type TaskCachedStatus struct {
	Status       string
	ErrorMessage string
	Timestamp    time.Time
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
	taskBots   map[string]string // taskId -> clientId
	taskCache  map[string]TaskCachedStatus
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
		taskBots:   make(map[string]string),
		taskCache:  make(map[string]TaskCachedStatus),
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

			if client.ConnectionType == "BOT" {
				h.BroadcastEvent("bot:list-update:"+client.TenantID, h.GetActiveBotsList(client.TenantID))
			}

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
				// Clean task mappings for this bot
				for tId, cId := range h.taskBots {
					if cId == client.ID {
						delete(h.taskBots, tId)
					}
				}
				h.mutex.Unlock()

				if client.ConnectionType == "BOT" {
					h.BroadcastEvent("bot:list-update:"+client.TenantID, h.GetActiveBotsList(client.TenantID))
				}
			} else {
				h.mutex.Unlock()
			}

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

	// 1. Authenticate connection based on type
	var tenantID string

	if connType == "BOT" {
		// API Key authentication for bots
		err := h.dbPool.QueryRow(r.Context(), "SELECT id FROM master.tenant WHERE api_key = $1", token).Scan(&tenantID)
		if err != nil {
			slog.Warn("bot ws handshake failed: invalid api key", "err", err)
			http.Error(w, "invalid API key", http.StatusUnauthorized)
			return
		}
	} else {
		// JWT token authentication for clients (dashboard users)
		claims := jwt.MapClaims{}
		parser := jwt.NewParser()
		_, _, err := parser.ParseUnverified(token, &claims)
		if err != nil {
			http.Error(w, "malformed token", http.StatusUnauthorized)
			return
		}

		tenantID, _ = claims["tenant_id"].(string)
		if tenantID == "" {
			http.Error(w, "invalid token payload: missing tenant_id", http.StatusUnauthorized)
			return
		}

		// Query tenant secret from master.tenant
		var secret string
		err = h.dbPool.QueryRow(r.Context(), "SELECT secret FROM master.tenant WHERE id = $1", tenantID).Scan(&secret)
		if err != nil {
			slog.Error("failed to retrieve tenant credentials on ws handshake", "tenant_id", tenantID, "err", err)
			http.Error(w, "unauthorized tenant", http.StatusUnauthorized)
			return
		}

		// Verify token signature with secret
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
		Status:         "ACTIVE",
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
	h.taskBots[taskId] = bot.ID
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
		if c.TenantID == tenantId && c.ConnectionType == "BOT" && c.Status == "ACTIVE" {
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
			slog.Error("failed to unmarshal task-accept data", "client_name", client.Name, "err", err)
			return
		}
		slog.Info("task-accept received", "client_name", client.Name, "taskId", data.TaskID)
		h.handleTaskAccepted(client, data.TaskID)

	case "task-reject":
		var data struct {
			TaskID  string `json:"taskId"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			slog.Error("failed to unmarshal task-reject data", "client_name", client.Name, "err", err)
			return
		}
		slog.Warn("task-reject received", "client_name", client.Name, "taskId", data.TaskID, "message", data.Message)
		h.handleTaskRejected(client, data.TaskID, data.Message)

	case "task-done":
		var data struct {
			TaskID  string `json:"taskId"`
			Status  string `json:"status"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			slog.Error("failed to unmarshal task-done data", "client_name", client.Name, "err", err)
			return
		}
		slog.Info("task-done received", "client_name", client.Name, "taskId", data.TaskID, "status", data.Status, "message", data.Message)
		h.handleTaskDone(client, data.TaskID, data.Status, data.Message)

	case "subscribe-event":
		var data struct {
			EventName string `json:"eventName"`
			RequestID string `json:"requestId"`
		}
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			return
		}
		if data.EventName == "" {
			ackPayload := map[string]interface{}{
				"event": "subscribe-event-ack",
				"data": map[string]interface{}{
					"requestId": data.RequestID,
					"status":    "error",
					"message":   "eventName in body required",
				},
			}
			bytes, _ := json.Marshal(ackPayload)
			select {
			case client.Send <- bytes:
			default:
			}
			return
		}
		h.subscribe(client, data.EventName)

		ackPayload := map[string]interface{}{
			"event": "subscribe-event-ack",
			"data": map[string]interface{}{
				"eventName": data.EventName,
				"requestId": data.RequestID,
				"status":    "success",
			},
		}
		bytes, _ := json.Marshal(ackPayload)
		select {
		case client.Send <- bytes:
		default:
		}

	case "unsubscribe-event":
		var data struct {
			EventName string `json:"eventName"`
			RequestID string `json:"requestId"`
		}
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			return
		}
		if data.EventName == "" {
			ackPayload := map[string]interface{}{
				"event": "unsubscribe-event-ack",
				"data": map[string]interface{}{
					"requestId": data.RequestID,
					"status":    "error",
					"message":   "eventName in body required",
				},
			}
			bytes, _ := json.Marshal(ackPayload)
			select {
			case client.Send <- bytes:
			default:
			}
			return
		}
		h.unsubscribe(client, data.EventName)

		ackPayload := map[string]interface{}{
			"event": "unsubscribe-event-ack",
			"data": map[string]interface{}{
				"eventName": data.EventName,
				"requestId": data.RequestID,
				"status":    "success",
			},
		}
		bytes, _ := json.Marshal(ackPayload)
		select {
		case client.Send <- bytes:
		default:
		}

	case "bot:status-change":
		var data struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			return
		}
		h.mutex.Lock()
		client.Status = data.Status
		tenantID := client.TenantID
		h.mutex.Unlock()

		h.BroadcastEvent("bot:list-update:"+tenantID, h.GetActiveBotsList(tenantID))

	case "bot:log":
		channel := fmt.Sprintf("bot:logs:%s:%s", client.TenantID, client.Name)
		h.BroadcastEvent(channel, msg.Data)

	case "bot:config-response", "bot:config-sync":
		channel := fmt.Sprintf("bot:config:%s:%s", client.TenantID, client.Name)
		h.BroadcastEvent(channel, msg.Data)

	case "command:log", "command:input-request":
		var data struct {
			CommandID string `json:"commandId"`
		}
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			return
		}
		h.BroadcastEvent("command:status:"+data.CommandID, msg.Data)

	case "command:input-response":
		var data struct {
			CommandID string `json:"commandId"`
		}
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			return
		}
		h.mutex.RLock()
		botID, exists := h.taskBots[data.CommandID]
		var bot *Client
		if exists {
			bot = h.clients[botID]
		}
		h.mutex.RUnlock()

		if bot != nil {
			rawMsg, err := json.Marshal(msg)
			if err == nil {
				select {
				case bot.Send <- rawMsg:
				default:
				}
			}
		}

	case "bot:config-request", "bot:config-update", "bot:standby", "bot:resume", "bot:restart":
		var data struct {
			BotName string `json:"botName"`
		}
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			slog.Error("failed to unmarshal bot control payload", "err", err, "raw", string(msg.Data))
			return
		}
		slog.Info("received bot control event", "event", msg.Event, "botName", data.BotName, "clientTenant", client.TenantID)

		h.mutex.RLock()
		var bot *Client
		var connectedBotNames []string
		for _, c := range h.clients {
			if c.ConnectionType == "BOT" {
				connectedBotNames = append(connectedBotNames, fmt.Sprintf("%s (tenant: %s)", c.Name, c.TenantID))
			}
			if c.TenantID == client.TenantID && c.ConnectionType == "BOT" && c.Name == data.BotName {
				bot = c
			}
		}
		h.mutex.RUnlock()

		if bot != nil {
			slog.Info("forwarding control event to bot", "event", msg.Event, "botName", data.BotName)
			rawMsg, err := json.Marshal(msg)
			if err == nil {
				select {
				case bot.Send <- rawMsg:
					slog.Info("sent control event bytes to bot", "event", msg.Event, "botName", data.BotName)
				default:
					slog.Error("bot send buffer full", "botName", data.BotName)
				}
			}
		} else {
			slog.Warn("bot not found for control event", "event", msg.Event, "botName", data.BotName, "availableBots", connectedBotNames)
		}
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
	slog.Info("client subscribed to event", "client_id", client.ID, "event", eventName)

	if strings.HasPrefix(eventName, "command:status:") {
		taskId := strings.TrimPrefix(eventName, "command:status:")
		if cached, exists := h.taskCache[taskId]; exists {
			slog.Info("found cached task status, replaying to client", "client_id", client.ID, "taskId", taskId, "status", cached.Status)
			payload := map[string]interface{}{
				"event":         "task-done",
				"taskId":        taskId,
				"status":        cached.Status,
				"error_message": cached.ErrorMessage,
			}
			msg := map[string]interface{}{
				"event": "event",
				"data": map[string]interface{}{
					"eventName": eventName,
					"payload":   payload,
				},
			}
			bytes, err := json.Marshal(msg)
			if err == nil {
				select {
				case client.Send <- bytes:
				default:
					slog.Warn("failed to send cached status to client, send buffer full", "client_id", client.ID)
				}
			}
		}
	}
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
	h.mutex.Lock()
	h.taskBots[taskId] = client.ID
	h.mutex.Unlock()

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
	delete(h.taskBots, taskId)
	h.taskCache[taskId] = TaskCachedStatus{
		Status:       "FAILED",
		ErrorMessage: message,
		Timestamp:    time.Now(),
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

	h.BroadcastEvent("command:status:"+taskId, map[string]interface{}{
		"event":         "task-done",
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
	delete(h.taskBots, taskId)
	h.taskCache[taskId] = TaskCachedStatus{
		Status:       status,
		ErrorMessage: message,
		Timestamp:    time.Now(),
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

	h.BroadcastEvent("command:status:"+taskId, map[string]interface{}{
		"event":         "task-done",
		"taskId":        taskId,
		"status":        status,
		"error_message": message,
	})
}

type BotInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	TenantID    string `json:"tenantId"`
	Status      string `json:"status"`
	ConnectedAt int64  `json:"connectedAt"`
	Inflight    int    `json:"inflightTasks"`
}

func (h *Hub) GetActiveBotsList(tenantID string) []BotInfo {
	h.mutex.RLock()
	defer h.mutex.RUnlock()

	var bots []BotInfo
	for _, c := range h.clients {
		if c.TenantID == tenantID && c.ConnectionType == "BOT" {
			status := c.Status
			if status == "" {
				status = "ACTIVE"
			}
			bots = append(bots, BotInfo{
				ID:          c.ID,
				Name:        c.Name,
				TenantID:    c.TenantID,
				Status:      status,
				ConnectedAt: c.ConnectedAt,
				Inflight:    c.Inflight,
			})
		}
	}
	return bots
}

func (h *Hub) ForwardToBot(tenantID string, botName string, message []byte) error {
	h.mutex.RLock()
	defer h.mutex.RUnlock()

	for _, c := range h.clients {
		if c.TenantID == tenantID && c.ConnectionType == "BOT" && c.Name == botName {
			select {
			case c.Send <- message:
				return nil
			default:
				return fmt.Errorf("bot connection send buffer full")
			}
		}
	}
	return fmt.Errorf("bot '%s' is not connected", botName)
}

func (h *Hub) DispatchTaskHandler(w http.ResponseWriter, r *http.Request) {
	uCtx, ok := middleware.GetUserContext(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req struct {
		TaskID string `json:"taskId"`
		Data   struct {
			Module     string `json:"module"`
			Type       string `json:"type"`
			ExecuteAt  string `json:"executeAt"`
			MaxRetries int    `json:"maxRetries"`
			Payload    string `json:"payload"`
		} `json:"data"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	if req.TaskID == "" || req.Data.Module == "" || req.Data.Type == "" {
		response.Error(w, http.StatusBadRequest, "taskId, data.module, and data.type are required")
		return
	}

	var parsedPayload interface{}
	if err := json.Unmarshal([]byte(req.Data.Payload), &parsedPayload); err != nil {
		// Fallback to raw string if it is not a valid JSON string
		parsedPayload = req.Data.Payload
	}

	botID, err := h.DispatchTask(
		r.Context(),
		req.TaskID,
		uCtx.TenantID,
		req.Data.Module,
		req.Data.Type,
		parsedPayload,
	)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, err.Error(), err)
		return
	}

	response.JSON(w, http.StatusOK, map[string]string{
		"taskId": req.TaskID,
		"botId":  botID,
	})
}
