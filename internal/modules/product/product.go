package product

import (
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
type Product struct {
	ID        int64            `json:"id,string"`
	Name      string           `json:"name"`
	CreatedAt time.Time        `json:"created_at"`
	UpdatedAt time.Time        `json:"updated_at"`
	Variants  []ProductVariant `json:"variants,omitempty"`
}

type ProductVariant struct {
	ID           int64     `json:"id,string"`
	Name         string    `json:"name"`
	Duration     int64     `json:"duration"`
	Interval     int64     `json:"interval"`
	Cooldown     int64     `json:"cooldown"`
	CopyTemplate *string   `json:"copy_template"`
	BasePrice    int64     `json:"base_price"`
	ProductID    int64     `json:"product_id,string"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	Product      *Product  `json:"product,omitempty"`
}

type PlatformProduct struct {
	ID               int64           `json:"id,string"`
	Platform         string          `json:"platform"`
	Name             string          `json:"name"`
	Variant          *string         `json:"variant"`
	ProductVariantID int64           `json:"product_variant_id,string"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
	ProductVariant   *ProductVariant `json:"product_variant,omitempty"`
}

// Payloads
type CreateProductPayload struct {
	Name string `json:"name" validate:"required"`
}

type CreateProductWithVariantPayload struct {
	Name     string                `json:"name" validate:"required"`
	Variants []CreateVariantNested `json:"variants" validate:"required,dive"`
}

type CreateVariantNested struct {
	Name         string  `json:"name" validate:"required"`
	Duration     int64   `json:"duration" validate:"required"`
	Interval     int64   `json:"interval" validate:"required"`
	Cooldown     int64   `json:"cooldown" validate:"required"`
	CopyTemplate *string `json:"copy_template"`
	BasePrice    int64   `json:"base_price"`
}

type CreateVariantPayload struct {
	Name         string  `json:"name" validate:"required"`
	Duration     int64   `json:"duration" validate:"required"`
	Interval     int64   `json:"interval" validate:"required"`
	Cooldown     int64   `json:"cooldown" validate:"required"`
	CopyTemplate *string `json:"copy_template"`
	BasePrice    int64   `json:"base_price"`
	ProductID    int64   `json:"product_id,string" validate:"required"`
}

type UpdateProductPayload struct {
	Name string `json:"name" validate:"required"`
}

type UpdateVariantPayload struct {
	Name         *string `json:"name"`
	Duration     *int64  `json:"duration"`
	Interval     *int64  `json:"interval"`
	Cooldown     *int64  `json:"cooldown"`
	CopyTemplate *string `json:"copy_template"`
	BasePrice    *int64  `json:"base_price"`
}

type CreatePlatformProductPayload struct {
	Platform         string  `json:"platform" validate:"required"`
	Name             string  `json:"name" validate:"required"`
	Variant          *string `json:"variant"`
	ProductVariantID int64   `json:"product_variant_id,string" validate:"required"`
}

type UpdatePlatformProductPayload struct {
	Platform         *string  `json:"platform"`
	Name             *string  `json:"name"`
	Variant          **string `json:"variant"`
	ProductVariantID *int64   `json:"product_variant_id,string"`
}

type ResolveItem struct {
	Name    string  `json:"name" validate:"required"`
	Variant *string `json:"variant"`
}

type ResolvePayload struct {
	Platform string        `json:"platform" validate:"required"`
	Items    []ResolveItem `json:"items" validate:"required,dive"`
}

type ByNamesPayload struct {
	Platform string   `json:"platform" validate:"required"`
	Names    []string `json:"names" validate:"required"`
}

type ProductHandler struct {
	dbPool *pgxpool.Pool
}

func NewProductHandler(dbPool *pgxpool.Pool) *ProductHandler {
	return &ProductHandler{dbPool: dbPool}
}

func (h *ProductHandler) RegisterRoutes(r chi.Router, auth *middleware.AuthMiddleware) {
	// Product
	r.Route("/product", func(r chi.Router) {
		r.Use(auth.TenantAuth)
		r.Get("/", h.FindAllProducts)
		r.Get("/{id}", h.FindOneProduct)
		r.With(middleware.ValidateBody[CreateProductPayload]()).Post("/", h.CreateProduct)
		r.With(middleware.ValidateBody[CreateProductWithVariantPayload]()).Post("/with-variant", h.CreateProductWithVariant)
		r.With(middleware.ValidateBody[UpdateProductPayload]()).Patch("/{id}", h.UpdateProduct)
		r.Delete("/{id}", h.RemoveProduct)
	})

	// Variant
	r.Route("/product-variant", func(r chi.Router) {
		r.Use(auth.TenantAuth)
		r.Get("/", h.FindAllVariants)
		r.Get("/{id}", h.FindOneVariant)
		r.With(middleware.ValidateBody[CreateVariantPayload]()).Post("/", h.CreateVariant)
		r.With(middleware.ValidateBody[UpdateVariantPayload]()).Patch("/{id}", h.UpdateVariant)
		r.Delete("/{id}", h.RemoveVariant)
	})

	// Platform Product
	r.Route("/platform-product", func(r chi.Router) {
		r.Use(auth.TenantAuth)
		r.Get("/", h.FindAllPlatformProducts)
		r.With(middleware.ValidateBody[ResolvePayload]()).Post("/resolve", h.Resolve)
		r.With(middleware.ValidateBody[ByNamesPayload]()).Post("/by-names", h.ByNames)
		r.Get("/{id}", h.FindOnePlatformProduct)
		r.With(middleware.ValidateBody[CreatePlatformProductPayload]()).Post("/", h.CreatePlatformProduct)
		r.With(middleware.ValidateBody[UpdatePlatformProductPayload]()).Patch("/{id}", h.UpdatePlatformProduct)
		r.Delete("/{id}", h.RemovePlatformProduct)
	})
}

// ==========================================
// PRODUCT CRUD
// ==========================================

func (h *ProductHandler) FindAllProducts(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	page, limit, offset := response.ParsePagination(r)

	nameFilter := r.URL.Query().Get("name")
	whereClause := ""
	var args []interface{}
	if nameFilter != "" {
		whereClause = "WHERE name ILIKE $1"
		args = append(args, "%"+nameFilter+"%")
	}

	countQuery := fmt.Sprintf(`SELECT COUNT(*) FROM "%s".product %s`, tenantID, whereClause)
	var total int64
	err := h.dbPool.QueryRow(r.Context(), countQuery, args...).Scan(&total)
	if err != nil {
		slog.Error("failed to count products", "err", err)
		response.Error(w, http.StatusInternalServerError, "database count failed", err)
		return
	}

	// Dynamic sorting construction
	orderByClause := "name ASC"
	orderBy := r.URL.Query().Get("order_by")
	orderDir := r.URL.Query().Get("order_direction")
	if orderBy == "name" {
		dir := "ASC"
		if strings.ToUpper(orderDir) == "DESC" {
			dir = "DESC"
		}
		orderByClause = fmt.Sprintf("name %s", dir)
	}

	selectQuery := fmt.Sprintf(`
		SELECT id, name, created_at, updated_at 
		FROM "%s".product 
		%s 
		ORDER BY %s 
		LIMIT $%d OFFSET $%d
	`, tenantID, whereClause, orderByClause, len(args)+1, len(args)+2)

	selectArgs := append(args, limit, offset)
	rows, err := h.dbPool.Query(r.Context(), selectQuery, selectArgs...)
	if err != nil {
		slog.Error("failed to query products", "err", err)
		response.Error(w, http.StatusInternalServerError, "database query failed", err)
		return
	}
	defer rows.Close()

	var products []Product
	for rows.Next() {
		var p Product
		if err := rows.Scan(&p.ID, &p.Name, &p.CreatedAt, &p.UpdatedAt); err == nil {
			products = append(products, p)
		}
	}

	// Fetch variants for all products in bulk (fixes N+1 query problem)
	if len(products) > 0 {
		productIDs := make([]int64, len(products))
		for i, p := range products {
			productIDs[i] = p.ID
		}

		vRows, err := h.dbPool.Query(r.Context(), fmt.Sprintf(`
			SELECT id, name, duration, interval, cooldown, copy_template, base_price, product_id, created_at, updated_at
			FROM "%s".product_variant
			WHERE product_id = ANY($1)
			ORDER BY name ASC
		`, tenantID), productIDs)
		if err == nil {
			defer vRows.Close()
			variantMap := make(map[int64][]ProductVariant)
			for vRows.Next() {
				var v ProductVariant
				if err := vRows.Scan(&v.ID, &v.Name, &v.Duration, &v.Interval, &v.Cooldown, &v.CopyTemplate, &v.BasePrice, &v.ProductID, &v.CreatedAt, &v.UpdatedAt); err == nil {
					variantMap[v.ProductID] = append(variantMap[v.ProductID], v)
				}
			}
			for i := range products {
				if vars, found := variantMap[products[i].ID]; found {
					products[i].Variants = vars
				} else {
					products[i].Variants = []ProductVariant{}
				}
			}
		}
	}

	response.JSON(w, http.StatusOK, response.NewPaginationResponse(products, total, page, limit))
}

func (h *ProductHandler) FindOneProduct(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)

	var p Product
	err := h.dbPool.QueryRow(r.Context(), fmt.Sprintf(`
		SELECT id, name, created_at, updated_at 
		FROM "%s".product WHERE id = $1
	`, tenantID), id).Scan(&p.ID, &p.Name, &p.CreatedAt, &p.UpdatedAt)

	if err != nil {
		response.Error(w, http.StatusNotFound, fmt.Sprintf("product dengan id: %d tidak ditemukan", id), err)
		return
	}

	vRows, err := h.dbPool.Query(r.Context(), fmt.Sprintf(`
		SELECT id, name, duration, interval, cooldown, copy_template, base_price, created_at, updated_at
		FROM "%s".product_variant
		WHERE product_id = $1
		ORDER BY name ASC
	`, tenantID), id)
	if err == nil {
		defer vRows.Close()
		variants := []ProductVariant{}
		for vRows.Next() {
			var v ProductVariant
			if err := vRows.Scan(&v.ID, &v.Name, &v.Duration, &v.Interval, &v.Cooldown, &v.CopyTemplate, &v.BasePrice, &v.CreatedAt, &v.UpdatedAt); err == nil {
				v.ProductID = id
				variants = append(variants, v)
			}
		}
		p.Variants = variants
	} else {
		p.Variants = []ProductVariant{}
	}

	response.JSON(w, http.StatusOK, p)
}

func (h *ProductHandler) CreateProduct(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	payload := middleware.GetBody[CreateProductPayload](r)

	// Check if already exists
	var count int
	_ = h.dbPool.QueryRow(r.Context(), fmt.Sprintf(`SELECT COUNT(*) FROM "%s".product WHERE name = $1`, tenantID), payload.Name).Scan(&count)
	if count > 0 {
		response.Error(w, http.StatusBadRequest, "Produk sudah ada")
		return
	}

	var p Product
	err := h.dbPool.QueryRow(r.Context(), fmt.Sprintf(`
		INSERT INTO "%s".product (name, created_at, updated_at)
		VALUES ($1, NOW(), NOW())
		RETURNING id, name, created_at, updated_at
	`, tenantID), payload.Name).Scan(&p.ID, &p.Name, &p.CreatedAt, &p.UpdatedAt)

	if err != nil {
		slog.Error("failed to insert product", "err", err)
		response.Error(w, http.StatusInternalServerError, "failed to insert product", err)
		return
	}

	response.JSON(w, http.StatusCreated, p)
}

func (h *ProductHandler) CreateProductWithVariant(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	payload := middleware.GetBody[CreateProductWithVariantPayload](r)

	tx, err := h.dbPool.Begin(r.Context())
	if err != nil {
		slog.Error("failed to start transaction to create product with variants", "err", err)
		response.Error(w, http.StatusInternalServerError, "failed to start transaction", err)
		return
	}
	defer tx.Rollback(r.Context())

	// Check if already exists
	var count int
	_ = tx.QueryRow(r.Context(), fmt.Sprintf(`SELECT COUNT(*) FROM "%s".product WHERE name = $1`, tenantID), payload.Name).Scan(&count)
	if count > 0 {
		response.Error(w, http.StatusBadRequest, "Produk sudah ada")
		return
	}

	// Insert Product
	var p Product
	err = tx.QueryRow(r.Context(), fmt.Sprintf(`
		INSERT INTO "%s".product (name, created_at, updated_at)
		VALUES ($1, NOW(), NOW())
		RETURNING id, name, created_at, updated_at
	`, tenantID), payload.Name).Scan(&p.ID, &p.Name, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		slog.Error("failed to insert product within transaction", "err", err)
		response.Error(w, http.StatusInternalServerError, "failed to insert product", err)
		return
	}

	// Insert Variants
	var variants []ProductVariant
	for _, v := range payload.Variants {
		var pv ProductVariant
		err = tx.QueryRow(r.Context(), fmt.Sprintf(`
			INSERT INTO "%s".product_variant (name, duration, interval, cooldown, copy_template, base_price, product_id, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, NOW(), NOW())
			RETURNING id, name, duration, interval, cooldown, copy_template, base_price, product_id, created_at, updated_at
		`, tenantID), v.Name, v.Duration, v.Interval, v.Cooldown, v.CopyTemplate, v.BasePrice, p.ID).Scan(
			&pv.ID, &pv.Name, &pv.Duration, &pv.Interval, &pv.Cooldown, &pv.CopyTemplate, &pv.BasePrice, &pv.ProductID, &pv.CreatedAt, &pv.UpdatedAt,
		)
		if err != nil {
			slog.Error("failed to insert product variant within transaction", "err", err)
			response.Error(w, http.StatusInternalServerError, "failed to insert variants", err)
			return
		}
		variants = append(variants, pv)
	}

	if err := tx.Commit(r.Context()); err != nil {
		slog.Error("failed to commit transaction to create product with variants", "err", err)
		response.Error(w, http.StatusInternalServerError, "failed to commit transaction", err)
		return
	}

	p.Variants = variants
	response.JSON(w, http.StatusCreated, p)
}

func (h *ProductHandler) UpdateProduct(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	payload := middleware.GetBody[UpdateProductPayload](r)

	var p Product
	err := h.dbPool.QueryRow(r.Context(), fmt.Sprintf(`
		UPDATE "%s".product 
		SET name = $1, updated_at = NOW() 
		WHERE id = $2 
		RETURNING id, name, created_at, updated_at
	`, tenantID), payload.Name, id).Scan(&p.ID, &p.Name, &p.CreatedAt, &p.UpdatedAt)

	if err != nil {
		response.Error(w, http.StatusNotFound, fmt.Sprintf("product dengan id: %d tidak ditemukan", id), err)
		return
	}

	response.JSON(w, http.StatusOK, p)
}

func (h *ProductHandler) RemoveProduct(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)

	res, err := h.dbPool.Exec(r.Context(), fmt.Sprintf(`DELETE FROM "%s".product WHERE id = $1`, tenantID), id)
	if err != nil {
		slog.Error("failed to delete product", "err", err)
		response.Error(w, http.StatusInternalServerError, "failed to delete product", err)
		return
	}

	if res.RowsAffected() == 0 {
		response.Error(w, http.StatusNotFound, fmt.Sprintf("product dengan id: %d tidak ditemukan", id))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ==========================================
// PRODUCT VARIANT CRUD
// ==========================================

func (h *ProductHandler) FindAllVariants(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	page, limit, offset := response.ParsePagination(r)

	q := r.URL.Query()
	productFilter := q.Get("product_id")

	whereClause := ""
	var args []interface{}
	if productFilter != "" {
		pID, _ := strconv.ParseInt(productFilter, 10, 64)
		whereClause = "WHERE pv.product_id = $1"
		args = append(args, pID)
	}

	countQuery := fmt.Sprintf(`SELECT COUNT(*) FROM "%s".product_variant pv %s`, tenantID, whereClause)
	var total int64
	err := h.dbPool.QueryRow(r.Context(), countQuery, args...).Scan(&total)
	if err != nil {
		slog.Error("failed to count product variants", "err", err)
		response.Error(w, http.StatusInternalServerError, "database count failed", err)
		return
	}

	// Dynamic sorting construction
	orderByClause := "pv.name ASC"
	orderBy := q.Get("order_by")
	orderDir := q.Get("order_direction")
	if orderBy == "name" {
		dir := "ASC"
		if strings.ToUpper(orderDir) == "DESC" {
			dir = "DESC"
		}
		orderByClause = fmt.Sprintf("pv.name %s", dir)
	}

	selectQuery := fmt.Sprintf(`
		SELECT pv.id, pv.name, pv.duration, pv.interval, pv.cooldown, pv.copy_template, pv.base_price, pv.product_id, pv.created_at, pv.updated_at,
		       p.name AS product_name
		FROM "%s".product_variant pv
		LEFT JOIN "%s".product p ON pv.product_id = p.id
		%s
		ORDER BY %s
		LIMIT $%d OFFSET $%d
	`, tenantID, tenantID, whereClause, orderByClause, len(args)+1, len(args)+2)

	selectArgs := append(args, limit, offset)
	rows, err := h.dbPool.Query(r.Context(), selectQuery, selectArgs...)
	if err != nil {
		slog.Error("failed to query product variants", "err", err)
		response.Error(w, http.StatusInternalServerError, "database query failed", err)
		return
	}
	defer rows.Close()

	var variants []ProductVariant
	for rows.Next() {
		var v ProductVariant
		var productName *string
		err = rows.Scan(&v.ID, &v.Name, &v.Duration, &v.Interval, &v.Cooldown, &v.CopyTemplate, &v.BasePrice, &v.ProductID, &v.CreatedAt, &v.UpdatedAt, &productName)
		if err != nil {
			slog.Error("failed to scan product variant row", "err", err)
			response.Error(w, http.StatusInternalServerError, "database scan failed", err)
			return
		}
		if productName != nil {
			v.Product = &Product{
				ID:   v.ProductID,
				Name: *productName,
			}
		}
		variants = append(variants, v)
	}

	response.JSON(w, http.StatusOK, response.NewPaginationResponse(variants, total, page, limit))
}

func (h *ProductHandler) FindOneVariant(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)

	var v ProductVariant
	var productName *string
	err := h.dbPool.QueryRow(r.Context(), fmt.Sprintf(`
		SELECT pv.id, pv.name, pv.duration, pv.interval, pv.cooldown, pv.copy_template, pv.base_price, pv.product_id, pv.created_at, pv.updated_at,
		       p.name AS product_name
		FROM "%s".product_variant pv
		LEFT JOIN "%s".product p ON pv.product_id = p.id
		WHERE pv.id = $1
	`, tenantID, tenantID), id).Scan(&v.ID, &v.Name, &v.Duration, &v.Interval, &v.Cooldown, &v.CopyTemplate, &v.BasePrice, &v.ProductID, &v.CreatedAt, &v.UpdatedAt, &productName)

	if err != nil {
		response.Error(w, http.StatusNotFound, fmt.Sprintf("productVariant dengan id: %d tidak ditemukan", id), err)
		return
	}

	if productName != nil {
		v.Product = &Product{
			ID:   v.ProductID,
			Name: *productName,
		}
	}

	response.JSON(w, http.StatusOK, v)
}

func (h *ProductHandler) CreateVariant(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	payload := middleware.GetBody[CreateVariantPayload](r)

	// Validate product exists
	var count int
	_ = h.dbPool.QueryRow(r.Context(), fmt.Sprintf(`SELECT COUNT(*) FROM "%s".product WHERE id = $1`, tenantID), payload.ProductID).Scan(&count)
	if count == 0 {
		response.Error(w, http.StatusBadRequest, "associated product not found")
		return
	}

	var v ProductVariant
	err := h.dbPool.QueryRow(r.Context(), fmt.Sprintf(`
		INSERT INTO "%s".product_variant (name, duration, interval, cooldown, copy_template, base_price, product_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW(), NOW())
		RETURNING id, name, duration, interval, cooldown, copy_template, base_price, product_id, created_at, updated_at
	`, tenantID), payload.Name, payload.Duration, payload.Interval, payload.Cooldown, payload.CopyTemplate, payload.BasePrice, payload.ProductID).Scan(
		&v.ID, &v.Name, &v.Duration, &v.Interval, &v.Cooldown, &v.CopyTemplate, &v.BasePrice, &v.ProductID, &v.CreatedAt, &v.UpdatedAt,
	)

	if err != nil {
		slog.Error("failed to insert product variant", "err", err)
		response.Error(w, http.StatusInternalServerError, "failed to insert variant", err)
		return
	}

	response.JSON(w, http.StatusCreated, v)
}

func (h *ProductHandler) UpdateVariant(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	payload := middleware.GetBody[UpdateVariantPayload](r)

	tx, err := h.dbPool.Begin(r.Context())
	if err != nil {
		slog.Error("failed to start transaction to update variant", "err", err)
		response.Error(w, http.StatusInternalServerError, "failed to start transaction", err)
		return
	}
	defer tx.Rollback(r.Context())

	// Build update query
	query := fmt.Sprintf(`UPDATE "%s".product_variant SET `, tenantID)
	var args []interface{}
	argIdx := 1

	if payload.Name != nil {
		query += fmt.Sprintf("name = $%d, ", argIdx)
		args = append(args, *payload.Name)
		argIdx++
	}
	if payload.Duration != nil {
		query += fmt.Sprintf("duration = $%d, ", argIdx)
		args = append(args, *payload.Duration)
		argIdx++
	}
	if payload.Interval != nil {
		query += fmt.Sprintf("interval = $%d, ", argIdx)
		args = append(args, *payload.Interval)
		argIdx++
	}
	if payload.Cooldown != nil {
		query += fmt.Sprintf("cooldown = $%d, ", argIdx)
		args = append(args, *payload.Cooldown)
		argIdx++
	}
	if payload.CopyTemplate != nil {
		query += fmt.Sprintf("copy_template = $%d, ", argIdx)
		args = append(args, *payload.CopyTemplate)
		argIdx++
	}
	if payload.BasePrice != nil {
		query += fmt.Sprintf("base_price = $%d, ", argIdx)
		args = append(args, *payload.BasePrice)
		argIdx++
	}

	query += "updated_at = NOW() "
	query += fmt.Sprintf("WHERE id = $%d", argIdx)
	args = append(args, id)

	res, err := tx.Exec(r.Context(), query, args...)
	if err != nil {
		slog.Error("failed to update variant", "err", err)
		response.Error(w, http.StatusInternalServerError, "failed to update variant", err)
		return
	}

	if res.RowsAffected() == 0 {
		response.Error(w, http.StatusNotFound, fmt.Sprintf("productVariant dengan id: %d tidak ditemukan", id))
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		slog.Error("failed to commit transaction to update variant", "err", err)
		response.Error(w, http.StatusInternalServerError, "failed to commit transaction", err)
		return
	}

	var v ProductVariant
	_ = h.dbPool.QueryRow(r.Context(), fmt.Sprintf(`
		SELECT id, name, duration, interval, cooldown, copy_template, base_price, product_id, created_at, updated_at
		FROM "%s".product_variant WHERE id = $1
	`, tenantID), id).Scan(&v.ID, &v.Name, &v.Duration, &v.Interval, &v.Cooldown, &v.CopyTemplate, &v.BasePrice, &v.ProductID, &v.CreatedAt, &v.UpdatedAt)

	response.JSON(w, http.StatusOK, v)
}

func (h *ProductHandler) RemoveVariant(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)

	res, err := h.dbPool.Exec(r.Context(), fmt.Sprintf(`DELETE FROM "%s".product_variant WHERE id = $1`, tenantID), id)
	if err != nil {
		slog.Error("failed to delete product variant", "err", err)
		response.Error(w, http.StatusInternalServerError, "failed to delete variant", err)
		return
	}

	if res.RowsAffected() == 0 {
		response.Error(w, http.StatusNotFound, fmt.Sprintf("productVariant dengan id: %d tidak ditemukan", id))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ==========================================
// PLATFORM PRODUCT CRUD
// ==========================================

func (h *ProductHandler) FindAllPlatformProducts(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	page, limit, offset := response.ParsePagination(r)

	q := r.URL.Query()
	nameFilter := q.Get("name")
	platformFilter := q.Get("platform")
	variantFilter := q.Get("variant")
	pvFilter := q.Get("product_variant_id")

	var conditions []string
	var args []interface{}
	argIdx := 1

	if nameFilter != "" {
		conditions = append(conditions, fmt.Sprintf("pp.name ILIKE $%d", argIdx))
		args = append(args, "%"+nameFilter+"%")
		argIdx++
	}
	if platformFilter != "" {
		conditions = append(conditions, fmt.Sprintf("pp.platform ILIKE $%d", argIdx))
		args = append(args, "%"+platformFilter+"%")
		argIdx++
	}
	if variantFilter != "" {
		conditions = append(conditions, fmt.Sprintf("pp.variant ILIKE $%d", argIdx))
		args = append(args, "%"+variantFilter+"%")
		argIdx++
	}
	if pvFilter != "" {
		pvID, _ := strconv.ParseInt(pvFilter, 10, 64)
		conditions = append(conditions, fmt.Sprintf("pp.product_variant_id = $%d", argIdx))
		args = append(args, pvID)
		argIdx++
	}

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = "WHERE " + strings.Join(conditions, " AND ")
	}

	countQuery := fmt.Sprintf(`SELECT COUNT(*) FROM "%s".platform_product pp %s`, tenantID, whereClause)
	var total int64
	err := h.dbPool.QueryRow(r.Context(), countQuery, args...).Scan(&total)
	if err != nil {
		slog.Error("failed to count platform products", "err", err)
		response.Error(w, http.StatusInternalServerError, "database count failed", err)
		return
	}

	// Dynamic sorting construction
	orderByClause := "pp.name ASC"
	orderBy := q.Get("order_by")
	orderDir := q.Get("order_direction")
	if orderBy == "name" {
		dir := "ASC"
		if strings.ToUpper(orderDir) == "DESC" {
			dir = "DESC"
		}
		orderByClause = fmt.Sprintf("pp.name %s", dir)
	}

	selectQuery := fmt.Sprintf(`
		SELECT pp.id, pp.platform, pp.name, pp.variant, pp.product_variant_id, pp.created_at, pp.updated_at,
		       pv.id, pv.name, pv.duration, pv.interval, pv.cooldown, pv.copy_template, pv.base_price, pv.product_id, pv.created_at, pv.updated_at,
		       p.id, p.name
		FROM "%s".platform_product pp
		LEFT JOIN "%s".product_variant pv ON pp.product_variant_id = pv.id
		LEFT JOIN "%s".product p ON pv.product_id = p.id
		%s
		ORDER BY %s
		LIMIT $%d OFFSET $%d
	`, tenantID, tenantID, tenantID, whereClause, orderByClause, argIdx, argIdx+1)

	selectArgs := append(args, limit, offset)
	rows, err := h.dbPool.Query(r.Context(), selectQuery, selectArgs...)
	if err != nil {
		slog.Error("failed to query platform products", "err", err)
		response.Error(w, http.StatusInternalServerError, "database query failed", err)
		return
	}
	defer rows.Close()

	var pps []PlatformProduct
	for rows.Next() {
		var pp PlatformProduct
		var pv ProductVariant
		var p Product
		err = rows.Scan(
			&pp.ID, &pp.Platform, &pp.Name, &pp.Variant, &pp.ProductVariantID, &pp.CreatedAt, &pp.UpdatedAt,
			&pv.ID, &pv.Name, &pv.Duration, &pv.Interval, &pv.Cooldown, &pv.CopyTemplate, &pv.BasePrice, &pv.ProductID, &pv.CreatedAt, &pv.UpdatedAt,
			&p.ID, &p.Name,
		)
		if err != nil {
			slog.Error("failed to scan platform product row", "err", err)
			response.Error(w, http.StatusInternalServerError, "database scan failed", err)
			return
		}
		pv.Product = &p
		pp.ProductVariant = &pv
		pps = append(pps, pp)
	}

	response.JSON(w, http.StatusOK, response.NewPaginationResponse(pps, total, page, limit))
}

func (h *ProductHandler) FindOnePlatformProduct(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)

	var pp PlatformProduct
	var pv ProductVariant
	var p Product
	err := h.dbPool.QueryRow(r.Context(), fmt.Sprintf(`
		SELECT pp.id, pp.platform, pp.name, pp.variant, pp.product_variant_id, pp.created_at, pp.updated_at,
		       pv.id, pv.name, pv.duration, pv.interval, pv.cooldown, pv.copy_template, pv.base_price, pv.product_id, pv.created_at, pv.updated_at,
		       p.id, p.name
		FROM "%s".platform_product pp
		LEFT JOIN "%s".product_variant pv ON pp.product_variant_id = pv.id
		LEFT JOIN "%s".product p ON pv.product_id = p.id
		WHERE pp.id = $1
	`, tenantID, tenantID, tenantID), id).Scan(
		&pp.ID, &pp.Platform, &pp.Name, &pp.Variant, &pp.ProductVariantID, &pp.CreatedAt, &pp.UpdatedAt,
		&pv.ID, &pv.Name, &pv.Duration, &pv.Interval, &pv.Cooldown, &pv.CopyTemplate, &pv.BasePrice, &pv.ProductID, &pv.CreatedAt, &pv.UpdatedAt,
		&p.ID, &p.Name,
	)

	if err != nil {
		response.Error(w, http.StatusNotFound, fmt.Sprintf("platformProduct dengan id: %d tidak ditemukan", id), err)
		return
	}

	pv.Product = &p
	pp.ProductVariant = &pv

	response.JSON(w, http.StatusOK, pp)
}

func (h *ProductHandler) CreatePlatformProduct(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	payload := middleware.GetBody[CreatePlatformProductPayload](r)

	// Validate product variant exists
	var count int
	_ = h.dbPool.QueryRow(r.Context(), fmt.Sprintf(`SELECT COUNT(*) FROM "%s".product_variant WHERE id = $1`, tenantID), payload.ProductVariantID).Scan(&count)
	if count == 0 {
		response.Error(w, http.StatusBadRequest, "associated product variant not found")
		return
	}

	// Normalize variant
	var normalizedVariant *string
	if payload.Variant != nil {
		trimmed := strings.TrimSpace(*payload.Variant)
		if trimmed != "" {
			normalizedVariant = &trimmed
		}
	}

	// Check if already exists
	var duplicate int
	if normalizedVariant != nil {
		_ = h.dbPool.QueryRow(r.Context(), fmt.Sprintf(`
			SELECT COUNT(*) FROM "%s".platform_product 
			WHERE name = $1 AND platform = $2 AND variant = $3 AND product_variant_id = $4
		`, tenantID), payload.Name, payload.Platform, *normalizedVariant, payload.ProductVariantID).Scan(&duplicate)
	} else {
		_ = h.dbPool.QueryRow(r.Context(), fmt.Sprintf(`
			SELECT COUNT(*) FROM "%s".platform_product 
			WHERE name = $1 AND platform = $2 AND variant IS NULL AND product_variant_id = $3
		`, tenantID), payload.Name, payload.Platform, payload.ProductVariantID).Scan(&duplicate)
	}

	if duplicate > 0 {
		response.Error(w, http.StatusBadRequest, "Produk Platform sudah ada")
		return
	}

	var newID int64
	var createdAt, updatedAt time.Time
	err := h.dbPool.QueryRow(r.Context(), fmt.Sprintf(`
		INSERT INTO "%s".platform_product (platform, name, variant, product_variant_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, NOW(), NOW())
		RETURNING id, created_at, updated_at
	`, tenantID), payload.Platform, payload.Name, payload.Variant, payload.ProductVariantID).Scan(&newID, &createdAt, &updatedAt)
	if err != nil {
		slog.Error("failed to insert platform product", "err", err)
		response.Error(w, http.StatusInternalServerError, "failed to insert platform product", err)
		return
	}

	var pp PlatformProduct
	var pv ProductVariant
	var p Product
	err = h.dbPool.QueryRow(r.Context(), fmt.Sprintf(`
		SELECT pp.id, pp.platform, pp.name, pp.variant, pp.product_variant_id, pp.created_at, pp.updated_at,
		       pv.id, pv.name, pv.duration, pv.interval, pv.cooldown, pv.copy_template, pv.base_price, pv.product_id, pv.created_at, pv.updated_at,
		       p.id, p.name
		FROM "%s".platform_product pp
		LEFT JOIN "%s".product_variant pv ON pp.product_variant_id = pv.id
		LEFT JOIN "%s".product p ON pv.product_id = p.id
		WHERE pp.id = $1
	`, tenantID, tenantID, tenantID), newID).Scan(
		&pp.ID, &pp.Platform, &pp.Name, &pp.Variant, &pp.ProductVariantID, &pp.CreatedAt, &pp.UpdatedAt,
		&pv.ID, &pv.Name, &pv.Duration, &pv.Interval, &pv.Cooldown, &pv.CopyTemplate, &pv.BasePrice, &pv.ProductID, &pv.CreatedAt, &pv.UpdatedAt,
		&p.ID, &p.Name,
	)

	if err != nil {
		slog.Error("failed to fetch created platform product", "err", err)
		response.Error(w, http.StatusInternalServerError, "failed to fetch platform product", err)
		return
	}

	pv.Product = &p
	pp.ProductVariant = &pv

	response.JSON(w, http.StatusCreated, pp)
}

func (h *ProductHandler) UpdatePlatformProduct(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	payload := middleware.GetBody[UpdatePlatformProductPayload](r)

	// Fetch current details
	var currentName, currentPlatform string
	var currentVariant *string
	var currentPV int64
	err := h.dbPool.QueryRow(r.Context(), fmt.Sprintf(`
		SELECT name, platform, variant, product_variant_id FROM "%s".platform_product WHERE id = $1
	`, tenantID), id).Scan(&currentName, &currentPlatform, &currentVariant, &currentPV)
	if err != nil {
		response.Error(w, http.StatusNotFound, fmt.Sprintf("platformProduct dengan id: %d tidak ditemukan", id), err)
		return
	}

	tx, err := h.dbPool.Begin(r.Context())
	if err != nil {
		slog.Error("failed to start transaction to update platform product", "err", err)
		response.Error(w, http.StatusInternalServerError, "failed to start transaction", err)
		return
	}
	defer tx.Rollback(r.Context())

	// Build update query
	query := fmt.Sprintf(`UPDATE "%s".platform_product SET `, tenantID)
	var args []interface{}
	argIdx := 1

	if payload.Name != nil {
		query += fmt.Sprintf("name = $%d, ", argIdx)
		args = append(args, *payload.Name)
		argIdx++
		currentName = *payload.Name
	}
	if payload.Platform != nil {
		query += fmt.Sprintf("platform = $%d, ", argIdx)
		args = append(args, *payload.Platform)
		argIdx++
		currentPlatform = *payload.Platform
	}
	if payload.ProductVariantID != nil {
		query += fmt.Sprintf("product_variant_id = $%d, ", argIdx)
		args = append(args, *payload.ProductVariantID)
		argIdx++
		currentPV = *payload.ProductVariantID
	}
	if payload.Variant != nil {
		if *payload.Variant == nil {
			query += "variant = NULL, "
			currentVariant = nil
		} else {
			trimmed := strings.TrimSpace(**payload.Variant)
			if trimmed == "" {
				query += "variant = NULL, "
				currentVariant = nil
			} else {
				query += fmt.Sprintf("variant = $%d, ", argIdx)
				args = append(args, trimmed)
				argIdx++
				currentVariant = &trimmed
			}
		}
	}

	// Check duplicates
	var duplicate int
	duplicateQuery := ""
	var dupArgs []interface{}
	if currentVariant != nil {
		duplicateQuery = fmt.Sprintf(`
			SELECT COUNT(*) FROM "%s".platform_product 
			WHERE id != $1 AND name = $2 AND platform = $3 AND variant = $4 AND product_variant_id = $5
		`, tenantID)
		dupArgs = []interface{}{id, currentName, currentPlatform, *currentVariant, currentPV}
	} else {
		duplicateQuery = fmt.Sprintf(`
			SELECT COUNT(*) FROM "%s".platform_product 
			WHERE id != $1 AND name = $2 AND platform = $3 AND variant IS NULL AND product_variant_id = $4
		`, tenantID)
		dupArgs = []interface{}{id, currentName, currentPlatform, currentPV}
	}

	_ = tx.QueryRow(r.Context(), duplicateQuery, dupArgs...).Scan(&duplicate)
	if duplicate > 0 {
		response.Error(w, http.StatusBadRequest, "Produk Platform sudah ada")
		return
	}

	query += "updated_at = NOW() "
	query += fmt.Sprintf("WHERE id = $%d", argIdx)
	args = append(args, id)

	_, err = tx.Exec(r.Context(), query, args...)
	if err != nil {
		slog.Error("failed to update platform product", "err", err)
		response.Error(w, http.StatusInternalServerError, "failed to update platform product", err)
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		slog.Error("failed to commit transaction to update platform product", "err", err)
		response.Error(w, http.StatusInternalServerError, "failed to commit transaction", err)
		return
	}

	var pp PlatformProduct
	var pv ProductVariant
	var p Product
	err = h.dbPool.QueryRow(r.Context(), fmt.Sprintf(`
		SELECT pp.id, pp.platform, pp.name, pp.variant, pp.product_variant_id, pp.created_at, pp.updated_at,
		       pv.id, pv.name, pv.duration, pv.interval, pv.cooldown, pv.copy_template, pv.base_price, pv.product_id, pv.created_at, pv.updated_at,
		       p.id, p.name
		FROM "%s".platform_product pp
		LEFT JOIN "%s".product_variant pv ON pp.product_variant_id = pv.id
		LEFT JOIN "%s".product p ON pv.product_id = p.id
		WHERE pp.id = $1
	`, tenantID, tenantID, tenantID), id).Scan(
		&pp.ID, &pp.Platform, &pp.Name, &pp.Variant, &pp.ProductVariantID, &pp.CreatedAt, &pp.UpdatedAt,
		&pv.ID, &pv.Name, &pv.Duration, &pv.Interval, &pv.Cooldown, &pv.CopyTemplate, &pv.BasePrice, &pv.ProductID, &pv.CreatedAt, &pv.UpdatedAt,
		&p.ID, &p.Name,
	)
	if err == nil {
		pv.Product = &p
		pp.ProductVariant = &pv
	}

	response.JSON(w, http.StatusOK, pp)
}

func (h *ProductHandler) RemovePlatformProduct(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)

	res, err := h.dbPool.Exec(r.Context(), fmt.Sprintf(`DELETE FROM "%s".platform_product WHERE id = $1`, tenantID), id)
	if err != nil {
		slog.Error("failed to delete platform product", "err", err)
		response.Error(w, http.StatusInternalServerError, "failed to delete platform product", err)
		return
	}

	if res.RowsAffected() == 0 {
		response.Error(w, http.StatusNotFound, fmt.Sprintf("platformProduct dengan id: %d tidak ditemukan", id))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *ProductHandler) Resolve(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	payload := middleware.GetBody[ResolvePayload](r)

	type ResolvedItemResponse struct {
		ID               *int64  `json:"id,string,omitempty"`
		Name             string  `json:"name"`
		Variant          *string `json:"variant,omitempty"`
		ProductVariantID *int64  `json:"product_variant_id,string,omitempty"`
		IsFound          bool    `json:"isFound"`
	}

	var result []ResolvedItemResponse

	for _, item := range payload.Items {
		var normVariant *string
		if item.Variant != nil {
			trimmed := strings.TrimSpace(*item.Variant)
			if trimmed != "" {
				normVariant = &trimmed
			}
		}

		var pp PlatformProduct
		var err error

		if normVariant != nil {
			err = h.dbPool.QueryRow(r.Context(), fmt.Sprintf(`
				SELECT id, product_variant_id FROM "%s".platform_product
				WHERE platform = $1 AND LOWER(name) = LOWER($2) AND LOWER(variant) = LOWER($3)
			`, tenantID), payload.Platform, item.Name, *normVariant).Scan(&pp.ID, &pp.ProductVariantID)
		} else {
			err = h.dbPool.QueryRow(r.Context(), fmt.Sprintf(`
				SELECT id, product_variant_id FROM "%s".platform_product
				WHERE platform = $1 AND LOWER(name) = LOWER($2) AND variant IS NULL
			`, tenantID), payload.Platform, item.Name).Scan(&pp.ID, &pp.ProductVariantID)
		}

		if err != nil {
			result = append(result, ResolvedItemResponse{
				Name:    item.Name,
				Variant: normVariant,
				IsFound: false,
			})
		} else {
			result = append(result, ResolvedItemResponse{
				ID:               &pp.ID,
				Name:             item.Name,
				Variant:          normVariant,
				ProductVariantID: &pp.ProductVariantID,
				IsFound:          true,
			})
		}
	}

	response.JSON(w, http.StatusOK, result)
}

func (h *ProductHandler) ByNames(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID
	payload := middleware.GetBody[ByNamesPayload](r)

	type ByNameResponseItem struct {
		ID               *int64 `json:"id,string,omitempty"`
		Name             string `json:"name"`
		ProductVariantID *int64 `json:"product_variant_id,string,omitempty"`
		IsFound          bool   `json:"isFound"`
	}

	var result []ByNameResponseItem

	if len(payload.Names) == 0 {
		response.JSON(w, http.StatusOK, result)
		return
	}

	// Fetch all matching products in one query
	query := fmt.Sprintf(`
		SELECT id, name, product_variant_id 
		FROM "%s".platform_product
		WHERE platform = $1 AND name = ANY($2)
	`, tenantID)

	rows, err := h.dbPool.Query(r.Context(), query, payload.Platform, payload.Names)
	if err != nil {
		slog.Error("failed to query platform products by names", "err", err)
		response.Error(w, http.StatusInternalServerError, "database query failed", err)
		return
	}
	defer rows.Close()

	// Map matches
	matchMap := make(map[string]PlatformProduct)
	for rows.Next() {
		var pp PlatformProduct
		if err := rows.Scan(&pp.ID, &pp.Name, &pp.ProductVariantID); err == nil {
			matchMap[pp.Name] = pp
		}
	}

	for _, name := range payload.Names {
		if pp, found := matchMap[name]; found {
			result = append(result, ByNameResponseItem{
				ID:               &pp.ID,
				Name:             pp.Name,
				ProductVariantID: &pp.ProductVariantID,
				IsFound:          true,
			})
		} else {
			result = append(result, ByNameResponseItem{
				Name:    name,
				IsFound: false,
			})
		}
	}

	response.JSON(w, http.StatusOK, result)
}
