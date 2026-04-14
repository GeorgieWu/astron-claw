package router

import (
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"

	"astron-claw/backend/internal/middleware"
	"astron-claw/backend/internal/model"
	"astron-claw/backend/internal/pkg"
)

func (app *App) createToken(c *gin.Context) {
	token, err := app.TokenMgr.Generate(c.Request.Context(), "", 0)
	if err != nil {
		log.Error().Err(err).Msg("Failed to generate token")
		middleware.MetricsErrorResponse(c, model.ErrChatInternalError)
		return
	}
	log.Info().Str("token", pkg.SafePrefix(token, 10)).Msg("Token created via public API")
	c.JSON(200, gin.H{"code": 0, "token": token})
}

func (app *App) validateToken(c *gin.Context) {
	var body struct {
		Token string `json:"token"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		middleware.MetricsErrorResponse(c, model.ErrChatInvalidReq)
		return
	}

	valid := app.TokenMgr.Validate(c.Request.Context(), body.Token)
	botConnected := false
	if valid {
		botConnected = app.Bridge.IsBotConnected(c.Request.Context(), body.Token)
	}

	tokenPrefix := pkg.SafePrefix(body.Token, 10)
	log.Debug().Str("token", tokenPrefix).Bool("valid", valid).Msg("Token validate")

	c.JSON(200, gin.H{
		"code":          0,
		"valid":         valid,
		"bot_connected": botConnected,
	})
}
