package router

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"

	"astron-claw/backend/internal/infra/telemetry"
	"astron-claw/backend/internal/model"
)

const prometheusContentType = "text/plain; version=0.0.4; charset=utf-8"

func (app *App) getMetrics(c *gin.Context) {
	text, err := telemetry.RenderPrometheusExposition(c.Request.Context(), app.RDB)
	if err != nil {
		c.String(500, "Failed to render metrics: %v", err)
		return
	}
	c.Data(http.StatusOK, prometheusContentType, []byte(text))
}

func (app *App) deleteMetrics(c *gin.Context) {
	auth := c.GetHeader("Authorization")
	if auth == "" || !strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		log.Warn().Msg("Metrics reset rejected: invalid authorization")
		model.ErrorResponse(c, model.ErrAuthMissingAuth)
		return
	}
	sessionToken := strings.TrimSpace(auth[7:])
	if sessionToken == "" {
		log.Warn().Msg("Metrics reset rejected: invalid authorization")
		model.ErrorResponse(c, model.ErrAuthMissingAuth)
		return
	}
	if !app.AdminAuth.ValidateSession(c.Request.Context(), sessionToken) {
		log.Warn().Msg("Metrics reset rejected: invalid admin session")
		model.ErrorResponse(c, model.ErrAuthInvalidSession)
		return
	}

	if err := telemetry.ResetAllMetrics(c.Request.Context(), app.RDB); err != nil {
		c.JSON(500, gin.H{"code": 500, "error": "Failed to reset metrics"})
		return
	}
	log.Info().Msg("Metrics reset by admin")
	c.JSON(200, gin.H{"code": 0, "message": "All metrics reset"})
}
