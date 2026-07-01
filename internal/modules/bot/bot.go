package bot

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"atlas-api/internal/middleware"
	"atlas-api/internal/response"
	"atlas-api/internal/websocket"
)

type BotHandler struct {
	dbPool *pgxpool.Pool
	wsHub  *websocket.Hub
}

type BotCommandRequest struct {
	BotName string `json:"botName"`
}

type BotLogRequest struct {
	BotName   string    `json:"botName"`
	Level     string    `json:"level"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}

func NewBotHandler(dbPool *pgxpool.Pool, wsHub *websocket.Hub) *BotHandler {
	return &BotHandler{
		dbPool: dbPool,
		wsHub:  wsHub,
	}
}

func (h *BotHandler) RegisterRoutes(r chi.Router, auth *middleware.AuthMiddleware) {
	r.Route("/bot", func(r chi.Router) {
		r.Use(auth.TenantAuth)
		r.Get("/active", h.GetActive)
		r.Post("/standby", h.PostStandby)
		r.Post("/resume", h.PostResume)
		r.Post("/restart", h.PostRestart)
		r.Post("/log", h.PostLog)
		r.Get("/log", h.GetLog)
		r.Get("/api-key", h.GetAPIKey)
		r.Post("/api-key", h.GenerateAPIKey)
	})
}

func (h *BotHandler) GetActive(w http.ResponseWriter, r *http.Request) {
	uCtx, ok := middleware.GetUserContext(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	bots := h.wsHub.GetActiveBotsList(uCtx.TenantID)
	response.JSON(w, http.StatusOK, map[string]interface{}{
		"data": bots,
	})
}

func (h *BotHandler) PostStandby(w http.ResponseWriter, r *http.Request) {
	var req BotCommandRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if req.BotName == "" {
		response.Error(w, http.StatusBadRequest, "botName is required")
		return
	}

	uCtx, _ := middleware.GetUserContext(r.Context())
	msg := map[string]interface{}{
		"event": "bot:standby",
		"data": map[string]string{
			"botName": req.BotName,
		},
	}
	bytes, _ := json.Marshal(msg)

	if err := h.wsHub.ForwardToBot(uCtx.TenantID, req.BotName, bytes); err != nil {
		response.Error(w, http.StatusNotFound, err.Error(), err)
		return
	}

	response.JSON(w, http.StatusOK, map[string]string{"message": "standby command sent"})
}

func (h *BotHandler) PostResume(w http.ResponseWriter, r *http.Request) {
	var req BotCommandRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if req.BotName == "" {
		response.Error(w, http.StatusBadRequest, "botName is required")
		return
	}

	uCtx, _ := middleware.GetUserContext(r.Context())
	msg := map[string]interface{}{
		"event": "bot:resume",
		"data": map[string]string{
			"botName": req.BotName,
		},
	}
	bytes, _ := json.Marshal(msg)

	if err := h.wsHub.ForwardToBot(uCtx.TenantID, req.BotName, bytes); err != nil {
		response.Error(w, http.StatusNotFound, err.Error(), err)
		return
	}

	response.JSON(w, http.StatusOK, map[string]string{"message": "resume command sent"})
}

func (h *BotHandler) PostRestart(w http.ResponseWriter, r *http.Request) {
	var req BotCommandRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if req.BotName == "" {
		response.Error(w, http.StatusBadRequest, "botName is required")
		return
	}

	uCtx, _ := middleware.GetUserContext(r.Context())
	msg := map[string]interface{}{
		"event": "bot:restart",
		"data": map[string]string{
			"botName": req.BotName,
		},
	}
	bytes, _ := json.Marshal(msg)

	if err := h.wsHub.ForwardToBot(uCtx.TenantID, req.BotName, bytes); err != nil {
		response.Error(w, http.StatusNotFound, err.Error(), err)
		return
	}

	response.JSON(w, http.StatusOK, map[string]string{"message": "restart command sent"})
}

func (h *BotHandler) PostLog(w http.ResponseWriter, r *http.Request) {
	var req BotLogRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if req.BotName == "" || req.Level == "" || req.Message == "" {
		response.Error(w, http.StatusBadRequest, "botName, level, and message are required")
		return
	}

	uCtx, ok := middleware.GetUserContext(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	createdAt := req.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}

	// Async fire-and-forget db insertion to prevent blocking the bot
	go func() {
		_, err := h.dbPool.Exec(
			context.Background(),
			"INSERT INTO master.botlog_ts (bot_name, tenant_id, level, message, created_at) VALUES ($1, $2, $3, $4, $5)",
			req.BotName, uCtx.TenantID, req.Level, req.Message, createdAt,
		)
		if err != nil {
			slog.Error("failed to insert bot log to db", "bot", req.BotName, "tenant", uCtx.TenantID, "err", err)
		}
	}()

	response.JSON(w, http.StatusOK, map[string]string{"message": "log received"})
}

func (h *BotHandler) GetLog(w http.ResponseWriter, r *http.Request) {
	uCtx, ok := middleware.GetUserContext(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	botName := r.URL.Query().Get("botName")
	if botName == "" {
		response.Error(w, http.StatusBadRequest, "botName is required")
		return
	}

	pageStr := r.URL.Query().Get("page")
	limitStr := r.URL.Query().Get("limit")

	page := 1
	limit := 50

	if pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
			page = p
		}
	}
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
			if limit > 100 {
				limit = 100
			}
		}
	}

	offset := (page - 1) * limit

	rows, err := h.dbPool.Query(
		r.Context(),
		"SELECT id, bot_name, level, message, created_at FROM master.botlog_ts WHERE tenant_id = $1 AND bot_name = $2 ORDER BY created_at DESC LIMIT $3 OFFSET $4",
		uCtx.TenantID, botName, limit, offset,
	)
	if err != nil {
		slog.Error("failed to query bot logs from database", "bot", botName, "tenant", uCtx.TenantID, "err", err)
		response.Error(w, http.StatusInternalServerError, "failed to query logs", err)
		return
	}
	defer rows.Close()

	type LogRow struct {
		ID        int64     `json:"id"`
		BotName   string    `json:"botName"`
		Level     string    `json:"level"`
		Message   string    `json:"message"`
		CreatedAt time.Time `json:"createdAt"`
	}

	var logs []LogRow
	for rows.Next() {
		var l LogRow
		err := rows.Scan(&l.ID, &l.BotName, &l.Level, &l.Message, &l.CreatedAt)
		if err != nil {
			slog.Error("failed to scan bot log row", "err", err)
			response.Error(w, http.StatusInternalServerError, "failed to scan logs", err)
			return
		}
		logs = append(logs, l)
	}

	response.JSON(w, http.StatusOK, map[string]interface{}{
		"data": logs,
		"meta": map[string]int{
			"page":  page,
			"limit": limit,
		},
	})
}

func (h *BotHandler) GetAPIKey(w http.ResponseWriter, r *http.Request) {
	uCtx, ok := middleware.GetUserContext(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var apiKey *string
	err := h.dbPool.QueryRow(r.Context(), "SELECT api_key FROM master.tenant WHERE id = $1", uCtx.TenantID).Scan(&apiKey)
	if err != nil {
		slog.Error("failed to query api_key", "tenant_id", uCtx.TenantID, "err", err)
		response.Error(w, http.StatusInternalServerError, "failed to fetch API key", err)
		return
	}

	val := ""
	if apiKey != nil {
		val = *apiKey
	}

	response.JSON(w, http.StatusOK, map[string]string{
		"apiKey": val,
	})
}

func (h *BotHandler) GenerateAPIKey(w http.ResponseWriter, r *http.Request) {
	uCtx, ok := middleware.GetUserContext(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	// Generate 32-byte secure random string
	b := make([]byte, 32)
	_, err := rand.Read(b)
	if err != nil {
		slog.Error("failed to generate secure bytes", "err", err)
		response.Error(w, http.StatusInternalServerError, "failed to generate API key", err)
		return
	}
	newAPIKey := hex.EncodeToString(b)

	// Update tenant table with new api_key
	_, err = h.dbPool.Exec(r.Context(), "UPDATE master.tenant SET api_key = $1, updated_at = NOW() WHERE id = $2", newAPIKey, uCtx.TenantID)
	if err != nil {
		slog.Error("failed to update api_key in tenant", "tenant_id", uCtx.TenantID, "err", err)
		response.Error(w, http.StatusInternalServerError, "failed to save API key", err)
		return
	}

	response.JSON(w, http.StatusOK, map[string]string{
		"apiKey": newAPIKey,
	})
}
