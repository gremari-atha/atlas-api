package emailforward

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"atlas-api/internal/config"
	"atlas-api/internal/middleware"
	"atlas-api/internal/response"
	"atlas-api/internal/websocket"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Constants matching NestJS
const (
	OtpExtract        = "OTP_EXTRACT"
	NetflixUrlExtract = "NETFLIX_URL_EXTRACT"
)

// DTOs / Structs
type EmailDataPayload struct {
	From    string    `json:"from" validate:"required"`
	Subject string    `json:"subject" validate:"required"`
	Date    time.Time `json:"date" validate:"required"`
	Text    string    `json:"text" validate:"required"`
}

type RecieveEmailPayload struct {
	Tenant string             `json:"tenant" validate:"required"`
	Emails []EmailDataPayload `json:"emails" validate:"required,dive"`
}

type EmailForwardHandler struct {
	dbPool *pgxpool.Pool
}

func NewEmailForwardHandler(dbPool *pgxpool.Pool) *EmailForwardHandler {
	return &EmailForwardHandler{dbPool: dbPool}
}

// RegisterRoutes registers endpoints
func (h *EmailForwardHandler) RegisterRoutes(r chi.Router) {
	r.Route("/email-forward", func(r chi.Router) {
		r.With(middleware.ValidateBody[RecieveEmailPayload]()).Post("/", h.ReceiveEmail)
		r.Get("/subject", h.GetEmailSubject)
	})
}

// ReceiveEmail receives email forwarding webhook and puts it into master.email_forward_queue
func (h *EmailForwardHandler) ReceiveEmail(w http.ResponseWriter, r *http.Request) {
	payload := middleware.GetBody[RecieveEmailPayload](r)

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		slog.Error("failed to serialize email forward payload", "err", err)
		response.Error(w, http.StatusInternalServerError, "failed to serialize payload", err)
		return
	}

	_, err = h.dbPool.Exec(r.Context(), `
		INSERT INTO master.email_forward_queue (payload_json, status, available_at, created_at)
		VALUES ($1, 'PENDING', NOW(), NOW())
	`, string(payloadBytes))

	if err != nil {
		slog.Error("failed to enqueue email forward payload", "err", err)
		response.Error(w, http.StatusInternalServerError, "failed to queue email", err)
		return
	}

	slog.Info("Queued email-forward payload successfully", "tenant", payload.Tenant)
	w.WriteHeader(http.StatusNoContent)
}

// GetEmailSubject retrieves subjects filter list for the specified tenant
func (h *EmailForwardHandler) GetEmailSubject(w http.ResponseWriter, r *http.Request) {
	tenantID := r.URL.Query().Get("tenant")
	if tenantID == "" {
		response.JSON(w, http.StatusOK, map[string]interface{}{"subjects": []string{}})
		return
	}

	// Read email subjects from tenant schema
	rows, err := h.dbPool.Query(r.Context(), fmt.Sprintf(`
		SELECT subject FROM "%s".email_subject
	`, tenantID))
	if err != nil {
		slog.Warn("failed to fetch email subjects", "tenant", tenantID, "err", err)
		response.JSON(w, http.StatusOK, map[string]interface{}{"subjects": []string{}})
		return
	}
	defer rows.Close()

	subjects := []string{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err == nil {
			subjects = append(subjects, s)
		}
	}

	response.JSON(w, http.StatusOK, map[string]interface{}{"subjects": subjects})
}

// EmailForwardWorker processes queued emails
type EmailForwardWorker struct {
	dbPool       *pgxpool.Pool
	wsHub        *websocket.Hub
	pollInterval time.Duration
	staleTimeout time.Duration
	retryDelay   time.Duration
	dbTimeout    time.Duration
	running      bool
	workerCtx    context.Context
	workerCancel context.CancelFunc
	done         chan struct{}
}

func NewEmailForwardWorker(dbPool *pgxpool.Pool, wsHub *websocket.Hub) *EmailForwardWorker {
	dbTimeoutVal := config.GetEnvAsDurationMs("DB_TIMEOUT", 5000*time.Millisecond)

	return &EmailForwardWorker{
		dbPool:       dbPool,
		wsHub:        wsHub,
		pollInterval: 200 * time.Millisecond,
		staleTimeout: 5 * time.Minute,
		retryDelay:   5 * time.Second,
		dbTimeout:    dbTimeoutVal,
		done:         make(chan struct{}),
	}
}

func (w *EmailForwardWorker) Start(ctx context.Context) {
	w.workerCtx, w.workerCancel = context.WithCancel(ctx)
	w.running = true

	// Recover stale jobs on startup
	w.recoverStaleJobs()

	// Start loops
	go w.runLoop()
	go w.runRecoveryLoop()

	slog.Info("Email Forward Worker started successfully")
}

func (w *EmailForwardWorker) Stop() {
	if !w.running {
		return
	}
	w.running = false
	w.workerCancel()
	<-w.done
	slog.Info("Email Forward Worker stopped cleanly")
}

func (w *EmailForwardWorker) runLoop() {
	defer close(w.done)
	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if !w.running {
				return
			}
			processed, err := w.processNextJob()
			if err != nil {
				slog.Error("error processing email job", "err", err)
			}
			// Drain processing pipeline if there are active items
			for processed && w.running {
				processed, err = w.processNextJob()
				if err != nil {
					slog.Error("error processing email job", "err", err)
				}
			}
		case <-w.workerCtx.Done():
			return
		}
	}
}

func (w *EmailForwardWorker) runRecoveryLoop() {
	interval := w.staleTimeout / 2
	if interval > 60*time.Second {
		interval = 60 * time.Second
	}
	if interval < time.Second {
		interval = time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if !w.running {
				return
			}
			w.recoverStaleJobs()
		case <-w.workerCtx.Done():
			return
		}
	}
}

func (w *EmailForwardWorker) recoverStaleJobs() {
	cutoff := time.Now().Add(-w.staleTimeout)
	ctx, cancel := context.WithTimeout(w.workerCtx, w.dbTimeout)
	defer cancel()

	res, err := w.dbPool.Exec(ctx, `
		UPDATE master.email_forward_queue
		SET status = 'PENDING',
		    attempt = attempt + 1,
		    available_at = NOW(),
		    started_at = NULL
		WHERE status = 'PROCESSING'
		  AND started_at IS NOT NULL
		  AND started_at <= $1
	`, cutoff)

	if err != nil {
		slog.Error("failed to recover stale email jobs", "err", err)
		return
	}

	rowsAffected := res.RowsAffected()
	if rowsAffected > 0 {
		slog.Warn("recovered stale email-forward jobs", "count", rowsAffected)
	}
}

type JobRow struct {
	ID          int64
	PayloadJson string
	Attempt     int
}

func (w *EmailForwardWorker) processNextJob() (bool, error) {
	ctx, cancel := context.WithTimeout(w.workerCtx, w.dbTimeout)
	defer cancel()

	tx, err := w.dbPool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to begin claim transaction: %w", err)
	}
	defer tx.Rollback(context.Background())

	var job JobRow
	err = tx.QueryRow(ctx, `
		SELECT id, payload_json, attempt
		FROM master.email_forward_queue
		WHERE status = 'PENDING'
		  AND available_at <= NOW()
		ORDER BY available_at ASC, created_at ASC, id ASC
		LIMIT 1
		FOR UPDATE SKIP LOCKED
	`).Scan(&job.ID, &job.PayloadJson, &job.Attempt)

	if err != nil {
		if err == pgx.ErrNoRows {
			return false, nil
		}
		return false, fmt.Errorf("failed to select next pending job: %w", err)
	}

	_, err = tx.Exec(ctx, `
		UPDATE master.email_forward_queue
		SET status = 'PROCESSING',
		    started_at = NOW(),
		    last_error = NULL
		WHERE id = $1
	`, job.ID)
	if err != nil {
		return false, fmt.Errorf("failed to claim job: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("failed to commit claim transaction: %w", err)
	}

	err = w.processPayload(job.PayloadJson)
	if err != nil {
		availableAt := time.Now().Add(w.retryDelay)
		updateCtx, updateCancel := context.WithTimeout(w.workerCtx, w.dbTimeout)
		defer updateCancel()
		_, execErr := w.dbPool.Exec(updateCtx, `
			UPDATE master.email_forward_queue
			SET status = 'PENDING',
			    attempt = attempt + 1,
			    available_at = $1,
			    started_at = NULL,
			    last_error = $2
			WHERE id = $3
		`, availableAt, err.Error(), job.ID)
		if execErr != nil {
			slog.Error("failed to mark email-forward job as failed in db", "job_id", job.ID, "err", execErr)
		}
		return true, fmt.Errorf("job %d failed: %w", job.ID, err)
	}

	deleteCtx, deleteCancel := context.WithTimeout(w.workerCtx, w.dbTimeout)
	defer deleteCancel()
	_, err = w.dbPool.Exec(deleteCtx, `
		DELETE FROM master.email_forward_queue WHERE id = $1
	`, job.ID)
	if err != nil {
		slog.Error("failed to delete completed email-forward job", "job_id", job.ID, "err", err)
	}

	return true, nil
}

type EmailForwardSubjectInfo struct {
	Subject       string
	ExtractMethod string
}

func (w *EmailForwardWorker) processPayload(payloadJson string) error {
	var payload RecieveEmailPayload
	if err := json.Unmarshal([]byte(payloadJson), &payload); err != nil {
		return fmt.Errorf("failed to unmarshal job payload: %w", err)
	}

	if len(payload.Emails) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(w.workerCtx, w.dbTimeout)
	defer cancel()

	tx, err := w.dbPool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin process transaction: %w", err)
	}
	defer tx.Rollback(context.Background())

	subjList := make([]string, len(payload.Emails))
	for i, e := range payload.Emails {
		subjList[i] = e.Subject
	}

	rows, err := tx.Query(ctx, fmt.Sprintf(`
		SELECT subject, extract_method 
		FROM "%s".email_subject
		WHERE subject = ANY($1)
	`, payload.Tenant), subjList)
	if err != nil {
		return fmt.Errorf("failed to query tenant email subjects: %w", err)
	}
	defer rows.Close()

	var matchedSubjects []EmailForwardSubjectInfo
	for rows.Next() {
		var info EmailForwardSubjectInfo
		if err := rows.Scan(&info.Subject, &info.ExtractMethod); err == nil {
			matchedSubjects = append(matchedSubjects, info)
		}
	}

	if len(matchedSubjects) == 0 {
		return tx.Commit(ctx)
	}

	parser := NewEmailParser()

	for _, es := range matchedSubjects {
		for _, e := range payload.Emails {
			if e.Subject == es.Subject {
				var data *string

				if es.ExtractMethod == OtpExtract {
					data = parser.ExtractOtp(e.Text)
				} else if es.ExtractMethod == NetflixUrlExtract {
					data = parser.ExtractNetflixUrl(e.Text)
				}

				if data != nil {
					_, err = tx.Exec(ctx, fmt.Sprintf(`
						INSERT INTO "%s".email_message_ts (tenant_id, from_email, subject, email_date, parsed_data, created_at)
						VALUES ($1, $2, $3, $4, $5, NOW())
					`, payload.Tenant), payload.Tenant, e.From, e.Subject, e.Date, *data)
					if err != nil {
						return fmt.Errorf("failed to insert email message record: %w", err)
					}

					sanitizedEmail := parser.SanitizeEmail(e.From)
					eventName := sanitizedEmail

					go w.wsHub.BroadcastEvent(eventName, map[string]interface{}{
						"from":           e.From,
						"date":           e.Date,
						"subject":        e.Subject,
						"extract_method": es.ExtractMethod,
						"data":           *data,
					})
				}
			}
		}
	}

	return tx.Commit(ctx)
}

// EmailParser parses raw email texts
type EmailParser struct {
	netflixUrlRegex *regexp.Regexp
	otpRegex        *regexp.Regexp
}

func NewEmailParser() *EmailParser {
	return &EmailParser{
		netflixUrlRegex: regexp.MustCompile(`https://www\.netflix\.com/(?:password|account/update-primary-location|account/travel/verify|verifyemail|YourAccount)[^\s>\]]*`),
		otpRegex:        regexp.MustCompile(`(?m)^\s*(\d{4,6})\s*$`),
	}
}

func (p *EmailParser) SanitizeEmail(email string) string {
	replacer := strings.NewReplacer(".", "_", "@", "_")
	return replacer.Replace(strings.ToLower(email))
}

func (p *EmailParser) cleanText(emailText string) string {
	var sb strings.Builder
	for _, r := range emailText {
		if r >= 0x200C && r <= 0x200F {
			continue
		}
		sb.WriteRune(r)
	}
	return strings.TrimSpace(sb.String())
}

func (p *EmailParser) ExtractNetflixUrl(emailText string) *string {
	cleaned := p.cleanText(emailText)
	match := p.netflixUrlRegex.FindString(cleaned)
	if match == "" {
		return nil
	}
	return &match
}

func (p *EmailParser) ExtractOtp(emailText string) *string {
	cleaned := p.cleanText(emailText)
	matches := p.otpRegex.FindStringSubmatch(cleaned)
	if len(matches) < 2 {
		return nil
	}
	otpCode := matches[1]
	return &otpCode
}
