package account

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"atlas-api/internal/middleware"
	"atlas-api/internal/response"
	"atlas-api/internal/scheduler"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DB models
type Account struct {
	ID                 int64           `json:"id,string"`
	AccountPassword    string          `json:"account_password"`
	SubscriptionExpiry time.Time       `json:"subscription_expiry"`
	Status             string          `json:"status"`
	Billing            string          `json:"billing"`
	BatchStartDate     *time.Time      `json:"batch_start_date"`
	BatchEndDate       *time.Time      `json:"batch_end_date"`
	EmailID            int64           `json:"email_id,string"`
	ProductVariantID   int64           `json:"product_variant_id,string"`
	FreezeUntil        *time.Time      `json:"freeze_until"`
	Pinned             bool            `json:"pinned"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
	Email              *EmailInfo      `json:"email,omitempty"`
	ProductVariant     *VariantInfo    `json:"product_variant,omitempty"`
	Profile            []ProfileInfo   `json:"profile,omitempty"`
	Modifier           []ModifierInfo  `json:"modifier,omitempty"`
	ProfileCount       int             `json:"profile_count,omitempty"`
	MaxUser            int             `json:"max_user,omitempty"`
	UserCount          int             `json:"user_count,omitempty"`
}

type EmailInfo struct {
	ID    int64  `json:"id,string"`
	Email string `json:"email"`
}

type VariantInfo struct {
	ID        int64        `json:"id,string"`
	Name      string       `json:"name"`
	ProductID int64        `json:"product_id,string"`
	Product   *ProductInfo `json:"product,omitempty"`
}

type ProductInfo struct {
	ID   int64  `json:"id,string"`
	Name string `json:"name"`
}

type ProfileInfo struct {
	ID             int64      `json:"id,string"`
	Name           string     `json:"name"`
	MaxUser        int        `json:"max_user"`
	AllowGenerate  bool       `json:"allow_generate"`
	AccountID      int64      `json:"account_id,string"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	User           []UserInfo `json:"user,omitempty"`
}

type UserInfo struct {
	ID               int64      `json:"id,string"`
	Name             string     `json:"name"`
	Status           string     `json:"status"`
	ExpiredAt        *time.Time `json:"expired_at"`
	AccountProfileID int64      `json:"account_profile_id,string"`
	AccountID        int64      `json:"account_id,string"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type ModifierInfo struct {
	ID         int64     `json:"id,string"`
	ModifierID string    `json:"modifier_id"`
	Metadata   string    `json:"metadata"`
	Enabled    bool      `json:"enabled"`
	AccountID  int64     `json:"account_id,string"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Payloads
type CreateAccountPayload struct {
	AccountPassword    string                `json:"account_password" validate:"required"`
	SubscriptionExpiry string                `json:"subscription_expiry" validate:"required"`
	Status             string                `json:"status" validate:"required"`
	Billing            string                `json:"billing" validate:"required"`
	BatchStartDate     *string               `json:"batch_start_date"`
	BatchEndDate       *string               `json:"batch_end_date"`
	EmailID            int64                 `json:"email_id,string" validate:"required"`
	ProductVariantID   int64                 `json:"product_variant_id,string" validate:"required"`
	Pinned             bool                  `json:"pinned"`
	Profile            []CreateProfileNested `json:"profile"`
	Modifier           []CreateModNested     `json:"modifier"`
}

type CreateProfileNested struct {
	Name          string `json:"name" validate:"required"`
	MaxUser       int    `json:"max_user" validate:"required"`
	AllowGenerate bool   `json:"allow_generate"`
}

type CreateModNested struct {
	ModifierID string `json:"modifier_id" validate:"required"`
	Metadata   string `json:"metadata"`
}

type UpdateAccountPayload struct {
	AccountPassword    *string `json:"account_password"`
	SubscriptionExpiry *string `json:"subscription_expiry"`
	Status             *string `json:"status"`
	Billing            *string `json:"billing"`
	BatchStartDate     *string `json:"batch_start_date"`
	BatchEndDate       *string `json:"batch_end_date"`
	EmailID            *int64  `json:"email_id,string"`
	ProductVariantID   *int64  `json:"product_variant_id,string"`
	Pinned             *bool   `json:"pinned"`
}

type ModifierAction struct {
	ModifierID string  `json:"modifier_id" validate:"required"`
	Metadata   *string `json:"metadata"`
	Action     string  `json:"action" validate:"required,oneof=ADD UPDATE REMOVE"`
}

type UpdateModifierPayload struct {
	Modifier []ModifierAction `json:"modifier" validate:"required,dive"`
}

type FreezePayload struct {
	Duration int64 `json:"duration" validate:"required,gt=0"` // duration in milliseconds
}

type AccountHandler struct {
	dbPool      *pgxpool.Pool
	asynqClient *scheduler.Client
}

func NewAccountHandler(dbPool *pgxpool.Pool, asynqClient *scheduler.Client) *AccountHandler {
	return &AccountHandler{dbPool: dbPool, asynqClient: asynqClient}
}

func (h *AccountHandler) RegisterRoutes(r chi.Router, auth *middleware.AuthMiddleware) {
	r.Route("/account", func(r chi.Router) {
		r.Use(auth.TenantAuth)
		r.Get("/", h.FindAll)
		r.Get("/count", h.CountStatusAccount)
		r.Get("/{id}", h.FindOne)
		r.With(middleware.ValidateBody[CreateAccountPayload]()).Post("/", h.Create)
		r.With(middleware.ValidateBody[UpdateAccountPayload]()).Patch("/{id}", h.Update)
		r.With(middleware.ValidateBody[UpdateModifierPayload]()).Patch("/{id}/modifier", h.UpdateModifier)
		r.With(middleware.ValidateBody[FreezePayload]()).Patch("/{id}/freeze", h.Freeze)
		r.Patch("/{id}/unfreeze", h.Unfreeze)
		r.Delete("/{id}", h.Remove)
	})

	r.Route("/account-profile", func(r chi.Router) {
		r.Use(auth.TenantAuth)
		r.Get("/", h.FindAllProfiles)
		r.Get("/{id}", h.FindOneProfile)
		r.With(middleware.ValidateBody[CreateProfilePayload]()).Post("/", h.CreateProfile)
		r.With(middleware.ValidateBody[UpdateProfilePayload]()).Patch("/{id}", h.UpdateProfile)
		r.Delete("/{id}", h.RemoveProfile)
	})

	r.Route("/account-user", func(r chi.Router) {
		r.Use(auth.TenantAuth)
		r.Get("/", h.FindAllUsers)
		r.Get("/{id}", h.FindOneUser)
		r.With(middleware.ValidateBody[CreateUserPayload]()).Post("/", h.CreateUser)
		r.With(middleware.ValidateBody[UpdateUserPayload]()).Patch("/{id}", h.UpdateUser)
		r.Delete("/{id}", h.RemoveUser)
	})
}

// FindAll accounts
func (h *AccountHandler) FindAll(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	page, limit, offset := response.ParsePagination(r)

	q := r.URL.Query()
	isLite := q.Get("lite") == "true"
	emailFilter := q.Get("email")
	statusFilter := q.Get("status")
	variantFilter := q.Get("product_variant_id")
	productFilter := q.Get("product_id")

	// Dynamic SQL construction
	var queryConditions []string
	var args []interface{}
	argIdx := 1

	if variantFilter != "" {
		vID, _ := strconv.ParseInt(variantFilter, 10, 64)
		queryConditions = append(queryConditions, fmt.Sprintf("a.product_variant_id = $%d", argIdx))
		args = append(args, vID)
		argIdx++
	}

	if statusFilter != "" {
		if statusFilter == "freeze" {
			queryConditions = append(queryConditions, "a.freeze_until IS NOT NULL")
		} else {
			queryConditions = append(queryConditions, fmt.Sprintf("a.status = $%d", argIdx))
			args = append(args, statusFilter)
			argIdx++
		}
	}

	if emailFilter != "" {
		queryConditions = append(queryConditions, fmt.Sprintf("e.email ILIKE $%d", argIdx))
		args = append(args, "%"+emailFilter+"%")
		argIdx++
	}

	if productFilter != "" {
		pID, _ := strconv.ParseInt(productFilter, 10, 64)
		queryConditions = append(queryConditions, fmt.Sprintf("pv.product_id = $%d", argIdx))
		args = append(args, pID)
		argIdx++
	}

	whereClause := ""
	if len(queryConditions) > 0 {
		whereClause = "WHERE " + strings.Join(queryConditions, " AND ")
	}

	// Count total
	countQuery := fmt.Sprintf(`
		SELECT COUNT(DISTINCT a.id) 
		FROM "%s".account a
		JOIN "%s".email e ON a.email_id = e.id
		JOIN "%s".product_variant pv ON a.product_variant_id = pv.id
		%s
	`, tenantID, tenantID, tenantID, whereClause)

	var total int64
	err := h.dbPool.QueryRow(r.Context(), countQuery, args...).Scan(&total)
	if err != nil {
		slog.Error("failed to count accounts", "err", err)
		response.Error(w, http.StatusInternalServerError, "database count failed")
		return
	}

	// Fetch accounts
	selectQuery := fmt.Sprintf(`
		SELECT a.id, a.account_password, a.subscription_expiry, a.status, a.billing, 
		       a.batch_start_date, a.batch_end_date, a.email_id, a.product_variant_id, 
		       a.freeze_until, a.pinned, a.created_at, a.updated_at,
		       e.email, pv.name, pv.product_id, p.name
		FROM "%s".account a
		JOIN "%s".email e ON a.email_id = e.id
		JOIN "%s".product_variant pv ON a.product_variant_id = pv.id
		JOIN "%s".product p ON pv.product_id = p.id
		%s
		ORDER BY a.pinned DESC, a.updated_at DESC
		LIMIT $%d OFFSET $%d
	`, tenantID, tenantID, tenantID, tenantID, whereClause, argIdx, argIdx+1)

	selectArgs := append(args, limit, offset)
	rows, err := h.dbPool.Query(r.Context(), selectQuery, selectArgs...)
	if err != nil {
		slog.Error("failed to query accounts", "err", err)
		response.Error(w, http.StatusInternalServerError, "database query failed")
		return
	}
	defer rows.Close()

	var accounts []Account
	for rows.Next() {
		var a Account
		var e EmailInfo
		var v VariantInfo
		var p ProductInfo
		err = rows.Scan(
			&a.ID, &a.AccountPassword, &a.SubscriptionExpiry, &a.Status, &a.Billing,
			&a.BatchStartDate, &a.BatchEndDate, &a.EmailID, &a.ProductVariantID,
			&a.FreezeUntil, &a.Pinned, &a.CreatedAt, &a.UpdatedAt,
			&e.Email, &v.Name, &v.ProductID, &p.Name,
		)
		if err != nil {
			slog.Error("failed to scan account", "err", err)
			response.Error(w, http.StatusInternalServerError, "database scan failed")
			return
		}
		e.ID = a.EmailID
		v.ID = a.ProductVariantID
		p.ID = v.ProductID
		v.Product = &p
		a.Email = &e
		a.ProductVariant = &v
		accounts = append(accounts, a)
	}

	if !isLite && len(accounts) > 0 {
		// Bulk fetch profiles, users, and modifiers to avoid N+1 query problem
		accountIDs := make([]int64, len(accounts))
		for i, a := range accounts {
			accountIDs[i] = a.ID
		}

		// 1. Fetch profiles in bulk
		pRows, err := h.dbPool.Query(r.Context(), fmt.Sprintf(`
			SELECT id, name, max_user, allow_generate, account_id, created_at, updated_at
			FROM "%s".account_profile
			WHERE account_id = ANY($1)
			ORDER BY name ASC
		`, tenantID), accountIDs)
		if err == nil {
			defer pRows.Close()
			var profiles []ProfileInfo
			var profileIDs []int64
			for pRows.Next() {
				var pr ProfileInfo
				if err := pRows.Scan(&pr.ID, &pr.Name, &pr.MaxUser, &pr.AllowGenerate, &pr.AccountID, &pr.CreatedAt, &pr.UpdatedAt); err == nil {
					profiles = append(profiles, pr)
					profileIDs = append(profileIDs, pr.ID)
				}
			}

			// 2. Fetch users in bulk
			userMap := make(map[int64][]UserInfo)
			if len(profileIDs) > 0 {
				uRows, err := h.dbPool.Query(r.Context(), fmt.Sprintf(`
					SELECT id, name, status, expired_at, account_profile_id, account_id, created_at, updated_at
					FROM "%s".account_user
					WHERE account_profile_id = ANY($1) AND status = 'active'
				`, tenantID), profileIDs)
				if err == nil {
					defer uRows.Close()
					for uRows.Next() {
						var u UserInfo
						if err := uRows.Scan(&u.ID, &u.Name, &u.Status, &u.ExpiredAt, &u.AccountProfileID, &u.AccountID, &u.CreatedAt, &u.UpdatedAt); err == nil {
							userMap[u.AccountProfileID] = append(userMap[u.AccountProfileID], u)
						}
					}
				}
			}

			// Assemble users into profiles
			profileMap := make(map[int64][]ProfileInfo)
			for _, pr := range profiles {
				if users, found := userMap[pr.ID]; found {
					pr.User = users
				} else {
					pr.User = []UserInfo{}
				}
				profileMap[pr.AccountID] = append(profileMap[pr.AccountID], pr)
			}

			// 3. Fetch modifiers in bulk
			modifierMap := make(map[int64][]ModifierInfo)
			mRows, err := h.dbPool.Query(r.Context(), fmt.Sprintf(`
				SELECT id, modifier_id, metadata, enabled, account_id, created_at, updated_at
				FROM "%s".account_modifier
				WHERE account_id = ANY($1) AND enabled = true
			`, tenantID), accountIDs)
			if err == nil {
				defer mRows.Close()
				for mRows.Next() {
					var m ModifierInfo
					if err := mRows.Scan(&m.ID, &m.ModifierID, &m.Metadata, &m.Enabled, &m.AccountID, &m.CreatedAt, &m.UpdatedAt); err == nil {
						modifierMap[m.AccountID] = append(modifierMap[m.AccountID], m)
					}
				}
			}

			// Assemble profiles and modifiers into accounts
			for i := range accounts {
				if profs, found := profileMap[accounts[i].ID]; found {
					accounts[i].Profile = profs
				} else {
					accounts[i].Profile = []ProfileInfo{}
				}
				if mods, found := modifierMap[accounts[i].ID]; found {
					accounts[i].Modifier = mods
				} else {
					accounts[i].Modifier = []ModifierInfo{}
				}
			}
		}
	} else if isLite && len(accounts) > 0 {
		// Bulk fetch counts for lite queries to avoid N+1 query problem
		accountIDs := make([]int64, len(accounts))
		for i, a := range accounts {
			accountIDs[i] = a.ID
		}

		type ProfileStat struct {
			ProfileCount int
			MaxUser      int
		}
		profileStatMap := make(map[int64]ProfileStat)
		pRows, err := h.dbPool.Query(r.Context(), fmt.Sprintf(`
			SELECT 
				account_id,
				COUNT(id) AS profile_count,
				COALESCE(SUM(max_user), 0) AS max_user
			FROM "%s".account_profile
			WHERE account_id = ANY($1)
			GROUP BY account_id
		`, tenantID), accountIDs)
		if err == nil {
			defer pRows.Close()
			for pRows.Next() {
				var accID int64
				var ps ProfileStat
				if err := pRows.Scan(&accID, &ps.ProfileCount, &ps.MaxUser); err == nil {
					profileStatMap[accID] = ps
				}
			}
		}

		userCountMap := make(map[int64]int)
		uRows, err := h.dbPool.Query(r.Context(), fmt.Sprintf(`
			SELECT 
				ap.account_id,
				COUNT(au.id) AS user_count
			FROM "%s".account_user au
			JOIN "%s".account_profile ap ON au.account_profile_id = ap.id
			WHERE ap.account_id = ANY($1) AND au.status = 'active'
			GROUP BY ap.account_id
		`, tenantID, tenantID), accountIDs)
		if err == nil {
			defer uRows.Close()
			for uRows.Next() {
				var accID int64
				var count int
				if err := uRows.Scan(&accID, &count); err == nil {
					userCountMap[accID] = count
				}
			}
		}

		for i := range accounts {
			if stat, found := profileStatMap[accounts[i].ID]; found {
				accounts[i].ProfileCount = stat.ProfileCount
				accounts[i].MaxUser = stat.MaxUser
			} else {
				accounts[i].ProfileCount = 0
				accounts[i].MaxUser = 0
			}
			if count, found := userCountMap[accounts[i].ID]; found {
				accounts[i].UserCount = count
			} else {
				accounts[i].UserCount = 0
			}
		}
	}

	response.JSON(w, http.StatusOK, response.NewPaginationResponse(accounts, total, page, limit))
}

func (h *AccountHandler) FindOne(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)

	var a Account
	var e EmailInfo
	var v VariantInfo
	var p ProductInfo

	err := h.dbPool.QueryRow(r.Context(), fmt.Sprintf(`
		SELECT a.id, a.account_password, a.subscription_expiry, a.status, a.billing, 
		       a.batch_start_date, a.batch_end_date, a.email_id, a.product_variant_id, 
		       a.freeze_until, a.pinned, a.created_at, a.updated_at,
		       e.email, pv.name, pv.product_id, p.name
		FROM "%s".account a
		JOIN "%s".email e ON a.email_id = e.id
		JOIN "%s".product_variant pv ON a.product_variant_id = pv.id
		JOIN "%s".product p ON pv.product_id = p.id
		WHERE a.id = $1
	`, tenantID, tenantID, tenantID, tenantID), id).Scan(
		&a.ID, &a.AccountPassword, &a.SubscriptionExpiry, &a.Status, &a.Billing,
		&a.BatchStartDate, &a.BatchEndDate, &a.EmailID, &a.ProductVariantID,
		&a.FreezeUntil, &a.Pinned, &a.CreatedAt, &a.UpdatedAt,
		&e.Email, &v.Name, &v.ProductID, &p.Name,
	)

	if err != nil {
		response.Error(w, http.StatusNotFound, fmt.Sprintf("account dengan id: %d tidak ditemukan", id))
		return
	}
	e.ID = a.EmailID
	v.ID = a.ProductVariantID
	p.ID = v.ProductID
	v.Product = &p
	a.Email = &e
	a.ProductVariant = &v

	// Get profiles and users
	pRows, err := h.dbPool.Query(r.Context(), fmt.Sprintf(`
		SELECT id, name, max_user, allow_generate, created_at, updated_at
		FROM "%s".account_profile
		WHERE account_id = $1
		ORDER BY name ASC
	`, tenantID), id)
	if err == nil {
		defer pRows.Close()
		profiles := []ProfileInfo{}
		for pRows.Next() {
			var pr ProfileInfo
			if err := pRows.Scan(&pr.ID, &pr.Name, &pr.MaxUser, &pr.AllowGenerate, &pr.CreatedAt, &pr.UpdatedAt); err == nil {
				pr.AccountID = id

				uRows, err := h.dbPool.Query(r.Context(), fmt.Sprintf(`
					SELECT id, name, status, expired_at, account_id, created_at, updated_at
					FROM "%s".account_user
					WHERE account_profile_id = $1 AND status = 'active'
				`, tenantID), pr.ID)
				if err == nil {
					users := []UserInfo{}
					for uRows.Next() {
						var u UserInfo
						if err := uRows.Scan(&u.ID, &u.Name, &u.Status, &u.ExpiredAt, &u.AccountID, &u.CreatedAt, &u.UpdatedAt); err == nil {
							u.AccountProfileID = pr.ID
							users = append(users, u)
						}
					}
					uRows.Close()
					pr.User = users
				} else {
					pr.User = []UserInfo{}
				}
				profiles = append(profiles, pr)
			}
		}
		a.Profile = profiles
	} else {
		a.Profile = []ProfileInfo{}
	}

	// Get modifiers
	mRows, err := h.dbPool.Query(r.Context(), fmt.Sprintf(`
		SELECT id, modifier_id, metadata, enabled, created_at, updated_at
		FROM "%s".account_modifier
		WHERE account_id = $1 AND enabled = true
	`, tenantID), id)
	if err == nil {
		defer mRows.Close()
		modifiers := []ModifierInfo{}
		for mRows.Next() {
			var m ModifierInfo
			if err := mRows.Scan(&m.ID, &m.ModifierID, &m.Metadata, &m.Enabled, &m.CreatedAt, &m.UpdatedAt); err == nil {
				m.AccountID = id
				modifiers = append(modifiers, m)
			}
		}
		a.Modifier = modifiers
	} else {
		a.Modifier = []ModifierInfo{}
	}

	response.JSON(w, http.StatusOK, a)
}

func (h *AccountHandler) Create(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	payload := middleware.GetBody[CreateAccountPayload](r)

	// Validate expiry date format
	expiry, err := time.Parse(time.RFC3339, payload.SubscriptionExpiry)
	if err != nil {
		response.Error(w, http.StatusBadRequest, "invalid subscription_expiry date format")
		return
	}

	var batchStart, batchEnd *time.Time
	if payload.BatchStartDate != nil && *payload.BatchStartDate != "" {
		t, err := time.Parse(time.RFC3339, *payload.BatchStartDate)
		if err != nil {
			response.Error(w, http.StatusBadRequest, "invalid batch_start_date format")
			return
		}
		batchStart = &t
	}
	if payload.BatchEndDate != nil && *payload.BatchEndDate != "" {
		t, err := time.Parse(time.RFC3339, *payload.BatchEndDate)
		if err != nil {
			response.Error(w, http.StatusBadRequest, "invalid batch_end_date format")
			return
		}
		batchEnd = &t
	}

	tx, err := h.dbPool.Begin(r.Context())
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to start transaction")
		return
	}
	defer tx.Rollback(r.Context())

	// Check if already exists
	var count int
	err = tx.QueryRow(r.Context(), fmt.Sprintf(`
		SELECT COUNT(*) FROM "%s".account 
		WHERE email_id = $1 AND product_variant_id = $2
	`, tenantID), payload.EmailID, payload.ProductVariantID).Scan(&count)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "failed checking account existence")
		return
	}
	if count > 0 {
		response.Error(w, http.StatusBadRequest, "Akun dengan email dan varian produk sudah ada")
		return
	}

	// Insert Account
	var a Account
	err = tx.QueryRow(r.Context(), fmt.Sprintf(`
		INSERT INTO "%s".account (account_password, subscription_expiry, status, billing, batch_start_date, batch_end_date, email_id, product_variant_id, pinned, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW(), NOW())
		RETURNING id, account_password, subscription_expiry, status, billing, batch_start_date, batch_end_date, email_id, product_variant_id, freeze_until, pinned, created_at, updated_at
	`, tenantID), payload.AccountPassword, expiry, payload.Status, payload.Billing, batchStart, batchEnd, payload.EmailID, payload.ProductVariantID, payload.Pinned).Scan(
		&a.ID, &a.AccountPassword, &a.SubscriptionExpiry, &a.Status, &a.Billing,
		&a.BatchStartDate, &a.BatchEndDate, &a.EmailID, &a.ProductVariantID,
		&a.FreezeUntil, &a.Pinned, &a.CreatedAt, &a.UpdatedAt,
	)
	if err != nil {
		slog.Error("failed to create account", "err", err)
		response.Error(w, http.StatusInternalServerError, "failed to insert account")
		return
	}

	// Insert Profiles
	var seededProfiles []ProfileInfo
	for _, p := range payload.Profile {
		var pr ProfileInfo
		err = tx.QueryRow(r.Context(), fmt.Sprintf(`
			INSERT INTO "%s".account_profile (name, max_user, allow_generate, account_id, created_at, updated_at)
			VALUES ($1, $2, $3, $4, NOW(), NOW())
			RETURNING id, name, max_user, allow_generate, account_id, created_at, updated_at
		`, tenantID), p.Name, p.MaxUser, p.AllowGenerate, a.ID).Scan(
			&pr.ID, &pr.Name, &pr.MaxUser, &pr.AllowGenerate, &pr.AccountID, &pr.CreatedAt, &pr.UpdatedAt,
		)
		if err != nil {
			slog.Error("failed to insert profile", "err", err)
			response.Error(w, http.StatusInternalServerError, "failed to insert profiles")
			return
		}
		seededProfiles = append(seededProfiles, pr)
	}
	a.Profile = seededProfiles

	// Insert Modifiers
	var seededModifiers []ModifierInfo
	for _, m := range payload.Modifier {
		var mod ModifierInfo
		err = tx.QueryRow(r.Context(), fmt.Sprintf(`
			INSERT INTO "%s".account_modifier (modifier_id, metadata, enabled, account_id, created_at, updated_at)
			VALUES ($1, $2, true, $3, NOW(), NOW())
			RETURNING id, modifier_id, metadata, enabled, account_id, created_at, updated_at
		`, tenantID), m.ModifierID, m.Metadata, a.ID).Scan(
			&mod.ID, &mod.ModifierID, &mod.Metadata, &mod.Enabled, &mod.AccountID, &mod.CreatedAt, &mod.UpdatedAt,
		)
		if err != nil {
			slog.Error("failed to insert modifier", "err", err)
			response.Error(w, http.StatusInternalServerError, "failed to insert modifiers")
			return
		}
		seededModifiers = append(seededModifiers, mod)
	}
	a.Modifier = seededModifiers

	if err := tx.Commit(r.Context()); err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to commit transaction")
		return
	}

	// Sign up tasks for background execution via Asynq
	if len(payload.Modifier) > 0 {
		h.registerModifiersToTaskQueue(tenantID, a.ID, a.SubscriptionExpiry, batchEnd, payload.Modifier)
	}

	response.JSON(w, http.StatusCreated, a)
}

func (h *AccountHandler) Update(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	payload := middleware.GetBody[UpdateAccountPayload](r)

	// Fetch current state
	var currentStatus string
	var currentExpiry time.Time
	var currentBatchEnd *time.Time
	err := h.dbPool.QueryRow(r.Context(), fmt.Sprintf(`
		SELECT status, subscription_expiry, batch_end_date FROM "%s".account WHERE id = $1
	`, tenantID), id).Scan(&currentStatus, &currentExpiry, &currentBatchEnd)
	if err != nil {
		response.Error(w, http.StatusNotFound, fmt.Sprintf("account dengan id: %d tidak ditemukan", id))
		return
	}

	tx, err := h.dbPool.Begin(r.Context())
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to start transaction")
		return
	}
	defer tx.Rollback(r.Context())

	// Dynamically build update query
	query := fmt.Sprintf(`UPDATE "%s".account SET `, tenantID)
	var args []interface{}
	argIdx := 1

	if payload.AccountPassword != nil {
		query += fmt.Sprintf("account_password = $%d, ", argIdx)
		args = append(args, *payload.AccountPassword)
		argIdx++
	}
	if payload.SubscriptionExpiry != nil {
		t, err := time.Parse(time.RFC3339, *payload.SubscriptionExpiry)
		if err != nil {
			response.Error(w, http.StatusBadRequest, "invalid subscription_expiry format")
			return
		}
		query += fmt.Sprintf("subscription_expiry = $%d, ", argIdx)
		args = append(args, t)
		argIdx++
		currentExpiry = t
	}
	if payload.Billing != nil {
		query += fmt.Sprintf("billing = $%d, ", argIdx)
		args = append(args, *payload.Billing)
		argIdx++
	}
	if payload.EmailID != nil {
		query += fmt.Sprintf("email_id = $%d, ", argIdx)
		args = append(args, *payload.EmailID)
		argIdx++
	}
	if payload.ProductVariantID != nil {
		query += fmt.Sprintf("product_variant_id = $%d, ", argIdx)
		args = append(args, *payload.ProductVariantID)
		argIdx++
	}
	if payload.Pinned != nil {
		query += fmt.Sprintf("pinned = $%d, ", argIdx)
		args = append(args, *payload.Pinned)
		argIdx++
	}

	statusChangedToNonActive := false
	if payload.Status != nil {
		query += fmt.Sprintf("status = $%d, ", argIdx)
		args = append(args, *payload.Status)
		argIdx++
		if *payload.Status != "active" {
			statusChangedToNonActive = true
		}
	}

	if statusChangedToNonActive {
		// Update users to expired
		_, err = tx.Exec(r.Context(), fmt.Sprintf(`
			UPDATE "%s".account_user SET status = 'expired' WHERE account_id = $1 AND status = 'active'
		`, tenantID), id)
		if err != nil {
			slog.Error("failed to expire users on account update", "err", err)
			response.Error(w, http.StatusInternalServerError, "failed to update users status")
			return
		}
		query += "batch_start_date = NULL, batch_end_date = NULL, "
		currentBatchEnd = nil
	} else {
		if payload.BatchStartDate != nil {
			if *payload.BatchStartDate == "" {
				query += "batch_start_date = NULL, "
			} else {
				t, err := time.Parse(time.RFC3339, *payload.BatchStartDate)
				if err != nil {
					response.Error(w, http.StatusBadRequest, "invalid batch_start_date format")
					return
				}
				query += fmt.Sprintf("batch_start_date = $%d, ", argIdx)
				args = append(args, t)
				argIdx++
			}
		}
		if payload.BatchEndDate != nil {
			if *payload.BatchEndDate == "" {
				query += "batch_end_date = NULL, "
				currentBatchEnd = nil
			} else {
				t, err := time.Parse(time.RFC3339, *payload.BatchEndDate)
				if err != nil {
					response.Error(w, http.StatusBadRequest, "invalid batch_end_date format")
					return
				}
				query += fmt.Sprintf("batch_end_date = $%d, ", argIdx)
				args = append(args, t)
				argIdx++
				currentBatchEnd = &t
			}
		}
	}

	query += "updated_at = NOW() "
	query += fmt.Sprintf("WHERE id = $%d", argIdx)
	args = append(args, id)

	_, err = tx.Exec(r.Context(), query, args...)
	if err != nil {
		slog.Error("failed to update account", "err", err)
		response.Error(w, http.StatusInternalServerError, "failed to update account")
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to commit transaction")
		return
	}

	// Update task queue jobs if expiry or batch end changed
	if payload.SubscriptionExpiry != nil || payload.BatchEndDate != nil {
		// Read enabled modifiers
		rows, err := h.dbPool.Query(r.Context(), fmt.Sprintf(`
			SELECT modifier_id, metadata FROM "%s".account_modifier WHERE account_id = $1 AND enabled = true
		`, tenantID), id)
		if err == nil {
			var mods []CreateModNested
			for rows.Next() {
				var m CreateModNested
				if err := rows.Scan(&m.ModifierID, &m.Metadata); err == nil {
					mods = append(mods, m)
				}
			}
			rows.Close()

			if len(mods) > 0 {
				h.registerModifiersToTaskQueue(tenantID, id, currentExpiry, currentBatchEnd, mods)
			}
		}
	}

	// Fetch and return updated account
	var updated Account
	err = h.dbPool.QueryRow(r.Context(), fmt.Sprintf(`
		SELECT id, account_password, subscription_expiry, status, billing, batch_start_date, batch_end_date, email_id, product_variant_id, freeze_until, pinned, created_at, updated_at
		FROM "%s".account WHERE id = $1
	`, tenantID), id).Scan(
		&updated.ID, &updated.AccountPassword, &updated.SubscriptionExpiry, &updated.Status, &updated.Billing,
		&updated.BatchStartDate, &updated.BatchEndDate, &updated.EmailID, &updated.ProductVariantID,
		&updated.FreezeUntil, &updated.Pinned, &updated.CreatedAt, &updated.UpdatedAt,
	)
	response.JSON(w, http.StatusOK, updated)
}

func (h *AccountHandler) UpdateModifier(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	payload := middleware.GetBody[UpdateModifierPayload](r)

	// Fetch current details
	var expiry time.Time
	var batchEnd *time.Time
	err := h.dbPool.QueryRow(r.Context(), fmt.Sprintf(`
		SELECT subscription_expiry, batch_end_date FROM "%s".account WHERE id = $1
	`, tenantID), id).Scan(&expiry, &batchEnd)
	if err != nil {
		response.Error(w, http.StatusNotFound, fmt.Sprintf("account dengan id: %d tidak ditemukan", id))
		return
	}

	tx, err := h.dbPool.Begin(r.Context())
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to start transaction")
		return
	}
	defer tx.Rollback(r.Context())

	var modsToRegister []CreateModNested
	var modsToRemove []string

	for _, m := range payload.Modifier {
		meta := ""
		if m.Metadata != nil {
			meta = *m.Metadata
		}

		if m.Action == "ADD" {
			// Check if already exists
			var count int
			_ = tx.QueryRow(r.Context(), fmt.Sprintf(`
				SELECT COUNT(*) FROM "%s".account_modifier WHERE account_id = $1 AND modifier_id = $2
			`, tenantID), id, m.ModifierID).Scan(&count)

			if count > 0 {
				_, err = tx.Exec(r.Context(), fmt.Sprintf(`
					UPDATE "%s".account_modifier SET enabled = true, metadata = $1, updated_at = NOW() WHERE account_id = $2 AND modifier_id = $3
				`, tenantID), meta, id, m.ModifierID)
			} else {
				_, err = tx.Exec(r.Context(), fmt.Sprintf(`
					INSERT INTO "%s".account_modifier (modifier_id, metadata, enabled, account_id, created_at, updated_at)
					VALUES ($1, $2, true, $3, NOW(), NOW())
				`, tenantID), m.ModifierID, meta, id)
			}
			if err != nil {
				slog.Error("failed to add modifier", "err", err)
				response.Error(w, http.StatusInternalServerError, "failed to update modifiers")
				return
			}
			modsToRegister = append(modsToRegister, CreateModNested{ModifierID: m.ModifierID, Metadata: meta})
		} else if m.Action == "UPDATE" {
			_, err = tx.Exec(r.Context(), fmt.Sprintf(`
				UPDATE "%s".account_modifier SET metadata = $1, enabled = true, updated_at = NOW() WHERE account_id = $2 AND modifier_id = $3
			`, tenantID), meta, id, m.ModifierID)
			if err != nil {
				slog.Error("failed to update modifier", "err", err)
				response.Error(w, http.StatusInternalServerError, "failed to update modifiers")
				return
			}
			modsToRegister = append(modsToRegister, CreateModNested{ModifierID: m.ModifierID, Metadata: meta})
		} else if m.Action == "REMOVE" {
			_, err = tx.Exec(r.Context(), fmt.Sprintf(`
				UPDATE "%s".account_modifier SET enabled = false, updated_at = NOW() WHERE account_id = $2 AND modifier_id = $3
			`, tenantID), id, m.ModifierID)
			if err != nil {
				slog.Error("failed to remove modifier", "err", err)
				response.Error(w, http.StatusInternalServerError, "failed to remove modifiers")
				return
			}
			modsToRemove = append(modsToRemove, m.ModifierID)
		}
	}

	if err := tx.Commit(r.Context()); err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to commit transaction")
		return
	}

	// Remove cancelled jobs from task_queue database
	if len(modsToRemove) > 0 {
		h.removeJobsFromTaskQueue(tenantID, id, modsToRemove)
	}

	// Register added/updated jobs
	if len(modsToRegister) > 0 {
		h.registerModifiersToTaskQueue(tenantID, id, expiry, batchEnd, modsToRegister)
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *AccountHandler) Freeze(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	payload := middleware.GetBody[FreezePayload](r)

	// Validate account existence
	var count int
	_ = h.dbPool.QueryRow(r.Context(), fmt.Sprintf(`SELECT COUNT(*) FROM "%s".account WHERE id = $1`, tenantID), id).Scan(&count)
	if count == 0 {
		response.Error(w, http.StatusNotFound, "account not found")
		return
	}

	freezeUntil := time.Now().Add(time.Duration(payload.Duration) * time.Millisecond)

	tx, err := h.dbPool.Begin(r.Context())
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to start transaction")
		return
	}
	defer tx.Rollback(r.Context())

	// 1. Update account status to frozen
	_, err = tx.Exec(r.Context(), fmt.Sprintf(`
		UPDATE "%s".account SET freeze_until = $1, updated_at = NOW() WHERE id = $2
	`, tenantID), freezeUntil, id)
	if err != nil {
		slog.Error("failed to freeze account in DB", "err", err)
		response.Error(w, http.StatusInternalServerError, "failed to freeze account")
		return
	}

	// 2. Insert into task_queue in master schema
	var taskID int64
	err = tx.QueryRow(r.Context(), `
		INSERT INTO master.task_queue (execute_at, subject_id, context, payload, status, tenant_id)
		VALUES ($1, $2, 'unfreezeAccount', $3, 'QUEUED', $4)
		RETURNING id
	`, freezeUntil, fmt.Sprintf("%d", id), fmt.Sprintf(`{"accountId":"%d"}`, id), tenantID).Scan(&taskID)
	if err != nil {
		slog.Error("failed to insert unfreeze task into task_queue", "err", err)
		response.Error(w, http.StatusInternalServerError, "failed to schedule unfreeze task")
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to commit transaction")
		return
	}

	// 3. Register Asynq task at the future time
	taskIDStr := fmt.Sprintf("%d", taskID)
	err = scheduler.EnqueueTask(h.asynqClient, r.Context(), scheduler.TypeUnfreezeAccount, tenantID, taskIDStr, scheduler.UnfreezeAccountPayload{
		AccountID: id,
	}, freezeUntil)
	if err != nil {
		slog.Error("failed to enqueue unfreeze task in Asynq", "err", err)
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *AccountHandler) Unfreeze(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)

	// Validate account existence
	var count int
	_ = h.dbPool.QueryRow(r.Context(), fmt.Sprintf(`SELECT COUNT(*) FROM "%s".account WHERE id = $1`, tenantID), id).Scan(&count)
	if count == 0 {
		response.Error(w, http.StatusNotFound, "account not found")
		return
	}

	tx, err := h.dbPool.Begin(r.Context())
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to start transaction")
		return
	}
	defer tx.Rollback(r.Context())

	// 1. Clear freeze_until
	_, err = tx.Exec(r.Context(), fmt.Sprintf(`
		UPDATE "%s".account SET freeze_until = NULL, updated_at = NOW() WHERE id = $2
	`, tenantID), id)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to unfreeze account")
		return
	}

	// 2. Remove unfreeze jobs from database task_queue
	_, err = tx.Exec(r.Context(), `
		DELETE FROM master.task_queue 
		WHERE tenant_id = $1 AND subject_id = $2 AND context = 'unfreezeAccount'
	`, tenantID, fmt.Sprintf("%d", id))
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to clear unfreeze task queue")
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to commit transaction")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *AccountHandler) Remove(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)

	tx, err := h.dbPool.Begin(r.Context())
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to start transaction")
		return
	}
	defer tx.Rollback(r.Context())

	// Delete from account (triggers cascade on profiles, users, modifiers)
	res, err := tx.Exec(r.Context(), fmt.Sprintf(`DELETE FROM "%s".account WHERE id = $1`, tenantID), id)
	if err != nil {
		slog.Error("failed to delete account", "id", id, "err", err)
		response.Error(w, http.StatusInternalServerError, "failed to delete account")
		return
	}

	if res.RowsAffected() == 0 {
		response.Error(w, http.StatusNotFound, fmt.Sprintf("account dengan id: %d tidak ditemukan", id))
		return
	}

	// Clear task queue jobs from master
	_, err = tx.Exec(r.Context(), `
		DELETE FROM master.task_queue 
		WHERE tenant_id = $1 AND subject_id = $2
	`, tenantID, fmt.Sprintf("%d", id))
	if err != nil {
		slog.Error("failed to delete account tasks", "id", id, "err", err)
		response.Error(w, http.StatusInternalServerError, "failed to clear task queue")
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to commit transaction")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *AccountHandler) CountStatusAccount(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	variantFilter := r.URL.Query().Get("product_variant_id")

	// Query 1: disabled/frozen count
	disabledQuery := fmt.Sprintf(`
		SELECT COUNT(*) FROM "%s".account
		WHERE status = 'disable' OR freeze_until IS NOT NULL
	`, tenantID)
	var disabledArgs []interface{}
	if variantFilter != "" {
		vID, _ := strconv.ParseInt(variantFilter, 10, 64)
		disabledQuery += " AND product_variant_id = $1"
		disabledArgs = append(disabledArgs, vID)
	}

	var disabledCount int64
	err := h.dbPool.QueryRow(r.Context(), disabledQuery, disabledArgs...).Scan(&disabledCount)
	if err != nil {
		slog.Error("failed counting disabled accounts", "err", err)
		response.Error(w, http.StatusInternalServerError, "database count failed")
		return
	}

	// Query 2: expiring today count
	expiringQuery := fmt.Sprintf(`
		SELECT COUNT(*) FROM "%s".account
		WHERE (batch_end_date AT TIME ZONE 'Asia/Jakarta')::date = (NOW() AT TIME ZONE 'Asia/Jakarta')::date
	`, tenantID)
	var expiringArgs []interface{}
	if variantFilter != "" {
		vID, _ := strconv.ParseInt(variantFilter, 10, 64)
		expiringQuery += " AND product_variant_id = $1"
		expiringArgs = append(expiringArgs, vID)
	}

	var expiringCount int64
	err = h.dbPool.QueryRow(r.Context(), expiringQuery, expiringArgs...).Scan(&expiringCount)
	if err != nil {
		slog.Error("failed counting expiring accounts", "err", err)
		response.Error(w, http.StatusInternalServerError, "database count failed")
		return
	}

	// Query 3: CTE stats for slots
	cteFilterSql := ""
	var cteArgs []interface{}
	if variantFilter != "" {
		vID, _ := strconv.ParseInt(variantFilter, 10, 64)
		cteFilterSql = "AND a.product_variant_id = $1"
		cteArgs = append(cteArgs, vID)
	}

	cteQuery := fmt.Sprintf(`
		WITH
		user_counts AS (
			SELECT account_profile_id, COUNT(*) as active_count
			FROM "%s".account_user
			WHERE status = 'active'
			GROUP BY account_profile_id
		),

		profile_calc AS (
			SELECT
				ap.id AS profile_id,
				ap.account_id,
				ap.allow_generate,
				ap.max_user,
				COALESCE(uc.active_count, 0) as current_usage,
				(CASE WHEN a.status != 'disable' AND a.freeze_until IS NULL THEN 1 ELSE 0 END) as is_account_valid,
				(CASE WHEN COALESCE(uc.active_count, 0) < ap.max_user THEN 1 ELSE 0 END) as has_slot
			FROM "%s".account_profile ap
			JOIN "%s".account a ON a.id = ap.account_id
			LEFT JOIN user_counts uc ON uc.account_profile_id = ap.id
			WHERE 1=1 %s
		),

		account_agg AS (
			SELECT
				account_id,
				COUNT(CASE WHEN allow_generate = true AND has_slot = 1 THEN 1 END) as available_gen_profiles,
				COUNT(CASE WHEN allow_generate = true THEN 1 END) as total_gen_profiles
			FROM profile_calc
			WHERE is_account_valid = 1
			GROUP BY account_id
		)

		SELECT
			(SELECT COUNT(*) FROM profile_calc
			 WHERE is_account_valid = 1 AND allow_generate = true AND has_slot = 1
			)::int as profiles_available,

			(SELECT COUNT(*) FROM profile_calc
			 WHERE is_account_valid = 1 AND allow_generate = false AND has_slot = 1
			)::int as profiles_locked,

			COUNT(CASE WHEN available_gen_profiles > 0 THEN 1 END)::int as accounts_providing_slots,

			COUNT(CASE WHEN total_gen_profiles > 0 AND available_gen_profiles = 0 THEN 1 END)::int as accounts_full
		FROM account_agg;
	`, tenantID, tenantID, tenantID, cteFilterSql)

	var profilesAvailable, profilesLocked, accountsWithSlots, accountsFull int
	err = h.dbPool.QueryRow(r.Context(), cteQuery, cteArgs...).Scan(&profilesAvailable, &profilesLocked, &accountsWithSlots, &accountsFull)
	if err != nil && err != sql.ErrNoRows {
		slog.Error("failed executing CTE slot stats", "err", err)
		response.Error(w, http.StatusInternalServerError, "database aggregation failed")
		return
	}

	response.JSON(w, http.StatusOK, map[string]interface{}{
		"accounts_with_slots":          accountsWithSlots,
		"accounts_full":                accountsFull,
		"profiles_available":           profilesAvailable,
		"accounts_disabled_or_frozen":  disabledCount,
		"profiles_locked_but_has_slot": profilesLocked,
		"accounts_expiring_today":      expiringCount,
	})
}

// Background scheduler helpers
func (h *AccountHandler) removeJobsFromTaskQueue(tenantID string, accountID int64, contexts []string) {
	// 1. Delete from DB
	query := `DELETE FROM master.task_queue WHERE tenant_id = $1 AND subject_id = $2 AND context = ANY($3)`
	_, err := h.dbPool.Exec(context.Background(), query, tenantID, fmt.Sprintf("%d", accountID), contexts)
	if err != nil {
		slog.Error("failed to clear jobs from DB task_queue", "account_id", accountID, "err", err)
	}
}

func (h *AccountHandler) registerModifiersToTaskQueue(tenantID string, accountID int64, expiry time.Time, batchEnd *time.Time, modifiers []CreateModNested) {
	// First clean old jobs for these contexts to make sure we upsert cleanly
	var contexts []string
	for _, m := range modifiers {
		contexts = append(contexts, m.ModifierID)
	}
	h.removeJobsFromTaskQueue(tenantID, accountID, contexts)

	// Fetch Account email and variant product name for logging/payload context
	var emailStr, productName string
	err := h.dbPool.QueryRow(context.Background(), fmt.Sprintf(`
		SELECT e.email, p.name 
		FROM "%s".account a
		JOIN "%s".email e ON a.email_id = e.id
		JOIN "%s".product_variant pv ON a.product_variant_id = pv.id
		JOIN "%s".product p ON pv.product_id = p.id
		WHERE a.id = $1
	`, tenantID, tenantID, tenantID, tenantID), accountID).Scan(&emailStr, &productName)
	if err != nil {
		slog.Error("failed to query email/product for task registration", "account_id", accountID, "err", err)
		return
	}

	for _, mod := range modifiers {
		var executeAt time.Time
		var payload interface{}

		switch mod.ModifierID {
		case "accountSubsEndNotify":
			// Generate two reminder jobs: D-Day and D-Day minus metadata offsets
			var meta struct {
				Dday string `json:"dday"`
			}
			_ = json.Unmarshal([]byte(mod.Metadata), &meta)
			ddayOffset, _ := strconv.Atoi(meta.Dday)
			if ddayOffset <= 0 {
				ddayOffset = 3 // default
			}

			// Job 1: D-Day at 7:00 AM
			dday := time.Date(expiry.Year(), expiry.Month(), expiry.Day(), 7, 0, 0, 0, expiry.Location())
			ddayFormatted := expiry.Format("2006-01-02")
			
			payload1 := scheduler.BasePayload[scheduler.AccountSubsEndNotifyPayload]{
				TenantID: tenantID,
				Data: scheduler.AccountSubsEndNotifyPayload{
					Message: fmt.Sprintf("Langganan (Subscription) akun %s [%s] telah berakhir hari ini %s.\n\nSilahkan lakukan tindakan", emailStr, productName, ddayFormatted),
				},
			}
			h.saveAndEnqueue(tenantID, accountID, "accountSubsEndNotify", scheduler.TypeAccountSubsEndNotify, payload1, dday)

			// Job 2: D-Day minus offset days at 7:00 AM
			ddayMin := dday.AddDate(0, 0, -ddayOffset)
			payload2 := scheduler.BasePayload[scheduler.AccountSubsEndNotifyPayload]{
				TenantID: tenantID,
				Data: scheduler.AccountSubsEndNotifyPayload{
					Message: fmt.Sprintf("Langganan (Subscription) akun %s [%s] akan berakhir %d hari lagi pada %s.\n\nSilahkan lakukan tindakan", emailStr, productName, ddayOffset, ddayFormatted),
				},
			}
			h.saveAndEnqueue(tenantID, accountID, "accountSubsEndNotify", scheduler.TypeAccountSubsEndNotify, payload2, ddayMin)

		case "netflixResetPassword":
			if batchEnd == nil {
				continue
			}
			executeAt = *batchEnd
			
			// Get account password
			var accPass string
			_ = h.dbPool.QueryRow(context.Background(), fmt.Sprintf(`SELECT account_password FROM "%s".account WHERE id = $1`, tenantID), accountID).Scan(&accPass)

			payload = scheduler.NetflixResetPasswordPayload{
				AccountID: accountID,
				Email:     emailStr,
			}
			h.saveAndEnqueue(tenantID, accountID, "netflixResetPassword", scheduler.TypeNetflixResetPassword, payload, executeAt)

		case "subsEndDisableAccount":
			var meta struct {
				Offset     string `json:"offset"`
				OffsetUnit string `json:"offset_unit"`
			}
			_ = json.Unmarshal([]byte(mod.Metadata), &meta)
			offsetVal, _ := strconv.Atoi(meta.Offset)
			unit := meta.OffsetUnit
			if unit == "" {
				unit = "day"
			}

			var duration time.Duration
			switch unit {
			case "millisecond":
				duration = time.Duration(offsetVal) * time.Millisecond
			case "second":
				duration = time.Duration(offsetVal) * time.Second
			case "minute":
				duration = time.Duration(offsetVal) * time.Minute
			case "hour":
				duration = time.Duration(offsetVal) * time.Hour
			case "day":
				duration = time.Duration(offsetVal) * 24 * time.Hour
			}

			executeAt = expiry.Add(-duration)
			payload = scheduler.SubsEndDisableAccountPayload{
				AccountID: accountID,
			}
			h.saveAndEnqueue(tenantID, accountID, "subsEndDisableAccount", scheduler.TypeSubsEndDisable, payload, executeAt)
		}
	}
}

func (h *AccountHandler) saveAndEnqueue(tenantID string, accountID int64, contextKey string, taskType string, payload interface{}, executeAt time.Time) {
	bytes, _ := json.Marshal(payload)

	// Save task in Postgres
	var taskID int64
	err := h.dbPool.QueryRow(context.Background(), `
		INSERT INTO master.task_queue (execute_at, subject_id, context, payload, status, tenant_id)
		VALUES ($1, $2, $3, $4, 'QUEUED', $5)
		RETURNING id
	`, executeAt, fmt.Sprintf("%d", accountID), contextKey, string(bytes), tenantID).Scan(&taskID)
	if err != nil {
		slog.Error("failed to insert task into db queue", "err", err)
		return
	}

	// Enqueue in Asynq scheduler
	taskIDStr := fmt.Sprintf("%d", taskID)
	err = scheduler.EnqueueTask(h.asynqClient, context.Background(), taskType, tenantID, taskIDStr, payload, executeAt)
	if err != nil {
		slog.Error("failed to enqueue modifier task in Asynq", "task_id", taskIDStr, "err", err)
	}
}

// ==========================================
// ACCOUNT PROFILE CRUD
// ==========================================

type CreateProfilePayload struct {
	Name          string `json:"name" validate:"required"`
	MaxUser       int    `json:"max_user" validate:"required"`
	AllowGenerate bool   `json:"allow_generate"`
	AccountID     int64  `json:"account_id,string" validate:"required"`
}

type UpdateProfilePayload struct {
	Name          *string `json:"name"`
	MaxUser       *int    `json:"max_user"`
	AllowGenerate *bool   `json:"allow_generate"`
}

func (h *AccountHandler) FindAllProfiles(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	page, limit, offset := response.ParsePagination(r)

	q := r.URL.Query()
	accountFilter := q.Get("account_id")

	var profiles []ProfileInfo
	var total int64

	whereClause := ""
	var args []interface{}
	if accountFilter != "" {
		accID, _ := strconv.ParseInt(accountFilter, 10, 64)
		whereClause = "WHERE account_id = $1"
		args = append(args, accID)
	}

	countQuery := fmt.Sprintf(`SELECT COUNT(*) FROM "%s".account_profile %s`, tenantID, whereClause)
	err := h.dbPool.QueryRow(r.Context(), countQuery, args...).Scan(&total)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "database count failed")
		return
	}

	selectQuery := fmt.Sprintf(`
		SELECT id, name, max_user, allow_generate, account_id, created_at, updated_at
		FROM "%s".account_profile
		%s
		ORDER BY name ASC
		LIMIT $%d OFFSET $%d
	`, tenantID, whereClause, len(args)+1, len(args)+2)

	selectArgs := append(args, limit, offset)
	rows, err := h.dbPool.Query(r.Context(), selectQuery, selectArgs...)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "database query failed")
		return
	}
	defer rows.Close()

	for rows.Next() {
		var pr ProfileInfo
		err = rows.Scan(&pr.ID, &pr.Name, &pr.MaxUser, &pr.AllowGenerate, &pr.AccountID, &pr.CreatedAt, &pr.UpdatedAt)
		if err != nil {
			response.Error(w, http.StatusInternalServerError, "database scan failed")
			return
		}
		profiles = append(profiles, pr)
	}

	response.JSON(w, http.StatusOK, response.NewPaginationResponse(profiles, total, page, limit))
}

func (h *AccountHandler) FindOneProfile(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)

	var pr ProfileInfo
	err := h.dbPool.QueryRow(r.Context(), fmt.Sprintf(`
		SELECT id, name, max_user, allow_generate, account_id, created_at, updated_at
		FROM "%s".account_profile WHERE id = $1
	`, tenantID), id).Scan(&pr.ID, &pr.Name, &pr.MaxUser, &pr.AllowGenerate, &pr.AccountID, &pr.CreatedAt, &pr.UpdatedAt)
	if err != nil {
		response.Error(w, http.StatusNotFound, fmt.Sprintf("accountProfile dengan id: %d tidak ditemukan", id))
		return
	}

	response.JSON(w, http.StatusOK, pr)
}

func (h *AccountHandler) CreateProfile(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	payload := middleware.GetBody[CreateProfilePayload](r)

	// Validate account existence
	var count int
	_ = h.dbPool.QueryRow(r.Context(), fmt.Sprintf(`SELECT COUNT(*) FROM "%s".account WHERE id = $1`, tenantID), payload.AccountID).Scan(&count)
	if count == 0 {
		response.Error(w, http.StatusBadRequest, "associated account not found")
		return
	}

	var pr ProfileInfo
	err := h.dbPool.QueryRow(r.Context(), fmt.Sprintf(`
		INSERT INTO "%s".account_profile (name, max_user, allow_generate, account_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, NOW(), NOW())
		RETURNING id, name, max_user, allow_generate, account_id, created_at, updated_at
	`, tenantID), payload.Name, payload.MaxUser, payload.AllowGenerate, payload.AccountID).Scan(&pr.ID, &pr.Name, &pr.MaxUser, &pr.AllowGenerate, &pr.AccountID, &pr.CreatedAt, &pr.UpdatedAt)

	if err != nil {
		slog.Error("failed to create profile", "err", err)
		response.Error(w, http.StatusInternalServerError, "failed to insert profile")
		return
	}

	response.JSON(w, http.StatusCreated, pr)
}

func (h *AccountHandler) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	payload := middleware.GetBody[UpdateProfilePayload](r)

	tx, err := h.dbPool.Begin(r.Context())
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to start transaction")
		return
	}
	defer tx.Rollback(r.Context())

	// Build update query
	query := fmt.Sprintf(`UPDATE "%s".account_profile SET `, tenantID)
	var args []interface{}
	argIdx := 1

	if payload.Name != nil {
		query += fmt.Sprintf("name = $%d, ", argIdx)
		args = append(args, *payload.Name)
		argIdx++
	}
	if payload.MaxUser != nil {
		query += fmt.Sprintf("max_user = $%d, ", argIdx)
		args = append(args, *payload.MaxUser)
		argIdx++
	}
	if payload.AllowGenerate != nil {
		query += fmt.Sprintf("allow_generate = $%d, ", argIdx)
		args = append(args, *payload.AllowGenerate)
		argIdx++
	}

	query += "updated_at = NOW() "
	query += fmt.Sprintf("WHERE id = $%d", argIdx)
	args = append(args, id)

	res, err := tx.Exec(r.Context(), query, args...)
	if err != nil {
		slog.Error("failed to update profile", "err", err)
		response.Error(w, http.StatusInternalServerError, "failed to update profile")
		return
	}

	if res.RowsAffected() == 0 {
		response.Error(w, http.StatusNotFound, fmt.Sprintf("accountProfile dengan id: %d tidak ditemukan", id))
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to commit transaction")
		return
	}

	var pr ProfileInfo
	_ = h.dbPool.QueryRow(r.Context(), fmt.Sprintf(`
		SELECT id, name, max_user, allow_generate, account_id, created_at, updated_at
		FROM "%s".account_profile WHERE id = $1
	`, tenantID), id).Scan(&pr.ID, &pr.Name, &pr.MaxUser, &pr.AllowGenerate, &pr.AccountID, &pr.CreatedAt, &pr.UpdatedAt)

	response.JSON(w, http.StatusOK, pr)
}

func (h *AccountHandler) RemoveProfile(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)

	res, err := h.dbPool.Exec(r.Context(), fmt.Sprintf(`DELETE FROM "%s".account_profile WHERE id = $1`, tenantID), id)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to delete profile")
		return
	}

	if res.RowsAffected() == 0 {
		response.Error(w, http.StatusNotFound, fmt.Sprintf("accountProfile dengan id: %d tidak ditemukan", id))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ==========================================
// ACCOUNT USER CRUD
// ==========================================

type CreateUserPayload struct {
	Name             string  `json:"name" validate:"required"`
	ExpiredAt        *string `json:"expired_at"`
	AccountProfileID *int64  `json:"account_profile_id,string"`
	ProductVariantID int64   `json:"product_variant_id,string" validate:"required"`
}

type UpdateUserPayload struct {
	Name      *string `json:"name"`
	Status    *string `json:"status"`
	ExpiredAt *string `json:"expired_at"`
}

func (h *AccountHandler) FindAllUsers(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	page, limit, offset := response.ParsePagination(r)

	q := r.URL.Query()
	profileFilter := q.Get("account_profile_id")
	statusFilter := q.Get("status")

	var users []UserInfo
	var total int64

	var conditions []string
	var args []interface{}
	argIdx := 1

	if profileFilter != "" {
		pID, _ := strconv.ParseInt(profileFilter, 10, 64)
		conditions = append(conditions, fmt.Sprintf("account_profile_id = $%d", argIdx))
		args = append(args, pID)
		argIdx++
	}
	if statusFilter != "" {
		conditions = append(conditions, fmt.Sprintf("status = $%d", argIdx))
		args = append(args, statusFilter)
		argIdx++
	}

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = "WHERE " + strings.Join(conditions, " AND ")
	}

	countQuery := fmt.Sprintf(`SELECT COUNT(*) FROM "%s".account_user %s`, tenantID, whereClause)
	err := h.dbPool.QueryRow(r.Context(), countQuery, args...).Scan(&total)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "database count failed")
		return
	}

	selectQuery := fmt.Sprintf(`
		SELECT id, name, status, expired_at, account_profile_id, account_id, created_at, updated_at
		FROM "%s".account_user
		%s
		ORDER BY created_at DESC
		LIMIT $%d OFFSET $%d
	`, tenantID, whereClause, argIdx, argIdx+1)

	selectArgs := append(args, limit, offset)
	rows, err := h.dbPool.Query(r.Context(), selectQuery, selectArgs...)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "database query failed")
		return
	}
	defer rows.Close()

	for rows.Next() {
		var u UserInfo
		err = rows.Scan(&u.ID, &u.Name, &u.Status, &u.ExpiredAt, &u.AccountProfileID, &u.AccountID, &u.CreatedAt, &u.UpdatedAt)
		if err != nil {
			response.Error(w, http.StatusInternalServerError, "database scan failed")
			return
		}
		users = append(users, u)
	}

	response.JSON(w, http.StatusOK, response.NewPaginationResponse(users, total, page, limit))
}

func (h *AccountHandler) FindOneUser(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)

	var u UserInfo
	err := h.dbPool.QueryRow(r.Context(), fmt.Sprintf(`
		SELECT id, name, status, expired_at, account_profile_id, account_id, created_at, updated_at
		FROM "%s".account_user WHERE id = $1
	`, tenantID), id).Scan(&u.ID, &u.Name, &u.Status, &u.ExpiredAt, &u.AccountProfileID, &u.AccountID, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		response.Error(w, http.StatusNotFound, fmt.Sprintf("accountUser dengan id: %d tidak ditemukan", id))
		return
	}

	response.JSON(w, http.StatusOK, u)
}

func (h *AccountHandler) CreateUser(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	payload := middleware.GetBody[CreateUserPayload](r)

	var expiredAt *time.Time
	if payload.ExpiredAt != nil && *payload.ExpiredAt != "" {
		t, err := time.Parse(time.RFC3339, *payload.ExpiredAt)
		if err != nil {
			response.Error(w, http.StatusBadRequest, "invalid expired_at format")
			return
		}
		expiredAt = &t
	}

	tx, err := h.dbPool.Begin(r.Context())
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to start transaction")
		return
	}
	defer tx.Rollback(r.Context())

	var finalProfileID int64
	var finalAccountID int64

	if payload.AccountProfileID != nil {
		// Specific profile ID provided
		finalProfileID = *payload.AccountProfileID
		
		// Check that profile exists and has available slots
		var maxUser, currentUserCount int
		err = tx.QueryRow(r.Context(), fmt.Sprintf(`
			SELECT ap.account_id, ap.max_user, COUNT(au.id) 
			FROM "%s".account_profile ap
			LEFT JOIN "%s".account_user au ON ap.id = au.account_profile_id AND au.status = 'active'
			WHERE ap.id = $1
			GROUP BY ap.account_id, ap.max_user
		`, tenantID, tenantID), finalProfileID).Scan(&finalAccountID, &maxUser, &currentUserCount)

		if err != nil {
			response.Error(w, http.StatusBadRequest, "associated profile not found")
			return
		}

		if currentUserCount >= maxUser {
			response.Error(w, http.StatusBadRequest, "Account profile full")
			return
		}
	} else {
		// DYNAMIC CTE SLOT LOOKUP PRESERVED EXACTLY AS NestJS SQL
		sqlQuery := fmt.Sprintf(`WITH now_ts AS (
				SELECT NOW() AS current_time
			),
			candidate_stats AS (
				SELECT
					ap.id AS profile_id,
					ap.account_id,
					pv.cooldown,
					ap.max_user,
					COUNT(au.id) AS current_user_count,
					MAX(au.created_at) AS last_user_created_at,
					SUM(COUNT(au.id)) OVER(PARTITION BY ap.account_id) AS total_account_users
				FROM
					"%s".account_profile AS ap
					JOIN "%s".account AS a ON ap.account_id = a.id
					JOIN "%s".product_variant AS pv ON a.product_variant_id = pv.id
					LEFT JOIN "%s".account_user AS au
						ON ap.id = au.account_profile_id
						AND au.status = 'active'
				WHERE
					a.product_variant_id = $1
					AND ap.allow_generate = true
					AND a.status != 'disable'
					AND a.freeze_until IS NULL
				GROUP BY
					ap.id, pv.cooldown, ap.max_user, ap.account_id
			)
			SELECT
				cs.profile_id AS "candidateProfileId",
				cs.account_id AS "accountId",
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
			LIMIT 1;`, tenantID, tenantID, tenantID, tenantID)

		var candidateProfileId, accountId int64
		var status string
		err = tx.QueryRow(r.Context(), sqlQuery, payload.ProductVariantID).Scan(&candidateProfileId, &accountId, &status)
		if err != nil {
			slog.Warn("no candidate profile found for variant", "variant_id", payload.ProductVariantID)
			response.Error(w, http.StatusBadRequest, "No account profile available")
			return
		}

		if status == "cooldown" {
			response.Error(w, http.StatusBadRequest, "All accounts are currently in cooldown")
			return
		}

		finalProfileID = candidateProfileId
		finalAccountID = accountId
	}

	var u UserInfo
	err = tx.QueryRow(r.Context(), fmt.Sprintf(`
		INSERT INTO "%s".account_user (name, status, expired_at, account_profile_id, account_id, created_at, updated_at)
		VALUES ($1, 'active', $2, $3, $4, NOW(), NOW())
		RETURNING id, name, status, expired_at, account_profile_id, account_id, created_at, updated_at
	`, tenantID), payload.Name, expiredAt, finalProfileID, finalAccountID).Scan(&u.ID, &u.Name, &u.Status, &u.ExpiredAt, &u.AccountProfileID, &u.AccountID, &u.CreatedAt, &u.UpdatedAt)

	if err != nil {
		slog.Error("failed to create user", "err", err)
		response.Error(w, http.StatusInternalServerError, "failed to insert user")
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to commit transaction")
		return
	}

	response.JSON(w, http.StatusCreated, u)
}

func (h *AccountHandler) UpdateUser(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	payload := middleware.GetBody[UpdateUserPayload](r)

	tx, err := h.dbPool.Begin(r.Context())
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to start transaction")
		return
	}
	defer tx.Rollback(r.Context())

	// Build update query
	query := fmt.Sprintf(`UPDATE "%s".account_user SET `, tenantID)
	var args []interface{}
	argIdx := 1

	if payload.Name != nil {
		query += fmt.Sprintf("name = $%d, ", argIdx)
		args = append(args, *payload.Name)
		argIdx++
	}
	if payload.Status != nil {
		query += fmt.Sprintf("status = $%d, ", argIdx)
		args = append(args, *payload.Status)
		argIdx++
	}
	if payload.ExpiredAt != nil {
		if *payload.ExpiredAt == "" {
			query += "expired_at = NULL, "
		} else {
			t, err := time.Parse(time.RFC3339, *payload.ExpiredAt)
			if err != nil {
				response.Error(w, http.StatusBadRequest, "invalid expired_at format")
				return
			}
			query += fmt.Sprintf("expired_at = $%d, ", argIdx)
			args = append(args, t)
			argIdx++
		}
	}

	query += "updated_at = NOW() "
	query += fmt.Sprintf("WHERE id = $%d", argIdx)
	args = append(args, id)

	res, err := tx.Exec(r.Context(), query, args...)
	if err != nil {
		slog.Error("failed to update user", "err", err)
		response.Error(w, http.StatusInternalServerError, "failed to update user")
		return
	}

	if res.RowsAffected() == 0 {
		response.Error(w, http.StatusNotFound, fmt.Sprintf("accountUser dengan id: %d tidak ditemukan", id))
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to commit transaction")
		return
	}

	var u UserInfo
	_ = h.dbPool.QueryRow(r.Context(), fmt.Sprintf(`
		SELECT id, name, status, expired_at, account_profile_id, account_id, created_at, updated_at
		FROM "%s".account_user WHERE id = $1
	`, tenantID), id).Scan(&u.ID, &u.Name, &u.Status, &u.ExpiredAt, &u.AccountProfileID, &u.AccountID, &u.CreatedAt, &u.UpdatedAt)

	response.JSON(w, http.StatusOK, u)
}

func (h *AccountHandler) RemoveUser(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)

	res, err := h.dbPool.Exec(r.Context(), fmt.Sprintf(`DELETE FROM "%s".account_user WHERE id = $1`, tenantID), id)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "failed to delete user")
		return
	}

	if res.RowsAffected() == 0 {
		response.Error(w, http.StatusNotFound, fmt.Sprintf("accountUser dengan id: %d tidak ditemukan", id))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
