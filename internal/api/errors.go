package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-playground/validator/v10"
	"github.com/labstack/echo/v5"
)

// apiError はクライアントに返すエラー。`{"error": {"code": ..., "message": ..., "fields": {...}}}` の形で返す。
type apiError struct {
	status  int
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
}

func newAPIError(status int, code, message string) *apiError {
	return &apiError{status: status, Code: code, Message: message}
}

func (e *apiError) Error() string   { return e.Code + ": " + e.Message }
func (e *apiError) StatusCode() int { return e.status }

var (
	errUnauthorized = newAPIError(http.StatusUnauthorized, "unauthorized", "authentication is required")
	errForbidden    = newAPIError(http.StatusForbidden, "forbidden", "you do not have permission to do this")
	errNotFound     = newAPIError(http.StatusNotFound, "not_found", "not found")
)

func badRequest(message string) *apiError {
	return newAPIError(http.StatusBadRequest, "invalid_request", message)
}

// handleError はハンドラの返したエラーを JSON で返す。想定外のエラーの詳細は、クライアントには返さずログにだけ残す。
func (h *handler) handleError(c *echo.Context, err error) {
	if r, _ := echo.UnwrapResponse(c.Response()); r != nil && r.Committed {
		return
	}

	var (
		ae *apiError
		ve validator.ValidationErrors
		sc echo.HTTPStatusCoder
	)
	switch {
	case errors.As(err, &ae):
	case errors.As(err, &ve):
		fields := make(map[string]string, len(ve))
		for _, fe := range ve {
			fields[fe.Field()] = "failed the '" + fe.Tag() + "' check"
		}
		ae = &apiError{status: http.StatusBadRequest, Code: "invalid_request", Message: "the request is invalid", Fields: fields}
	case errors.As(err, &sc) && sc.StatusCode() >= 400 && sc.StatusCode() < 500:
		// ルーティング（404/405）、ボディが大きすぎる（413）など、Echo が返す 4xx。メッセージは標準の文言だけにする
		code := sc.StatusCode()
		ae = newAPIError(code, codeFor(code), strings.ToLower(http.StatusText(code)))
	default:
		h.log.Error("unhandled error", "route", c.Path(), "err", err)
		ae = newAPIError(http.StatusInternalServerError, "internal", "internal server error")
	}

	var cErr error
	if c.Request().Method == http.MethodHead {
		cErr = c.NoContent(ae.status)
	} else {
		cErr = c.JSON(ae.status, map[string]any{"error": ae})
	}
	if cErr != nil {
		h.log.Error("failed to send an error response", "err", cErr)
	}
}

func codeFor(status int) string {
	switch status {
	case http.StatusNotFound:
		return "not_found"
	case http.StatusMethodNotAllowed:
		return "method_not_allowed"
	case http.StatusRequestEntityTooLarge:
		return "too_large"
	case http.StatusBadRequest:
		return "invalid_request"
	default:
		return "error"
	}
}
