package model

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestErrorCodes(t *testing.T) {
	tests := []struct {
		name       string
		err        AppError
		code       int
		httpStatus int
		wsCode     int
	}{
		{"AuthInvalidToken", ErrAuthInvalidToken, 10001, http.StatusUnauthorized, 0},
		{"AuthMissingAuth", ErrAuthMissingAuth, 10002, http.StatusUnauthorized, 0},
		{"AuthInvalidSession", ErrAuthInvalidSession, 10003, http.StatusUnauthorized, 0},
		{"AuthUnauthorized", ErrAuthUnauthorized, 10004, http.StatusUnauthorized, 0},
		{"AuthWrongPassword", ErrAuthWrongPassword, 10005, http.StatusUnauthorized, 0},
		{"AdminPasswordExists", ErrAdminPasswordExists, 10101, http.StatusBadRequest, 0},
		{"AdminPasswordShort", ErrAdminPasswordShort, 10102, http.StatusBadRequest, 0},
		{"ChatEmptyMessage", ErrChatEmptyMessage, 10201, http.StatusBadRequest, 0},
		{"ChatNoBot", ErrChatNoBot, 10202, http.StatusBadRequest, 0},
		{"ChatSendFailed", ErrChatSendFailed, 10204, http.StatusInternalServerError, 0},
		{"ChatStreamTimeout", ErrChatStreamTimeout, 10205, 0, 0},
		{"ChatInternalError", ErrChatInternalError, 10206, 0, 0},
		{"MediaFileTooLarge", ErrMediaFileTooLarge, 10301, http.StatusRequestEntityTooLarge, 0},
		{"MediaInvalidFile", ErrMediaInvalidFile, 10302, http.StatusBadRequest, 0},
		{"MediaBadURLScheme", ErrMediaBadURLScheme, 10303, http.StatusBadRequest, 0},
		{"MediaUnsupportedType", ErrMediaUnsupportedType, 10304, http.StatusBadRequest, 0},
		{"MediaTooMany", ErrMediaTooMany, 10305, http.StatusBadRequest, 0},
		{"SessionNotFound", ErrSessionNotFound, 10401, http.StatusNotFound, 0},
		{"TokenNotFound", ErrTokenNotFound, 10501, http.StatusNotFound, 0},
		{"WSInvalidToken", ErrWSInvalidToken, 10601, 0, 4001},
		{"WSTokenDeleted", ErrWSTokenDeleted, 10602, 0, 4003},
		{"WSServerRestart", ErrWSServerRestart, 10603, 0, 4000},
		{"WSEvicted", ErrWSEvicted, 10604, 0, 4005},
		{"BotUnknownError", ErrBotUnknownError, 10701, 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err.Code != tt.code {
				t.Errorf("%s: code = %d, want %d", tt.name, tt.err.Code, tt.code)
			}
			if tt.err.HTTPStatus != tt.httpStatus {
				t.Errorf("%s: httpStatus = %d, want %d", tt.name, tt.err.HTTPStatus, tt.httpStatus)
			}
			if tt.err.WSCode != tt.wsCode {
				t.Errorf("%s: wsCode = %d, want %d", tt.name, tt.err.WSCode, tt.wsCode)
			}
			if tt.err.Message == "" {
				t.Errorf("%s: message should not be empty", tt.name)
			}
		})
	}
}

func TestErrorResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("standard error", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		ErrorResponse(c, ErrChatNoBot)

		if w.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
		}
		var resp map[string]interface{}
		json.Unmarshal(w.Body.Bytes(), &resp)
		if resp["error"] != "No bot connected" {
			t.Errorf("error = %v, want 'No bot connected'", resp["error"])
		}
		if code, ok := resp["code"].(float64); !ok || int(code) != 10202 {
			t.Errorf("code = %v, want 10202", resp["code"])
		}
	})

	t.Run("with detail", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		ErrorResponse(c, ErrSessionNotFound, "sess-abc")

		var resp map[string]interface{}
		json.Unmarshal(w.Body.Bytes(), &resp)
		errMsg, _ := resp["error"].(string)
		if errMsg != "Session not found: sess-abc" {
			t.Errorf("error = %q, want 'Session not found: sess-abc'", errMsg)
		}
		if code, ok := resp["code"].(float64); !ok || int(code) != 10401 {
			t.Errorf("code = %v, want 10401", resp["code"])
		}
	})

	t.Run("zero httpStatus defaults to 500", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		ErrorResponse(c, ErrChatStreamTimeout)

		if w.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", w.Code)
		}
		var resp map[string]interface{}
		json.Unmarshal(w.Body.Bytes(), &resp)
		if code, ok := resp["code"].(float64); !ok || int(code) != 10205 {
			t.Errorf("code = %v, want 10205", resp["code"])
		}
	})
}
