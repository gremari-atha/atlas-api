package statistic

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"atlas-api/internal/middleware"
	"atlas-api/internal/response"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Response models
type PeriodStats struct {
	NetIncome        int64     `json:"net_income"`
	Expense          int64     `json:"expense"`
	TransactionCount int64     `json:"transaction_count"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type DailyStats struct {
	Date             string    `json:"date"`
	NetIncome        int64     `json:"net_income"`
	Expense          int64     `json:"expense"`
	TransactionCount int64     `json:"transaction_count"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type RevenueStats struct {
	Period PeriodStats  `json:"period"`
	Daily  []DailyStats `json:"daily"`
}

type ProductStats struct {
	ProductVariantID int64              `json:"product_variant_id,string"`
	ItemsSold        int64              `json:"items_sold"`
	ProductVariant   ProductVariantInfo `json:"product_variant"`
}

type ProductVariantInfo struct {
	ID      int64       `json:"id,string"`
	Name    string      `json:"name"`
	Product ProductInfo `json:"product"`
}

type ProductInfo struct {
	ID   int64  `json:"id,string"`
	Name string `json:"name"`
}

type PlatformStats struct {
	Platform         string `json:"platform"`
	TransactionCount int64  `json:"transaction_count"`
}

type PeakHourStats struct {
	Hour             int   `json:"hour"`
	TransactionCount int64 `json:"transaction_count"`
}

type TodayStats struct {
	NetIncome        int64 `json:"net_income"`
	Expense          int64 `json:"expense"`
	TransactionCount int64 `json:"transaction_count"`
}

type AllStatsResponse struct {
	Revenue  RevenueStats    `json:"revenue"`
	Product  []ProductStats  `json:"product"`
	Platform []PlatformStats `json:"platform"`
	PeakHour []PeakHourStats `json:"peakHour"`
	Today    TodayStats      `json:"today"`
}

type StatisticHandler struct {
	dbPool *pgxpool.Pool
}

func NewStatisticHandler(dbPool *pgxpool.Pool) *StatisticHandler {
	return &StatisticHandler{dbPool: dbPool}
}

func (h *StatisticHandler) RegisterRoutes(r chi.Router, auth *middleware.AuthMiddleware) {
	r.Route("/statistic", func(r chi.Router) {
		r.Use(auth.TenantAuth)
		r.Get("/", h.GetAllStatistic)
	})
}

func (h *StatisticHandler) getRangeDetails(rangeParam string) (startDate time.Time, isMonthly bool) {
	now := time.Now()
	startDate = now
	isMonthly = false

	switch rangeParam {
	case "week":
		// Get Monday of current week
		day := int(now.Weekday())
		if day == 0 {
			day = 7 // standard Monday start
		}
		diff := now.AddDate(0, 0, -day+1)
		startDate = time.Date(diff.Year(), diff.Month(), diff.Day(), 0, 0, 0, 0, now.Location())
	case "3months":
		diff := now.AddDate(0, -3, 0)
		startDate = time.Date(diff.Year(), diff.Month(), diff.Day(), 0, 0, 0, 0, now.Location())
		isMonthly = true
	case "1year":
		diff := now.AddDate(-1, 0, 0)
		startDate = time.Date(diff.Year(), diff.Month(), diff.Day(), 0, 0, 0, 0, now.Location())
		isMonthly = true
	case "month":
		fallthrough
	default:
		startDate = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	}
	return startDate, isMonthly
}

func (h *StatisticHandler) GetAllStatistic(w http.ResponseWriter, r *http.Request) {
	uCtx, _ := middleware.GetUserContext(r.Context())
	tenantID := uCtx.TenantID

	rangeParam := r.URL.Query().Get("range")
	if rangeParam == "" {
		rangeParam = "month"
	}

	startDate, isMonthly := h.getRangeDetails(rangeParam)

	var responsePayload AllStatsResponse

	// 1. Period Totals Query
	var periodTotalSql string
	if isMonthly {
		periodTotalSql = fmt.Sprintf(`
			WITH rev AS (
				SELECT COALESCE(SUM(revenue), 0) AS total_revenue, COALESCE(SUM(total_transaction), 0) AS total_transaction
				FROM "%s".monthly_platform_stats
				WHERE bucket_month >= $1
			),
			exp AS (
				SELECT COALESCE(SUM(total_expense_amount), 0) AS total_expense
				FROM "%s".monthly_expense_stats
				WHERE bucket_month >= $1
			)
			SELECT 
				(total_revenue - total_expense)::BIGINT AS net_income,
				total_expense::BIGINT AS expense,
				total_transaction::BIGINT AS transaction_count
			FROM rev, exp;
		`, tenantID, tenantID)
	} else {
		periodTotalSql = fmt.Sprintf(`
			WITH rev AS (
				SELECT COALESCE(SUM(revenue), 0) AS total_revenue, COALESCE(SUM(total_transaction), 0) AS total_transaction
				FROM "%s".daily_platform_stats
				WHERE bucket >= $1
			),
			exp AS (
				SELECT COALESCE(SUM(total_expense_amount), 0) AS total_expense
				FROM "%s".daily_expense_stats
				WHERE bucket >= $1
			)
			SELECT 
				(total_revenue - total_expense)::BIGINT AS net_income,
				total_expense::BIGINT AS expense,
				total_transaction::BIGINT AS transaction_count
			FROM rev, exp;
		`, tenantID, tenantID)
	}

	var p PeriodStats
	err := h.dbPool.QueryRow(r.Context(), periodTotalSql, startDate).Scan(&p.NetIncome, &p.Expense, &p.TransactionCount)
	if err != nil {
		slog.Error("failed to query period stats", "err", err)
		response.Error(w, http.StatusInternalServerError, "database aggregation failed", err)
		return
	}
	p.CreatedAt = time.Now()
	p.UpdatedAt = time.Now()
	responsePayload.ResultPeriod(p)

	// 2. Query Time-Series Breakdown for Chart
	var breakdownSql string
	if isMonthly {
		breakdownSql = fmt.Sprintf(`
			WITH rev AS (
				SELECT 
					bucket_month,
					SUM(revenue) AS total_revenue,
					SUM(total_transaction) AS total_transaction
				FROM "%s".monthly_platform_stats
				WHERE bucket_month >= $1
				GROUP BY bucket_month
			),
			exp AS (
				SELECT 
					bucket_month,
					SUM(total_expense_amount) AS total_expense
				FROM "%s".monthly_expense_stats
				WHERE bucket_month >= $1
				GROUP BY bucket_month
			)
			SELECT 
				to_char(COALESCE(rev.bucket_month, exp.bucket_month), 'YYYY-MM') AS date,
				(COALESCE(rev.total_revenue, 0) - COALESCE(exp.total_expense, 0))::BIGINT AS net_income,
				COALESCE(exp.total_expense, 0)::BIGINT AS expense,
				COALESCE(rev.total_transaction, 0)::BIGINT AS transaction_count,
				COALESCE(rev.bucket_month, exp.bucket_month) AS created_at
			FROM rev
			FULL OUTER JOIN exp ON rev.bucket_month = exp.bucket_month
			ORDER BY COALESCE(rev.bucket_month, exp.bucket_month) ASC;
		`, tenantID, tenantID)
	} else {
		breakdownSql = fmt.Sprintf(`
			WITH rev AS (
				SELECT 
					bucket,
					SUM(revenue) AS total_revenue,
					SUM(total_transaction) AS total_transaction
				FROM "%s".daily_platform_stats
				WHERE bucket >= $1
				GROUP BY bucket
			),
			exp AS (
				SELECT 
					bucket,
					SUM(total_expense_amount) AS total_expense
				FROM "%s".daily_expense_stats
				WHERE bucket >= $1
				GROUP BY bucket
			)
			SELECT 
				COALESCE(rev.bucket, exp.bucket)::date::text AS date,
				(COALESCE(rev.total_revenue, 0) - COALESCE(exp.total_expense, 0))::BIGINT AS net_income,
				COALESCE(exp.total_expense, 0)::BIGINT AS expense,
				COALESCE(rev.total_transaction, 0)::BIGINT AS transaction_count,
				COALESCE(rev.bucket, exp.bucket) AS created_at
			FROM rev
			FULL OUTER JOIN exp ON rev.bucket = exp.bucket
			ORDER BY date ASC;
		`, tenantID, tenantID)
	}

	rows, err := h.dbPool.Query(r.Context(), breakdownSql, startDate)
	if err != nil {
		slog.Error("failed to query daily/monthly breakdown stats", "err", err)
		response.Error(w, http.StatusInternalServerError, "database breakdown query failed", err)
		return
	}
	defer rows.Close()

	var dailyList []DailyStats
	for rows.Next() {
		var ds DailyStats
		err = rows.Scan(&ds.Date, &ds.NetIncome, &ds.Expense, &ds.TransactionCount, &ds.CreatedAt)
		if err != nil {
			slog.Error("failed to scan daily stats", "err", err)
			response.Error(w, http.StatusInternalServerError, "scan failed", err)
			return
		}
		ds.UpdatedAt = ds.CreatedAt
		dailyList = append(dailyList, ds)
	}
	responsePayload.ResultDaily(dailyList)

	// 3. Query Product Sales Stats
	var productSql string
	if isMonthly {
		productSql = fmt.Sprintf(`
			SELECT 
				stats.product_variant_id,
				SUM(stats.total_transaction)::BIGINT AS items_sold,
				pv.name AS product_variant_name,
				p.id AS product_id,
				p.name AS product_name
			FROM "%s".monthly_product_sales_stats stats
			JOIN "%s".product_variant pv ON stats.product_variant_id = pv.id
			JOIN "%s".product p ON stats.product_id = p.id
			WHERE stats.bucket_month >= $1
			GROUP BY stats.product_variant_id, pv.name, p.id, p.name
			ORDER BY items_sold DESC;
		`, tenantID, tenantID, tenantID)
	} else {
		productSql = fmt.Sprintf(`
			SELECT 
				stats.product_variant_id,
				SUM(stats.total_transaction)::BIGINT AS items_sold,
				pv.name AS product_variant_name,
				p.id AS product_id,
				p.name AS product_name
			FROM "%s".daily_product_sales_stats stats
			JOIN "%s".product_variant pv ON stats.product_variant_id = pv.id
			JOIN "%s".product p ON stats.product_id = p.id
			WHERE stats.bucket >= $1
			GROUP BY stats.product_variant_id, pv.name, p.id, p.name
			ORDER BY items_sold DESC;
		`, tenantID, tenantID, tenantID)
	}

	pRows, err := h.dbPool.Query(r.Context(), productSql, startDate)
	if err != nil {
		slog.Error("failed to query product sales stats", "err", err)
		response.Error(w, http.StatusInternalServerError, "database product query failed", err)
		return
	}
	defer pRows.Close()
	pStats := []ProductStats{}
	for pRows.Next() {
		var ps ProductStats
		err = pRows.Scan(&ps.ProductVariantID, &ps.ItemsSold, &ps.ProductVariant.Name, &ps.ProductVariant.Product.ID, &ps.ProductVariant.Product.Name)
		if err != nil {
			slog.Error("failed to scan product stats row", "err", err)
			response.Error(w, http.StatusInternalServerError, "scan failed", err)
			return
		}
		ps.ProductVariant.ID = ps.ProductVariantID
		pStats = append(pStats, ps)
	}
	responsePayload.Product = pStats

	// 4. Query Platform Stats
	var platformSql string
	if isMonthly {
		platformSql = fmt.Sprintf(`
			SELECT 
				platform,
				SUM(total_transaction)::BIGINT AS transaction_count
			FROM "%s".monthly_platform_stats
			WHERE bucket_month >= $1
			GROUP BY platform
			ORDER BY transaction_count DESC;
		`, tenantID)
	} else {
		platformSql = fmt.Sprintf(`
			SELECT 
				platform,
				SUM(total_transaction)::BIGINT AS transaction_count
			FROM "%s".daily_platform_stats
			WHERE bucket >= $1
			GROUP BY platform
			ORDER BY transaction_count DESC;
		`, tenantID)
	}

	platRows, err := h.dbPool.Query(r.Context(), platformSql, startDate)
	if err != nil {
		slog.Error("failed to query platform stats", "err", err)
		response.Error(w, http.StatusInternalServerError, "database platform query failed", err)
		return
	}
	defer platRows.Close()
	platStats := []PlatformStats{}
	for platRows.Next() {
		var pl PlatformStats
		if err := platRows.Scan(&pl.Platform, &pl.TransactionCount); err != nil {
			slog.Error("failed to scan platform stats row", "err", err)
			response.Error(w, http.StatusInternalServerError, "scan failed", err)
			return
		}
		platStats = append(platStats, pl)
	}
	responsePayload.Platform = platStats

	// 5. Query Peak Hour Stats
	peakHourSql := fmt.Sprintf(`
		SELECT 
			EXTRACT(HOUR FROM (bucket AT TIME ZONE 'Asia/Jakarta'))::SMALLINT AS hour,
			SUM(total_transaction)::BIGINT AS transaction_count
		FROM "%s".peak_hour_stats
		WHERE bucket >= $1
		GROUP BY hour
		ORDER BY hour ASC;
	`, tenantID)

	peakRows, err := h.dbPool.Query(r.Context(), peakHourSql, startDate)
	if err != nil {
		slog.Error("failed to query peak hour stats", "err", err)
		response.Error(w, http.StatusInternalServerError, "database peak hour query failed", err)
		return
	}
	defer peakRows.Close()
	peakStats := []PeakHourStats{}
	for peakRows.Next() {
		var ph PeakHourStats
		if err := peakRows.Scan(&ph.Hour, &ph.TransactionCount); err != nil {
			slog.Error("failed to scan peak hour stats row", "err", err)
			response.Error(w, http.StatusInternalServerError, "scan failed", err)
			return
		}
		peakStats = append(peakStats, ph)
	}
	responsePayload.PeakHour = peakStats

	// 6. Query Today Stats
	todaySql := fmt.Sprintf(`
		WITH rev AS (
			SELECT 
				COALESCE(SUM(ti.price), 0)::BIGINT AS total_revenue, 
				COUNT(DISTINCT t.id)::BIGINT AS total_transaction
			FROM "%s".transaction_ts t
			LEFT JOIN "%s".transaction_item_ts ti ON t.id = ti.transaction_id
			WHERE (t.created_at AT TIME ZONE 'Asia/Jakarta')::date = (NOW() AT TIME ZONE 'Asia/Jakarta')::date
		),
		exp AS (
			SELECT COALESCE(SUM(amount), 0)::BIGINT AS total_expense
			FROM "%s".expense
			WHERE (created_at AT TIME ZONE 'Asia/Jakarta')::date = (NOW() AT TIME ZONE 'Asia/Jakarta')::date
		)
		SELECT 
			(total_revenue - total_expense)::BIGINT AS net_income,
			total_expense::BIGINT AS expense,
			total_transaction::BIGINT AS transaction_count
		FROM rev, exp;
	`, tenantID, tenantID, tenantID)

	err = h.dbPool.QueryRow(r.Context(), todaySql).Scan(
		&responsePayload.Today.NetIncome,
		&responsePayload.Today.Expense,
		&responsePayload.Today.TransactionCount,
	)
	if err != nil {
		slog.Error("failed to query today stats", "err", err)
		response.Error(w, http.StatusInternalServerError, "database today stats query failed", err)
		return
	}

	response.JSON(w, http.StatusOK, responsePayload)
}

func (res *AllStatsResponse) ResultPeriod(p PeriodStats) {
	res.Revenue.Period = p
}

func (res *AllStatsResponse) ResultDaily(list []DailyStats) {
	if list == nil {
		list = []DailyStats{}
	}
	res.Revenue.Daily = list
}
