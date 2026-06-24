package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

type TestStruct struct {
	Name  string `json:"name" validate:"required"`
	Age   int    `json:"age" validate:"required,gte=18"`
	Email string `json:"email" validate:"required,email"`
}

func TestValidateBody(t *testing.T) {
	handlerFunc := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := GetBody[TestStruct](r)
		w.Header().Set("X-Test-Name", body.Name)
		w.WriteHeader(http.StatusOK)
	})

	t.Run("Validation_Success", func(t *testing.T) {
		jsonBody := `{"name":"John Doe","age":25,"email":"john.doe@example.com"}`
		req := httptest.NewRequest(http.MethodPost, "/test", bytes.NewBufferString(jsonBody))
		rec := httptest.NewRecorder()

		ValidateBody[TestStruct]()(handlerFunc).ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d (%s)", rec.Code, rec.Body.String())
		}
		if rec.Header().Get("X-Test-Name") != "John Doe" {
			t.Errorf("expected header value 'John Doe', got %q", rec.Header().Get("X-Test-Name"))
		}
	})

	t.Run("Validation_MissingField", func(t *testing.T) {
		jsonBody := `{"age":25,"email":"john.doe@example.com"}`
		req := httptest.NewRequest(http.MethodPost, "/test", bytes.NewBufferString(jsonBody))
		rec := httptest.NewRecorder()

		ValidateBody[TestStruct]()(handlerFunc).ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected status 400, got %d", rec.Code)
		}
	})

	t.Run("Validation_InvalidEmail", func(t *testing.T) {
		jsonBody := `{"name":"John Doe","age":25,"email":"invalid-email-format"}`
		req := httptest.NewRequest(http.MethodPost, "/test", bytes.NewBufferString(jsonBody))
		rec := httptest.NewRecorder()

		ValidateBody[TestStruct]()(handlerFunc).ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected status 400, got %d", rec.Code)
		}
	})
}
