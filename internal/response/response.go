package response

import (
	"encoding/json"
	"net/http"
	"strconv"
)

// ErrorResponse represents a standardized HTTP error payload
type ErrorResponse struct {
	Error string `json:"error"`
}

// ValidationErrorResponse represents a standardized validation failure payload
type ValidationErrorResponse struct {
	Error  string   `json:"error"`
	Errors []string `json:"errors"`
}

// PaginationMeta holds paging metadata
type PaginationMeta struct {
	Total      int64 `json:"total"`
	Page       int   `json:"page"`
	Limit      int   `json:"limit"`
	TotalPages int   `json:"totalPages"`
}

// PaginationResponse is a generic wrapper for paginated lists
type PaginationResponse[T any] struct {
	Data []T            `json:"data"`
	Meta PaginationMeta `json:"meta"`
}

// JSON sends a JSON response with the specified status code.
// Tag struct json:"field,string" in nested types automatically serializes int64 to string.
func JSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if data != nil {
		_ = json.NewEncoder(w).Encode(data)
	}
}

// Error sends a standardized error JSON response.
func Error(w http.ResponseWriter, status int, message string) {
	JSON(w, status, ErrorResponse{Error: message})
}

// ValidationError sends a standardized validation error JSON response.
func ValidationError(w http.ResponseWriter, errors []string) {
	JSON(w, http.StatusBadRequest, ValidationErrorResponse{
		Error:  "validation failed",
		Errors: errors,
	})
}

// ParsePagination parses standard page/limit parameters from the request URL query
func ParsePagination(r *http.Request) (page int, limit int, offset int) {
	q := r.URL.Query()
	page = 1
	if pStr := q.Get("page"); pStr != "" {
		if p, err := strconv.Atoi(pStr); err == nil && p > 0 {
			page = p
		}
	}
	limit = 10
	if lStr := q.Get("limit"); lStr != "" {
		if l, err := strconv.Atoi(lStr); err == nil && l > 0 {
			limit = l
		}
	}
	offset = (page - 1) * limit
	return page, limit, offset
}

// NewPaginationResponse wraps a slice into a PaginationResponse
func NewPaginationResponse[T any](data []T, total int64, page int, limit int) PaginationResponse[T] {
	if data == nil {
		data = []T{} // prevent null in JSON
	}
	totalPages := 1
	if limit > 0 {
		totalPages = int((total + int64(limit) - 1) / int64(limit))
	}
	if totalPages == 0 {
		totalPages = 1
	}
	return PaginationResponse[T]{
		Data: data,
		Meta: PaginationMeta{
			Total:      total,
			Page:       page,
			Limit:      limit,
			TotalPages: totalPages,
		},
	}
}
