package transaction

import (
	"crypto/rand"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"atlas-api/internal/middleware"
	"atlas-api/internal/response"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DB models
type Transaction struct {
	ID         string            `json:"id"`
	Customer   string            `json:"customer"`
	Platform   string            `json:"platform"`
	TotalPrice int64             `json:"total_price"`
	CreatedAt  time.Time         `json:"created_at"`
	Items      []TransactionItem `json:"items,omitempty"`
}

type TransactionItem struct {
	ID               int64      `json:"id,string"`
	TransactionID    string     `json:"transaction_id"`
	Price            int64      `json:"price"`
	AccountID        *int64     `json:"account_id,string"`
	AccountUserID    *int64     `json:"account_user_id,string"`
	ProductID        *int64     `json:"product_id,string"`
	ProductVariantID *int64     `json:"product_variant_id,string"`
	CreatedAt        time.Time  `json:"created_at"`
	User             *UserInfo  `json:"user,omitempty"`
}

type UserInfo struct {
	ID               int64        `json:"id,string"`
	Name             string       `json:"name"`
	Status           string       `json:"status"`
	ExpiredAt        *time.Time   `json:"expired_at"`
	AccountProfileID int64        `json:"account_profile_id,string"`
	AccountID        int64        `json:"account_id,string"`
	CreatedAt        time.Time    `json:"created_at"`
	UpdatedAt        time.Time    `json:"updated_at"`
	Account          *AccountInfo `json:"account,omitempty"`
	Profile          *ProfileInfo `json:"profile,omitempty"`
}

type AccountInfo struct {
	ID                 int64        `json:"id,string"`
	AccountPassword    string       `json:"account_password"`
	SubscriptionExpiry time.Time    `json:"subscription_expiry"`
	Status             string       `json:"status"`
	Billing            string       `json:"billing"`
	EmailID            int64        `json:"email_id,string"`
	ProductVariantID   int64        `json:"product_variant_id,string"`
	Email              *EmailInfo   `json:"email,omitempty"`
	ProductVariant     *VariantInfo `json:"product_variant,omitempty"`
}

type EmailInfo struct {
	ID    int64  `json:"id,string"`
	Email string `json:"email"`
}

type VariantInfo struct {
	ID        int64        `json:"id,string"`
	Name      string       `json:"name"`
	BasePrice int64        `json:"base_price"`
	ProductID int64        `json:"product_id,string"`
	Product   *ProductInfo `json:"product,omitempty"`
}

type ProductInfo struct {
	ID   int64  `json:"id,string"`
	Name string `json:"name"`
}

type ProfileInfo struct {
	ID   int64  `json:"id,string"`
	Name string `json:"name"`
}

type Expense struct {
	ID        int64     `json:"id,string"`
	SubjectID *int64    `json:"subject_id,string"`
	Type      string    `json:"type"`
	Amount    int64     `json:"amount"`
	Note      *string   `json:"note"`
	CreatedAt time.Time `json:"created_at"`
}

type EmailSubject struct {
	ID            int64     `json:"id,string"`
	Context       string    `json:"context"`
	Subject       string    `json:"subject"`
	ExtractMethod string    `json:"extract_method"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// Payloads
type CreateTransactionPayload struct {
	ID       string                   `json:"id"` // optional client-generated snowflake ID
	Customer string                   `json:"customer" validate:"required"`
	Platform string                   `json:"platform" validate:"required"`
	Items    []CreateTransactionItem `json:"items" validate:"required,dive"`
}

type CreateTransactionItem struct {
	ProductVariantID int64  `json:"product_variant_id,string" validate:"required"`
	Price            *int64 `json:"price"`
	AccountProfileID *int64 `json:"account_profile_id,string"`
}

type UpdateTransactionPayload struct {
	Customer *string `json:"customer"`
	Platform *string `json:"platform"`
}

type CreateExpensePayload struct {
	SubjectID *int64  `json:"subject_id,string"`
	Type      string  `json:"type" validate:"required"`
	Amount    int64   `json:"amount" validate:"required,gt=0"`
	Note      *string `json:"note"`
}

type UpdateExpensePayload struct {
	SubjectID *int64  `json:"subject_id,string"`
	Type      *string `json:"type"`
	Amount    *int64  `json:"amount"`
	Note      *string `json:"note"`
}

type CreateEmailSubjectPayload struct {
	Context       string `json:"context" validate:"required"`
	Subject       string `json:"subject" validate:"required"`
	ExtractMethod string `json:"extract_method" validate:"required"`
}

type UpdateEmailSubjectPayload struct {
	Context       *string `json:"context"`
	Subject       *string `json:"subject"`
	ExtractMethod *string `json:"extract_method"`
}

type TransactionHandler struct {
	dbPool *pgxpool.Pool
}

func NewTransactionHandler(dbPool *pgxpool.Pool) *TransactionHandler {
	return &TransactionHandler{dbPool: dbPool}
}

func (h *TransactionHandler) RegisterRoutes(r chi.Router, auth *middleware.AuthMiddleware) {
	// Transaction
	r.Route("/transaction", func(r chi.Router) {
		r.Use(auth.TenantAuth)
		r.Get("/", h.FindAllTransactions)
		r.Get("/{id}", h.FindOneTransaction)
		r.With(middleware.ValidateBody[CreateTransactionPayload]()).Post("/", h.CreateTransaction)
		r.With(middleware.ValidateBody[UpdateTransactionPayload]()).Patch("/{id}", h.UpdateTransaction)
		r.Delete("/{id}", h.RemoveTransaction)
	})

	// Expense
	r.Route("/expense", func(r chi.Router) {
		r.Use(auth.TenantAuth)
		r.Get("/", h.FindAllExpenses)
		r.Get("/{id}", h.FindOneExpense)
		r.With(middleware.ValidateBody[CreateExpensePayload]()).Post("/", h.CreateExpense)
		r.With(middleware.ValidateBody[UpdateExpensePayload]()).Patch("/{id}", h.UpdateExpense)
		r.Delete("/{id}", h.RemoveExpense)
	})

	// Email Subject
	r.Route("/email-subject", func(r chi.Router) {
		r.Use(auth.TenantAuth)
		r.Get("/", h.FindAllEmailSubjects)
		r.Get("/{id}", h.FindOneEmailSubject)
		r.With(middleware.ValidateBody[CreateEmailSubjectPayload]()).Post("/", h.CreateEmailSubject)
		r.With(middleware.ValidateBody[UpdateEmailSubjectPayload]()).Patch("/{id}", h.UpdateEmailSubject)
		r.Delete("/{id}", h.RemoveEmailSubject)
	})
}

// generateRandomNumericID generates a random 16-digit numeric string for Snowflake fallback
func generateRandomNumericID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	// format as a large number
	var val uint64
	for i := 0; i < 8; i++ {
		val = (val << 8) | uint64(b[i])
	}
	return fmt.Sprintf("%016d", val%10000000000000000)
}

// ==========================================
// TRANSACTION REST
// ==========================================

func (h *TransactionHandler) FindAllTransactions(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	page, limit, offset := response.ParsePagination(r)

	q := r.URL.Query()
	custFilter := q.Get("customer")
	platFilter := q.Get("platform")
	fromDate := q.Get("from_date")
	toDate := q.Get("to_date")

	var conditions []string
	var args []interface{}
	argIdx := 1

	if custFilter != "" {
		conditions = append(conditions, fmt.Sprintf("customer ILIKE $%d", argIdx))
		args = append(args, "%"+custFilter+"%")
		argIdx++
	}
	if platFilter != "" {
		conditions = append(conditions, fmt.Sprintf("platform ILIKE $%d", argIdx))
		args = append(args, "%"+platFilter+"%")
		argIdx++
	}
	if fromDate != "" || toDate != "" {
		start := time.Time{}
		end := time.Now()
		if fromDate != "" {
			if t, err := time.Parse(time.RFC3339, fromDate); err == nil {
				start = t
			}
		}
		if toDate != "" {
			if t, err := time.Parse(time.RFC3339, toDate); err == nil {
				end = t
			}
		}
		// start of day for start, end of day for end
		start = time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, start.Location())
		end = time.Date(end.Year(), end.Month(), end.Day(), 23, 59, 59, 999999999, end.Location())

		conditions = append(conditions, fmt.Sprintf("created_at BETWEEN $%d AND $%d", argIdx, argIdx+1))
		args = append(args, start, end)
		argIdx += 2
	}

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = "WHERE " + strings.Join(conditions, " AND ")
	}

	countQuery := fmt.Sprintf(`SELECT COUNT(DISTINCT id) FROM "%s".transaction_ts %s`, tenantID, whereClause)
	var total int64
	err := h.dbPool.QueryRow(r.Context(), countQuery, args...).Scan(&total)
	if err != nil {
		slog.Error("failed to count transactions", "err", err)
		response.Error(w, http.StatusInternalServerError, "database count failed")
		return
	}

	selectQuery := fmt.Sprintf(`
		SELECT id, customer, platform, total_price, created_at
		FROM "%s".transaction_ts
		%s
		ORDER BY created_at DESC
		LIMIT $%d OFFSET $%d
	`, tenantID, whereClause, argIdx, argIdx+1)

	selectArgs := append(args, limit, offset)
	rows, err := h.dbPool.Query(r.Context(), selectQuery, selectArgs...)
	if err != nil {
		slog.Error("failed to query transactions", "err", err)
		response.Error(w, http.StatusInternalServerError, "database query failed")
		return
	}
	defer rows.Close()

	var transactions []Transaction
	for rows.Next() {
		var t Transaction
		if err := rows.Scan(&t.ID, &t.Customer, &t.Platform, &t.TotalPrice, &t.CreatedAt); err == nil {
			transactions = append(transactions, t)
		}
	}

	// Fetch items for all transactions in bulk (fixes N+1 query problem)
	if len(transactions) > 0 {
		txIDs := make([]string, len(transactions))
		for i, t := range transactions {
			txIDs[i] = t.ID
		}

		itemRows, err := h.dbPool.Query(r.Context(), fmt.Sprintf(`
			SELECT id, price, account_id, account_user_id, product_id, product_variant_id, created_at, transaction_id
			FROM "%s".transaction_item_ts
			WHERE transaction_id = ANY($1)
		`, tenantID), txIDs)
		if err == nil {
			defer itemRows.Close()
			itemMap := make(map[string][]TransactionItem)
			for itemRows.Next() {
				var it TransactionItem
				if err := itemRows.Scan(&it.ID, &it.Price, &it.AccountID, &it.AccountUserID, &it.ProductID, &it.ProductVariantID, &it.CreatedAt, &it.TransactionID); err == nil {
					itemMap[it.TransactionID] = append(itemMap[it.TransactionID], it)
				}
			}
			for i := range transactions {
				if items, found := itemMap[transactions[i].ID]; found {
					transactions[i].Items = items
				} else {
					transactions[i].Items = []TransactionItem{}
				}
			}
		}
	}

	response.JSON(w, http.StatusOK, response.NewPaginationResponse(transactions, total, page, limit))
}

func (h *TransactionHandler) FindOneTransaction(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	id := chi.URLParam(r, "id")

	var t Transaction
	err := h.dbPool.QueryRow(r.Context(), fmt.Sprintf(`
		SELECT id, customer, platform, total_price, created_at
		FROM "%s".transaction_ts WHERE id = $1
	`, tenantID), id).Scan(&t.ID, &t.Customer, &t.Platform, &t.TotalPrice, &t.CreatedAt)

	if err != nil {
		response.Error(w, http.StatusNotFound, fmt.Sprintf("transaction dengan id: %s tidak ditemukan", id))
		return
	}

	// Get items with nested details
	rows, err := h.dbPool.Query(r.Context(), fmt.Sprintf(`
		SELECT ti.id, ti.price, ti.account_id, ti.account_user_id, ti.product_id, ti.product_variant_id, ti.created_at,
		       au.name, au.status, au.expired_at, ap.name, a.account_password, a.subscription_expiry, a.status,
		       e.email, pv.name, p.name
		FROM "%s".transaction_item_ts ti
		LEFT JOIN "%s".account_user au ON ti.account_user_id = au.id
		LEFT JOIN "%s".account_profile ap ON au.account_profile_id = ap.id
		LEFT JOIN "%s".account a ON ti.account_id = a.id
		LEFT JOIN "%s".email e ON a.email_id = e.id
		LEFT JOIN "%s".product_variant pv ON ti.product_variant_id = pv.id
		LEFT JOIN "%s".product p ON ti.product_id = p.id
		WHERE ti.transaction_id = $1
	`, tenantID, tenantID, tenantID, tenantID, tenantID, tenantID, tenantID), id)
	if err == nil {
		defer rows.Close()
		items := []TransactionItem{}
		for rows.Next() {
			var it TransactionItem
			var u UserInfo
			var ap ProfileInfo
			var a AccountInfo
			var e EmailInfo
			var pv VariantInfo
			var p ProductInfo

			var uName, uStatus, apName, aPass, aStatus, eMail, pvName, pName sql.NullString
			var expTime, subExpiry sql.NullTime

			err = rows.Scan(
				&it.ID, &it.Price, &it.AccountID, &it.AccountUserID, &it.ProductID, &it.ProductVariantID, &it.CreatedAt,
				&uName, &uStatus, &expTime, &apName, &aPass, &subExpiry, &aStatus,
				&eMail, &pvName, &pName,
			)
			if err == nil {
				it.TransactionID = id
				if it.AccountUserID != nil && uName.Valid {
					u.ID = *it.AccountUserID
					u.Name = uName.String
					u.Status = uStatus.String
					if expTime.Valid {
						u.ExpiredAt = &expTime.Time
					}
					u.AccountProfileID = 0 // omitted
					if it.AccountID != nil {
						u.AccountID = *it.AccountID
					}

					if apName.Valid {
						ap.Name = apName.String
						u.Profile = &ap
					}

					if aPass.Valid {
						a.ID = u.AccountID
						a.AccountPassword = aPass.String
						if subExpiry.Valid {
							a.SubscriptionExpiry = subExpiry.Time
						}
						a.Status = aStatus.String

						if eMail.Valid {
							e.Email = eMail.String
							a.Email = &e
						}

						if pvName.Valid {
							pv.Name = pvName.String
							if pName.Valid {
								p.Name = pName.String
								pv.Product = &p
							}
							a.ProductVariant = &pv
						}
						u.Account = &a
					}
					it.User = &u
				}
				items = append(items, it)
			}
		}
		t.Items = items
	} else {
		t.Items = []TransactionItem{}
	}

	response.JSON(w, http.StatusOK, t)
}

func (h *TransactionHandler) CreateTransaction(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	payload := middleware.GetBody[CreateTransactionPayload](r)

	// Idempotency check if transaction ID is provided
	if payload.ID != "" {
		var t Transaction
		err := h.dbPool.QueryRow(r.Context(), fmt.Sprintf(`
			SELECT id, customer, platform, total_price, created_at 
			FROM "%s".transaction_ts WHERE id = $1
		`, tenantID), payload.ID).Scan(&t.ID, &t.Customer, &t.Platform, &t.TotalPrice, &t.CreatedAt)

		if err == nil {
			// Idempotent hit: return existing transaction details
			slog.Info("idempotency check hit, returning existing transaction", "transaction_id", payload.ID)
			h.FindOneTransaction(w, r)
			return
		}
	}

	tx, err := h.dbPool.Begin(r.Context())
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to start transaction")
		return
	}
	defer tx.Rollback(r.Context())

	transactionID := payload.ID
	if transactionID == "" {
		transactionID = generateRandomNumericID()
	}

	var transactionItems []TransactionItem
	var generatedUsers []UserInfo
	var failedItems []map[string]interface{}
	var totalPrice int64 = 0

	for _, item := range payload.Items {
		var finalProfileID int64
		var finalAccountID int64
		var finalProductID int64
		var finalPVBasePrice int64
		var finalProductName string
		var count int
		var pvID int64
		var productID int64

		if item.AccountProfileID != nil {
			// Check specific profile
			err = tx.QueryRow(r.Context(), fmt.Sprintf(`
				SELECT ap.account_id, ap.max_user, COUNT(au.id), a.product_variant_id, pv.product_id, pv.base_price, p.name
				FROM "%s".account_profile ap
				JOIN "%s".account a ON ap.account_id = a.id
				JOIN "%s".product_variant pv ON a.product_variant_id = pv.id
				JOIN "%s".product p ON pv.product_id = p.id
				LEFT JOIN "%s".account_user au ON ap.id = au.account_profile_id AND au.status = 'active'
				WHERE ap.id = $1
				GROUP BY ap.account_id, ap.max_user, a.product_variant_id, pv.product_id, pv.base_price, p.name
			`, tenantID, tenantID, tenantID, tenantID, tenantID), *item.AccountProfileID).Scan(&finalAccountID, &finalProfileID, &count, &pvID, &productID, &finalPVBasePrice, &finalProductName)

			if err != nil || count >= int(finalProfileID) {
				failedItems = append(failedItems, map[string]interface{}{
					"availability_status": "NOT_AVAILABLE",
					"product_variant_id":  strconv.FormatInt(item.ProductVariantID, 10),
					"product_name":        "-",
				})
				continue
			}
			finalProfileID = *item.AccountProfileID
			finalProductID = productID
		} else {
			// CTE lookup
			sqlQuery := fmt.Sprintf(`WITH now_ts AS (
					SELECT NOW() AS current_time
				),
				candidate_stats AS (
					SELECT
						ap.id AS profile_id,
						ap.account_id,
						pv.cooldown,
						ap.max_user,
						pv.product_id,
						pv.base_price,
						p.name AS product_name,
						COUNT(au.id) AS current_user_count,
						MAX(au.created_at) AS last_user_created_at,
						SUM(COUNT(au.id)) OVER(PARTITION BY ap.account_id) AS total_account_users
					FROM
						"%s".account_profile AS ap
						JOIN "%s".account AS a ON ap.account_id = a.id
						JOIN "%s".product_variant AS pv ON a.product_variant_id = pv.id
						JOIN "%s".product AS p ON pv.product_id = p.id
						LEFT JOIN "%s".account_user AS au
							ON ap.id = au.account_profile_id
							AND au.status = 'active'
					WHERE
						a.product_variant_id = $1
						AND ap.allow_generate = true
						AND a.status != 'disable'
						AND a.freeze_until IS NULL
					GROUP BY
						ap.id, pv.cooldown, ap.max_user, ap.account_id, pv.product_id, pv.base_price, p.name
				)
				SELECT
					cs.profile_id AS "candidateProfileId",
					cs.account_id AS "accountId",
					cs.product_id AS "productId",
					cs.base_price AS "basePrice",
					cs.product_name AS "productName",
					CASE
						WHEN (
							cs.last_user_created_at IS NULL OR
							n.current_time - cs.last_user_created_at > (cs.cooldown / 1000) * INTERVAL '1 second'
						) THEN 'available'
						ELSE 'cooldown'
					END AS status
				FROM
					candidate_stats AS cs
					CROSS JOIN now_ts n
				WHERE
					cs.current_user_count < cs.max_user
				ORDER BY
					CASE
						WHEN (
							cs.last_user_created_at IS NULL OR
							n.current_time - cs.last_user_created_at > (cs.cooldown / 1000) * INTERVAL '1 second'
						) THEN 1
						ELSE 2
					END ASC,
					CASE
						WHEN cs.current_user_count > 0 THEN 0
						ELSE 1
					END ASC,
					cs.total_account_users DESC,
					(cs.max_user - cs.current_user_count) ASC,
					cs.profile_id ASC
				LIMIT 1;`, tenantID, tenantID, tenantID, tenantID, tenantID)

			var candidateProfileId, accountId, productId, basePrice int64
			var status, productName string
			err = tx.QueryRow(r.Context(), sqlQuery, item.ProductVariantID).Scan(&candidateProfileId, &accountId, &productId, &basePrice, &productName, &status)
			if err != nil {
				failedItems = append(failedItems, map[string]interface{}{
					"availability_status": "NOT_AVAILABLE",
					"product_variant_id":  strconv.FormatInt(item.ProductVariantID, 10),
					"product_name":        "-",
				})
				continue
			}

			if status == "cooldown" {
				failedItems = append(failedItems, map[string]interface{}{
					"availability_status": "COOLDOWN",
					"product_variant_id":  strconv.FormatInt(item.ProductVariantID, 10),
					"product_name":        productName,
				})
				continue
			}

			finalProfileID = candidateProfileId
			finalAccountID = accountId
			finalProductID = productId
			finalPVBasePrice = basePrice
			finalProductName = productName
		}

		// Insert active account user
		var u UserInfo
		err = tx.QueryRow(r.Context(), fmt.Sprintf(`
			INSERT INTO "%s".account_user (name, status, expired_at, account_profile_id, account_id, created_at, updated_at)
			VALUES ($1, 'active', NULL, $2, $3, NOW(), NOW())
			RETURNING id, name, status, expired_at, account_profile_id, account_id, created_at, updated_at
		`, tenantID), payload.Customer, finalProfileID, finalAccountID).Scan(&u.ID, &u.Name, &u.Status, &u.ExpiredAt, &u.AccountProfileID, &u.AccountID, &u.CreatedAt, &u.UpdatedAt)
		if err != nil {
			response.Error(w, http.StatusInternalServerError, "failed to insert account user")
			return
		}

		itemPrice := finalPVBasePrice
		if item.Price != nil {
			itemPrice = *item.Price
		}
		totalPrice += itemPrice

		// Build transaction item list
		it := TransactionItem{
			TransactionID:    transactionID,
			Price:            itemPrice,
			AccountID:        &finalAccountID,
			AccountUserID:    &u.ID,
			ProductID:        &finalProductID,
			ProductVariantID: &item.ProductVariantID,
		}
		transactionItems = append(transactionItems, it)
		generatedUsers = append(generatedUsers, u)
	}

	if len(transactionItems) == 0 {
		// Rollback and return failed users lists
		tx.Rollback(r.Context())
		response.JSON(w, http.StatusOK, map[string]interface{}{
			"account_user": failedItems,
		})
		return
	}

	// Create transaction
	var t Transaction
	err = tx.QueryRow(r.Context(), fmt.Sprintf(`
		INSERT INTO "%s".transaction_ts (id, customer, platform, total_price, created_at)
		VALUES ($1, $2, $3, $4, NOW())
		RETURNING id, customer, platform, total_price, created_at
	`, tenantID), transactionID, payload.Customer, payload.Platform, totalPrice).Scan(&t.ID, &t.Customer, &t.Platform, &t.TotalPrice, &t.CreatedAt)
	if err != nil {
		slog.Error("failed to create transaction_ts record", "err", err)
		response.Error(w, http.StatusInternalServerError, "failed to insert transaction record")
		return
	}

	// Bulk insert items (since hypertable cannot bulk insert returning id in postgres easily, we do them in a loop)
	for i, item := range transactionItems {
		var id int64
		err = tx.QueryRow(r.Context(), fmt.Sprintf(`
			INSERT INTO "%s".transaction_item_ts (transaction_id, price, account_id, account_user_id, product_id, product_variant_id, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, NOW())
			RETURNING id
		`, tenantID), item.TransactionID, item.Price, item.AccountID, item.AccountUserID, item.ProductID, item.ProductVariantID).Scan(&id)
		if err != nil {
			slog.Error("failed to insert transaction_item_ts", "err", err)
			response.Error(w, http.StatusInternalServerError, "failed to insert transaction items")
			return
		}
		transactionItems[i].ID = id
	}

	if err := tx.Commit(r.Context()); err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to commit transaction")
		return
	}

	t.Items = transactionItems

	// Format response to combine users list
	var finalUsersResponse []interface{}
	for _, gu := range generatedUsers {
		finalUsersResponse = append(finalUsersResponse, gu)
	}
	for _, fi := range failedItems {
		finalUsersResponse = append(finalUsersResponse, fi)
	}

	response.JSON(w, http.StatusCreated, map[string]interface{}{
		"transaction":  t,
		"account_user": finalUsersResponse,
	})
}

func (h *TransactionHandler) UpdateTransaction(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	id := chi.URLParam(r, "id")
	payload := middleware.GetBody[UpdateTransactionPayload](r)

	if payload.Customer == nil && payload.Platform == nil {
		var updated Transaction
		err := h.dbPool.QueryRow(r.Context(), fmt.Sprintf(`
			SELECT id, customer, platform, total_price, created_at 
			FROM "%s".transaction_ts WHERE id = $1
		`, tenantID), id).Scan(&updated.ID, &updated.Customer, &updated.Platform, &updated.TotalPrice, &updated.CreatedAt)
		if err != nil {
			response.Error(w, http.StatusNotFound, fmt.Sprintf("transaction dengan id: %s tidak ditemukan", id))
			return
		}
		response.JSON(w, http.StatusOK, updated)
		return
	}

	tx, err := h.dbPool.Begin(r.Context())
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to start transaction")
		return
	}
	defer tx.Rollback(r.Context())

	// Build update query
	query := fmt.Sprintf(`UPDATE "%s".transaction_ts SET `, tenantID)
	var args []interface{}
	argIdx := 1

	if payload.Customer != nil {
		query += fmt.Sprintf("customer = $%d, ", argIdx)
		args = append(args, *payload.Customer)
		argIdx++
	}
	if payload.Platform != nil {
		query += fmt.Sprintf("platform = $%d, ", argIdx)
		args = append(args, *payload.Platform)
		argIdx++
	}

	query += "updated_at = NOW() WHERE id = " // wait, transaction_ts has no updated_at in schema, wait!
	// Let's check `000022_create_transaction_ts_hypertable.up.sql`:
	// It has: `id`, `customer`, `platform`, `total_price`, `created_at`. No `updated_at` column!
	// So we don't update updated_at!
	// Let's clean the query
	query = fmt.Sprintf(`UPDATE "%s".transaction_ts SET `, tenantID)
	args = nil
	argIdx = 1

	if payload.Customer != nil {
		query += fmt.Sprintf("customer = $%d, ", argIdx)
		args = append(args, *payload.Customer)
		argIdx++
	}
	if payload.Platform != nil {
		query += fmt.Sprintf("platform = $%d, ", argIdx)
		args = append(args, *payload.Platform)
		argIdx++
	}

	// strip trailing comma
	query = strings.TrimSuffix(query, ", ")
	query += fmt.Sprintf(" WHERE id = $%d", argIdx)
	args = append(args, id)

	res, err := tx.Exec(r.Context(), query, args...)
	if err != nil {
		slog.Error("failed to update transaction", "err", err)
		response.Error(w, http.StatusInternalServerError, "failed to update transaction")
		return
	}

	if res.RowsAffected() == 0 {
		response.Error(w, http.StatusNotFound, fmt.Sprintf("transaction dengan id: %s tidak ditemukan", id))
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to commit transaction")
		return
	}

	var updated Transaction
	_ = h.dbPool.QueryRow(r.Context(), fmt.Sprintf(`
		SELECT id, customer, platform, total_price, created_at 
		FROM "%s".transaction_ts WHERE id = $1
	`, tenantID), id).Scan(&updated.ID, &updated.Customer, &updated.Platform, &updated.TotalPrice, &updated.CreatedAt)

	response.JSON(w, http.StatusOK, updated)
}

func (h *TransactionHandler) RemoveTransaction(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	id := chi.URLParam(r, "id")

	tx, err := h.dbPool.Begin(r.Context())
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to start transaction")
		return
	}
	defer tx.Rollback(r.Context())

	// Delete from transaction_ts (cascade deletes items since we run it in transaction, wait! No foreign keys on hypertable usually, we delete manually)
	res, err := tx.Exec(r.Context(), fmt.Sprintf(`DELETE FROM "%s".transaction_ts WHERE id = $1`, tenantID), id)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to delete transaction")
		return
	}

	if res.RowsAffected() == 0 {
		response.Error(w, http.StatusNotFound, fmt.Sprintf("transaction dengan id: %s tidak ditemukan", id))
		return
	}

	_, err = tx.Exec(r.Context(), fmt.Sprintf(`DELETE FROM "%s".transaction_item_ts WHERE transaction_id = $1`, tenantID), id)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to delete transaction items")
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to commit transaction")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ==========================================
// EXPENSE REST
// ==========================================

func (h *TransactionHandler) FindAllExpenses(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	page, limit, offset := response.ParsePagination(r)

	subjectIDStr := r.URL.Query().Get("subject_id")
	expenseType := r.URL.Query().Get("type")

	// Validation checks
	if subjectIDStr != "" && expenseType == "" {
		response.Error(w, http.StatusBadRequest, "query param subject_id memerlukan konteks type")
		return
	}
	if expenseType == "global" && subjectIDStr != "" {
		response.Error(w, http.StatusBadRequest, "konteks global tidak boleh memiliki subject_id")
		return
	}

	var whereClauses []string
	var args []interface{}
	argIdx := 1

	if expenseType != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("type = $%d", argIdx))
		args = append(args, expenseType)
		argIdx++
	}

	if subjectIDStr != "" {
		subjectID, err := strconv.ParseInt(subjectIDStr, 10, 64)
		if err != nil {
			response.Error(w, http.StatusBadRequest, "invalid subject_id format")
			return
		}
		whereClauses = append(whereClauses, fmt.Sprintf("subject_id = $%d", argIdx))
		args = append(args, subjectID)
		argIdx++
	}

	whereSQL := ""
	if len(whereClauses) > 0 {
		whereSQL = "WHERE " + strings.Join(whereClauses, " AND ")
	}

	expenses := []Expense{}
	var total int64

	countQuery := fmt.Sprintf(`SELECT COUNT(*) FROM "%s".expense %s`, tenantID, whereSQL)
	err := h.dbPool.QueryRow(r.Context(), countQuery, args...).Scan(&total)
	if err != nil {
		slog.Error("failed to count expenses", "err", err)
		response.Error(w, http.StatusInternalServerError, "database count failed")
		return
	}

	selectQuery := fmt.Sprintf(`
		SELECT id, subject_id, type, amount, note, created_at
		FROM "%s".expense
		%s
		ORDER BY created_at DESC
		LIMIT $%d OFFSET $%d
	`, tenantID, whereSQL, argIdx, argIdx+1)

	selectArgs := append(args, limit, offset)
	rows, err := h.dbPool.Query(r.Context(), selectQuery, selectArgs...)
	if err != nil {
		slog.Error("failed to query expenses", "err", err)
		response.Error(w, http.StatusInternalServerError, "database query failed")
		return
	}
	defer rows.Close()

	for rows.Next() {
		var ex Expense
		err = rows.Scan(&ex.ID, &ex.SubjectID, &ex.Type, &ex.Amount, &ex.Note, &ex.CreatedAt)
		if err != nil {
			response.Error(w, http.StatusInternalServerError, "database scan failed")
			return
		}
		expenses = append(expenses, ex)
	}

	response.JSON(w, http.StatusOK, response.NewPaginationResponse(expenses, total, page, limit))
}

func (h *TransactionHandler) FindOneExpense(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)

	var ex Expense
	err := h.dbPool.QueryRow(r.Context(), fmt.Sprintf(`
		SELECT id, subject_id, type, amount, note, created_at
		FROM "%s".expense WHERE id = $1
	`, tenantID), id).Scan(&ex.ID, &ex.SubjectID, &ex.Type, &ex.Amount, &ex.Note, &ex.CreatedAt)

	if err != nil {
		response.Error(w, http.StatusNotFound, fmt.Sprintf("expense dengan id: %d tidak ditemukan", id))
		return
	}

	response.JSON(w, http.StatusOK, ex)
}

func (h *TransactionHandler) CreateExpense(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	payload := middleware.GetBody[CreateExpensePayload](r)

	var ex Expense
	err := h.dbPool.QueryRow(r.Context(), fmt.Sprintf(`
		INSERT INTO "%s".expense (subject_id, type, amount, note, created_at)
		VALUES ($1, $2, $3, $4, NOW())
		RETURNING id, subject_id, type, amount, note, created_at
	`, tenantID), payload.SubjectID, payload.Type, payload.Amount, payload.Note).Scan(&ex.ID, &ex.SubjectID, &ex.Type, &ex.Amount, &ex.Note, &ex.CreatedAt)

	if err != nil {
		slog.Error("failed to create expense", "err", err)
		response.Error(w, http.StatusInternalServerError, "failed to insert expense")
		return
	}

	response.JSON(w, http.StatusCreated, ex)
}

func (h *TransactionHandler) UpdateExpense(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	payload := middleware.GetBody[UpdateExpensePayload](r)

	if payload.Type == nil && payload.Amount == nil && payload.SubjectID == nil && payload.Note == nil {
		var ex Expense
		err := h.dbPool.QueryRow(r.Context(), fmt.Sprintf(`
			SELECT id, subject_id, type, amount, note, created_at
			FROM "%s".expense WHERE id = $1
		`, tenantID), id).Scan(&ex.ID, &ex.SubjectID, &ex.Type, &ex.Amount, &ex.Note, &ex.CreatedAt)
		if err != nil {
			response.Error(w, http.StatusNotFound, fmt.Sprintf("expense dengan id: %d tidak ditemukan", id))
			return
		}
		response.JSON(w, http.StatusOK, ex)
		return
	}

	// Fetch current details
	var currentType string
	var currentAmount int64
	var currentSubject *int64
	var currentNote *string
	err := h.dbPool.QueryRow(r.Context(), fmt.Sprintf(`
		SELECT type, amount, subject_id, note FROM "%s".expense WHERE id = $1
	`, tenantID), id).Scan(&currentType, &currentAmount, &currentSubject, &currentNote)
	if err != nil {
		response.Error(w, http.StatusNotFound, fmt.Sprintf("expense dengan id: %d tidak ditemukan", id))
		return
	}

	tx, err := h.dbPool.Begin(r.Context())
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to start transaction")
		return
	}
	defer tx.Rollback(r.Context())

	// Build update query
	query := fmt.Sprintf(`UPDATE "%s".expense SET `, tenantID)
	var args []interface{}
	argIdx := 1

	if payload.Type != nil {
		query += fmt.Sprintf("type = $%d, ", argIdx)
		args = append(args, *payload.Type)
		argIdx++
	}
	if payload.Amount != nil {
		query += fmt.Sprintf("amount = $%d, ", argIdx)
		args = append(args, *payload.Amount)
		argIdx++
	}
	if payload.SubjectID != nil {
		query += fmt.Sprintf("subject_id = $%d, ", argIdx)
		args = append(args, *payload.SubjectID)
		argIdx++
	}
	if payload.Note != nil {
		query += fmt.Sprintf("note = $%d, ", argIdx)
		args = append(args, *payload.Note)
		argIdx++
	}

	// strip trailing comma
	query = strings.TrimSuffix(query, ", ")
	query += fmt.Sprintf(" WHERE id = $%d", argIdx)
	args = append(args, id)

	_, err = tx.Exec(r.Context(), query, args...)
	if err != nil {
		slog.Error("failed to update expense", "err", err)
		response.Error(w, http.StatusInternalServerError, "failed to update expense")
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to commit transaction")
		return
	}

	var ex Expense
	_ = h.dbPool.QueryRow(r.Context(), fmt.Sprintf(`
		SELECT id, subject_id, type, amount, note, created_at
		FROM "%s".expense WHERE id = $1
	`, tenantID), id).Scan(&ex.ID, &ex.SubjectID, &ex.Type, &ex.Amount, &ex.Note, &ex.CreatedAt)

	response.JSON(w, http.StatusOK, ex)
}

func (h *TransactionHandler) RemoveExpense(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)

	res, err := h.dbPool.Exec(r.Context(), fmt.Sprintf(`DELETE FROM "%s".expense WHERE id = $1`, tenantID), id)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to delete expense")
		return
	}

	if res.RowsAffected() == 0 {
		response.Error(w, http.StatusNotFound, fmt.Sprintf("expense dengan id: %d tidak ditemukan", id))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ==========================================
// EMAIL SUBJECT REST
// ==========================================

func (h *TransactionHandler) FindAllEmailSubjects(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	page, limit, offset := response.ParsePagination(r)

	var subs []EmailSubject
	var total int64

	countQuery := fmt.Sprintf(`SELECT COUNT(*) FROM "%s".email_subject`, tenantID)
	err := h.dbPool.QueryRow(r.Context(), countQuery).Scan(&total)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "database count failed")
		return
	}

	selectQuery := fmt.Sprintf(`
		SELECT id, context, subject, extract_method, created_at, updated_at
		FROM "%s".email_subject
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2
	`, tenantID)

	rows, err := h.dbPool.Query(r.Context(), selectQuery, limit, offset)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "database query failed")
		return
	}
	defer rows.Close()

	for rows.Next() {
		var s EmailSubject
		err = rows.Scan(&s.ID, &s.Context, &s.Subject, &s.ExtractMethod, &s.CreatedAt, &s.UpdatedAt)
		if err != nil {
			response.Error(w, http.StatusInternalServerError, "database scan failed")
			return
		}
		subs = append(subs, s)
	}

	response.JSON(w, http.StatusOK, response.NewPaginationResponse(subs, total, page, limit))
}

func (h *TransactionHandler) FindOneEmailSubject(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)

	var s EmailSubject
	err := h.dbPool.QueryRow(r.Context(), fmt.Sprintf(`
		SELECT id, context, subject, extract_method, created_at, updated_at
		FROM "%s".email_subject WHERE id = $1
	`, tenantID), id).Scan(&s.ID, &s.Context, &s.Subject, &s.ExtractMethod, &s.CreatedAt, &s.UpdatedAt)

	if err != nil {
		response.Error(w, http.StatusNotFound, fmt.Sprintf("emailSubject dengan id: %d tidak ditemukan", id))
		return
	}

	response.JSON(w, http.StatusOK, s)
}

func (h *TransactionHandler) CreateEmailSubject(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	payload := middleware.GetBody[CreateEmailSubjectPayload](r)

	var s EmailSubject
	err := h.dbPool.QueryRow(r.Context(), fmt.Sprintf(`
		INSERT INTO "%s".email_subject (context, subject, extract_method, created_at, updated_at)
		VALUES ($1, $2, $3, NOW(), NOW())
		RETURNING id, context, subject, extract_method, created_at, updated_at
	`, tenantID), payload.Context, payload.Subject, payload.ExtractMethod).Scan(&s.ID, &s.Context, &s.Subject, &s.ExtractMethod, &s.CreatedAt, &s.UpdatedAt)

	if err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to insert email subject")
		return
	}

	response.JSON(w, http.StatusCreated, s)
}

func (h *TransactionHandler) UpdateEmailSubject(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	payload := middleware.GetBody[UpdateEmailSubjectPayload](r)

	tx, err := h.dbPool.Begin(r.Context())
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to start transaction")
		return
	}
	defer tx.Rollback(r.Context())

	// Build update query
	query := fmt.Sprintf(`UPDATE "%s".email_subject SET `, tenantID)
	var args []interface{}
	argIdx := 1

	if payload.Context != nil {
		query += fmt.Sprintf("context = $%d, ", argIdx)
		args = append(args, *payload.Context)
		argIdx++
	}
	if payload.Subject != nil {
		query += fmt.Sprintf("subject = $%d, ", argIdx)
		args = append(args, *payload.Subject)
		argIdx++
	}
	if payload.ExtractMethod != nil {
		query += fmt.Sprintf("extract_method = $%d, ", argIdx)
		args = append(args, *payload.ExtractMethod)
		argIdx++
	}

	query += "updated_at = NOW() "
	query += fmt.Sprintf("WHERE id = $%d", argIdx)
	args = append(args, id)

	res, err := tx.Exec(r.Context(), query, args...)
	if err != nil {
		slog.Error("failed to update email subject", "err", err)
		response.Error(w, http.StatusInternalServerError, "failed to update email subject")
		return
	}

	if res.RowsAffected() == 0 {
		response.Error(w, http.StatusNotFound, fmt.Sprintf("emailSubject dengan id: %d tidak ditemukan", id))
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to commit transaction")
		return
	}

	var s EmailSubject
	_ = h.dbPool.QueryRow(r.Context(), fmt.Sprintf(`
		SELECT id, context, subject, extract_method, created_at, updated_at
		FROM "%s".email_subject WHERE id = $1
	`, tenantID), id).Scan(&s.ID, &s.Context, &s.Subject, &s.ExtractMethod, &s.CreatedAt, &s.UpdatedAt)

	response.JSON(w, http.StatusOK, s)
}

func (h *TransactionHandler) RemoveEmailSubject(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)

	res, err := h.dbPool.Exec(r.Context(), fmt.Sprintf(`DELETE FROM "%s".email_subject WHERE id = $1`, tenantID), id)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to delete email subject")
		return
	}

	if res.RowsAffected() == 0 {
		response.Error(w, http.StatusNotFound, fmt.Sprintf("emailSubject dengan id: %d tidak ditemukan", id))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
