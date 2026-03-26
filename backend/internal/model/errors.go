package model

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// AppError defines a structured application error with a unique error code,
// an HTTP status code for the response, and a human-readable message.
type AppError struct {
	Code       int    // 应用错误码 (10000+)
	HTTPStatus int    // HTTP 状态码
	WSCode     int    // WebSocket 关闭码 (仅 WS 错误使用, 否则为 0)
	Message    string
}

var (
	// ── Auth 认证错误 (10001-10099) ─────────────────────────
	ErrAuthInvalidToken   = AppError{Code: 10001, HTTPStatus: http.StatusUnauthorized, Message: "Invalid or missing token"}
	ErrAuthMissingAuth    = AppError{Code: 10002, HTTPStatus: http.StatusUnauthorized, Message: "Missing authorization"}
	ErrAuthInvalidSession = AppError{Code: 10003, HTTPStatus: http.StatusUnauthorized, Message: "Invalid admin session"}
	ErrAuthUnauthorized   = AppError{Code: 10004, HTTPStatus: http.StatusUnauthorized, Message: "Unauthorized"}
	ErrAuthWrongPassword  = AppError{Code: 10005, HTTPStatus: http.StatusUnauthorized, Message: "Wrong password"}

	// ── Admin 管理员错误 (10101-10199) ──────────────────────
	ErrAdminPasswordExists = AppError{Code: 10101, HTTPStatus: http.StatusBadRequest, Message: "Password already set"}
	ErrAdminPasswordShort  = AppError{Code: 10102, HTTPStatus: http.StatusBadRequest, Message: "Password too short"}

	// ── Chat / SSE 聊天错误 (10201-10299) ───────────────────
	ErrChatEmptyMessage  = AppError{Code: 10201, HTTPStatus: http.StatusBadRequest, Message: "Empty message"}
	ErrChatNoBot         = AppError{Code: 10202, HTTPStatus: http.StatusBadRequest, Message: "No bot connected"}
	ErrChatInvalidReq    = AppError{Code: 10203, HTTPStatus: http.StatusBadRequest, Message: "Invalid request"}
	ErrChatSendFailed    = AppError{Code: 10204, HTTPStatus: http.StatusInternalServerError, Message: "Failed to send message to bot"}
	ErrChatStreamTimeout = AppError{Code: 10205, Message: "Stream timeout"}
	ErrChatInternalError = AppError{Code: 10206, Message: "Internal server error"}

	// ── Media 媒体错误 (10301-10399) ────────────────────────
	ErrMediaFileTooLarge    = AppError{Code: 10301, HTTPStatus: http.StatusRequestEntityTooLarge, Message: "File too large"}
	ErrMediaInvalidFile     = AppError{Code: 10302, HTTPStatus: http.StatusBadRequest, Message: "Invalid file or unsupported type"}
	ErrMediaBadURLScheme    = AppError{Code: 10303, HTTPStatus: http.StatusBadRequest, Message: "Invalid media URL scheme"}
	ErrMediaUnsupportedType = AppError{Code: 10304, HTTPStatus: http.StatusBadRequest, Message: "Unsupported media type"}
	ErrMediaTooMany         = AppError{Code: 10305, HTTPStatus: http.StatusBadRequest, Message: "Too many media items (max 10)"}

	// ── Session 会话错误 (10401-10499) ──────────────────────
	ErrSessionNotFound = AppError{Code: 10401, HTTPStatus: http.StatusNotFound, Message: "Session not found"}

	// ── Token 错误 (10501-10599) ────────────────────────────
	ErrTokenNotFound = AppError{Code: 10501, HTTPStatus: http.StatusNotFound, Message: "Token not found"}

	// ── WebSocket 错误 (10601-10699) ────────────────────────
	ErrWSInvalidToken  = AppError{Code: 10601, WSCode: 4001, Message: "Invalid or missing bot token"}
	ErrWSTokenDeleted  = AppError{Code: 10602, WSCode: 4003, Message: "Token deleted"}
	ErrWSServerRestart = AppError{Code: 10603, WSCode: 4000, Message: "Server restarting"}
	ErrWSEvicted       = AppError{Code: 10604, WSCode: 4005, Message: "Evicted by newer connection"}

	// ── Bot 内部错误 (10701-10799) ──────────────────────────
	ErrBotUnknownError = AppError{Code: 10701, Message: "Unknown error from bot"}
)

// ErrorResponse returns a JSON error response via gin.Context.
// The response body contains the application error code (10000+) and message.
func ErrorResponse(c *gin.Context, err AppError, detail ...string) {
	msg := err.Message
	if len(detail) > 0 && detail[0] != "" {
		msg = msg + ": " + detail[0]
	}
	httpStatus := err.HTTPStatus
	if httpStatus == 0 {
		httpStatus = http.StatusInternalServerError
	}
	c.JSON(httpStatus, gin.H{"code": err.Code, "error": msg})
}
