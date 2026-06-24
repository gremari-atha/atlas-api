package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"atlas-api/internal/response"

	"github.com/go-playground/validator/v10"
)

type contextKey string

const bodyContextKey contextKey = "validatedBody"

var validate = validator.New()

// ValidateBody is a generic middleware that decodes and validates request body using go-playground/validator
func ValidateBody[T any]() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body T
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				response.Error(w, http.StatusBadRequest, "invalid request body: "+err.Error())
				return
			}

			if err := validate.Struct(body); err != nil {
				var errMsgs []string
				if validationErrors, ok := err.(validator.ValidationErrors); ok {
					for _, ve := range validationErrors {
						msg := fmt.Sprintf("field '%s' is invalid on rules '%s'", ve.Field(), ve.Tag())
						errMsgs = append(errMsgs, msg)
					}
				} else {
					errMsgs = append(errMsgs, err.Error())
				}
				response.ValidationError(w, errMsgs)
				return
			}

			ctx := context.WithValue(r.Context(), bodyContextKey, body)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// GetBody retrieves the validated body from the request context
func GetBody[T any](r *http.Request) T {
	val := r.Context().Value(bodyContextKey)
	if val == nil {
		var zero T
		return zero
	}
	return val.(T)
}
