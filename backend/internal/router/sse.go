package router

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"astron-claw/backend/internal/infra/telemetry"
	"astron-claw/backend/internal/model"
	"astron-claw/backend/internal/pkg"
	"astron-claw/backend/internal/service"
)

const (
	sseTimeout        = 600 // 10 minutes
	sseBlockMs        = 1000
	heartbeatInterval = 15.0 // seconds
)

type ChatRequest struct {
	Content   string      `json:"content"`
	SessionID *string     `json:"sessionId,omitempty"`
	Media     []MediaItem `json:"media,omitempty"`
}

type MediaItem struct {
	Type     string `json:"type"`
	Content  string `json:"content"`
	MimeType string `json:"mimeType,omitempty"`
}

func (app *App) chatSSE(c *gin.Context) {
	t0 := time.Now()
	tokenStr := c.GetString("token")
	tp := telemetry.TokenPrefix(tokenStr)

	reqStatus := "success"
	reqCode := 200
	durationRecorded := false
	recordReq := func() {
		counterAttrs := metric.WithAttributeSet(attribute.NewSet(
			attribute.String("status", reqStatus),
			attribute.String("code", strconv.Itoa(reqCode)),
		))
		telemetry.ChatRequestTotal.Add(context.Background(), 1, counterAttrs)
		// Record first-byte latency for failed requests in defer;
		// successful requests record it at SSE stream entry.
		if !durationRecorded {
			histAttrs := metric.WithAttributeSet(attribute.NewSet(
				attribute.String("status", reqStatus),
			))
			telemetry.ChatRequestDuration.Record(context.Background(), time.Since(t0).Seconds(), histAttrs)
		}
	}
	defer recordReq()

	var body ChatRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		reqStatus = "bad_request"
		reqCode = 400
		model.ErrorResponse(c, model.ErrChatInvalidReq)
		return
	}

	// Validate media
	content := body.Content
	var mediaURLs []string

	if len(body.Media) > 10 {
		log.Warn().Str("token", tp).Msg("SSE: bad request — too many media items")
		reqStatus = "bad_request"
		reqCode = 400
		model.ErrorResponse(c, model.ErrMediaTooMany)
		return
	}

	if body.Media != nil {
		for _, item := range body.Media {
			if item.Type == "url" {
				if !strings.HasPrefix(item.Content, "http://") && !strings.HasPrefix(item.Content, "https://") {
					log.Warn().Str("url", item.Content).Str("token", tp).
						Msg("SSE: bad request — invalid media URL scheme")
					reqStatus = "bad_request"
					reqCode = 400
					model.ErrorResponse(c, model.ErrMediaBadURLScheme)
					return
				}
				mediaURLs = append(mediaURLs, item.Content)
			} else {
				log.Warn().Str("type", item.Type).Str("token", tp).
					Msg("SSE: bad request — unsupported media type")
				reqStatus = "bad_request"
				reqCode = 400
				model.ErrorResponse(c, model.ErrMediaUnsupportedType)
				return
			}
		}
	}

	if content == "" && len(mediaURLs) == 0 {
		log.Warn().Str("token", tp).Msg("SSE: bad request — empty message")
		reqStatus = "bad_request"
		reqCode = 400
		model.ErrorResponse(c, model.ErrChatEmptyMessage)
		return
	}

	// Check bot connected
	ctx := c.Request.Context()
	if !app.Bridge.IsBotConnected(ctx, tokenStr) {
		log.Warn().Str("token", tp).Msg("SSE: no bot connected")
		reqStatus = "no_bot"
		reqCode = 400
		model.ErrorResponse(c, model.ErrChatNoBot)
		return
	}

	// Resolve session
	var sessionID string
	var sessionNumber int
	if body.SessionID != nil && *body.SessionID != "" {
		sid, snum, found := app.Bridge.GetSession(ctx, tokenStr, *body.SessionID)
		if !found {
			log.Warn().Str("session", *body.SessionID).Str("token", tp).
				Msg("SSE: session not found")
			reqStatus = "session_not_found"
			reqCode = 404
			model.ErrorResponse(c, model.ErrSessionNotFound)
			return
		}
		sessionID = sid
		sessionNumber = snum
	} else {
		var err error
		sessionID, sessionNumber, err = app.Bridge.CreateSession(ctx, tokenStr)
		if err != nil {
			log.Error().Err(err).Str("token", tp).Msg("SSE: failed to create session")
			reqStatus = "error"
			reqCode = model.ErrSessionCreateFailed.Code
			model.ErrorResponse(c, model.ErrSessionCreateFailed)
			return
		}
	}

	// Clear stale events and reset consumer group
	inbox := service.ChatInboxPrefix + tokenStr + ":" + sessionID
	app.Queue.Purge(ctx, inbox)
	app.Queue.EnsureGroup(ctx, inbox, "sse")
	app.Bridge.TrackChatInbox(ctx, tokenStr, inbox)

	// Send message to bot
	reqID, err := app.Bridge.SendToBot(ctx, tokenStr, content, mediaURLs, sessionID)
	if err != nil {
		log.Error().Err(err).Str("token", tp).Msg("SSE: send_to_bot failed")
		reqStatus = "send_fail"
		reqCode = 500
		model.ErrorResponse(c, model.ErrChatSendFailed)
		return
	}

	// Success — entering SSE stream; record first-byte latency now (not at stream end).
	durationRecorded = true
	telemetry.ChatRequestDuration.Record(context.Background(), time.Since(t0).Seconds(),
		metric.WithAttributeSet(attribute.NewSet(
			attribute.String("status", reqStatus),
		)),
	)

	log.Info().Str("req", reqID).Str("session", pkg.SafePrefix(sessionID, 8)).Str("token", tp).
		Msg("SSE: chat started")

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		model.ErrorResponse(c, model.ErrChatStreamUnsupported)
		return
	}

	// Set SSE headers
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)

	// Track active stream
	streamStart := time.Now()
	closeReason := "done"
	telemetry.ChatActiveStreams.Add(ctx, 1)

	// 注册SSE连接到bot状态监控器
	var sseConn *service.SSEConnection
	if app.BotStatusMonitor != nil {
		sseConn = app.BotStatusMonitor.RegisterSSEConnection(tokenStr, sessionID, inbox)
		defer app.BotStatusMonitor.UnregisterSSEConnection(inbox)
	}

	defer func() {
		telemetry.ChatActiveStreams.Add(context.Background(), -1)
		streamDuration := time.Since(streamStart).Seconds()
		telemetry.ChatStreamDuration.Record(context.Background(), streamDuration,
			metric.WithAttributeSet(attribute.NewSet(
				attribute.String("close_reason", closeReason),
			)),
		)
	}()

	// Stream events — use a deadline tracker instead of re-creating contexts
	deadline := time.Now().Add(sseTimeout * time.Second)

	// First event: session info
	sessionEvent := pkg.FormatSSEEvent("session", map[string]interface{}{
		"sessionId":     sessionID,
		"sessionNumber": sessionNumber,
	})
	if _, err := c.Writer.WriteString(sessionEvent); err != nil {
		closeReason = "write_error"
		return
	}
	flusher.Flush()

	lastHeartbeat := time.Now()
	hasChunks := false

	// 准备 bot 断开监听 channel
	var botDisconnectC <-chan struct{}
	if sseConn != nil {
		botDisconnectC = sseConn.DisconnectC
	} else {
		// 创建一个永远不会触发的 channel（nil channel 在 select 中会被忽略）
		botDisconnectC = nil
	}

	for {
		// Check client disconnect and bot disconnect
		select {
		case <-c.Request.Context().Done():
			closeReason = "client_disconnect"
			log.Info().Str("token", tp).Msg("SSE: client disconnected")
			go app.Bridge.SendCancelToBot(context.Background(), tokenStr, sessionID)
			return
		case <-botDisconnectC:
			closeReason = "bot_disconnect"
			log.Info().Str("token", tp).Msg("SSE: bot disconnected")
			errEvent := pkg.FormatSSEEvent("error", map[string]interface{}{
				"content": model.ErrChatNoBot.Message,
			})
			_, _ = c.Writer.WriteString(errEvent)
			flusher.Flush()
			return
		default:
		}

		// Check timeout
		if time.Now().After(deadline) {
			closeReason = "timeout"
			errEvent := pkg.FormatSSEEvent("error", map[string]interface{}{
				"content": model.ErrChatStreamTimeout.Message,
			})
			_, _ = c.Writer.WriteString(errEvent)
			flusher.Flush()
			return
		}

		result, err := app.Queue.Consume(c.Request.Context(), inbox, "sse", reqID, sseBlockMs)
		if err != nil {
			log.Error().Err(err).Str("token", tp).Msg("SSE: consume error")
			closeReason = "error"
			errEvent := pkg.FormatSSEEvent("error", map[string]interface{}{
				"content": model.ErrChatInternalError.Message,
			})
			_, _ = c.Writer.WriteString(errEvent)
			flusher.Flush()
			return
		}

		if result == nil {
			// Check client disconnect and bot disconnect
			select {
			case <-c.Request.Context().Done():
				closeReason = "client_disconnect"
				go app.Bridge.SendCancelToBot(context.Background(), tokenStr, sessionID)
				return
			case <-botDisconnectC:
				closeReason = "bot_disconnect"
				log.Info().Str("token", tp).Msg("SSE: bot disconnected")
				errEvent := pkg.FormatSSEEvent("error", map[string]interface{}{
					"content": model.ErrChatNoBot.Message,
				})
				_, _ = c.Writer.WriteString(errEvent)
				flusher.Flush()
				return
			default:
			}

			// Heartbeat
			if time.Since(lastHeartbeat).Seconds() >= heartbeatInterval {
				_, _ = c.Writer.WriteString(pkg.FormatSSEComment())
				flusher.Flush()
				lastHeartbeat = time.Now()
			}
			continue
		}

		_ = app.Queue.Ack(context.Background(), inbox, "sse", result.ID)

		// Reset deadline on activity
		deadline = time.Now().Add(sseTimeout * time.Second)

		var event map[string]interface{}
		if err := json.Unmarshal([]byte(result.Data), &event); err != nil {
			log.Warn().Str("token", tp).Msg("SSE: invalid JSON in inbox")
			continue
		}

		eventType, _ := event["type"].(string)
		if eventType == "" {
			eventType = "message"
		}
		delete(event, "type")

		var eventData map[string]interface{}
		if eventType == "media" {
			if d, ok := event["data"].(map[string]interface{}); ok {
				eventData = d
			} else {
				log.Warn().Str("token", tp).Msg("SSE: media event missing data payload")
				continue
			}
		} else {
			eventData = event
		}

		if eventType == "chunk" {
			hasChunks = true
		}

		// Auto-inject chunk before done if no preceding chunks
		if eventType == "done" && !hasChunks {
			if contentStr, ok := eventData["content"].(string); ok && contentStr != "" {
				chunkEvent := pkg.FormatSSEEvent("chunk", map[string]interface{}{
					"content": contentStr,
				})
				_, _ = c.Writer.WriteString(chunkEvent)
				flusher.Flush()
			}
		}

		sseEvent := pkg.FormatSSEEvent(eventType, eventData)
		_, _ = c.Writer.WriteString(sseEvent)
		flusher.Flush()

		// Terminal events
		if eventType == "done" || eventType == "error" {
			return
		}
	}
}

func (app *App) listSessions(c *gin.Context) {
	tokenStr := c.GetString("token")

	sessions, err := app.Bridge.GetSessions(c.Request.Context(), tokenStr)
	if err != nil {
		log.Error().Err(err).Msg("Failed to list sessions")
		model.ErrorResponse(c, model.ErrChatInternalError)
		return
	}

	sessionList := make([]gin.H, len(sessions))
	for i, s := range sessions {
		sessionList[i] = gin.H{"id": s.ID, "number": s.Number}
	}

	c.JSON(200, gin.H{
		"code":     0,
		"sessions": sessionList,
	})
}

func (app *App) createSession(c *gin.Context) {
	tokenStr := c.GetString("token")
	ctx := c.Request.Context()

	sessionID, sessionNumber, err := app.Bridge.CreateSession(ctx, tokenStr)
	if err != nil {
		log.Error().Err(err).Msg("Failed to create session")
		model.ErrorResponse(c, model.ErrSessionCreateFailed)
		return
	}

	sessions, err := app.Bridge.GetSessions(ctx, tokenStr)
	if err != nil {
		log.Error().Err(err).Msg("Failed to list sessions")
		model.ErrorResponse(c, model.ErrChatInternalError)
		return
	}

	sessionList := make([]gin.H, len(sessions))
	for i, s := range sessions {
		sessionList[i] = gin.H{"id": s.ID, "number": s.Number}
	}

	c.JSON(200, gin.H{
		"code":          0,
		"sessionId":     sessionID,
		"sessionNumber": sessionNumber,
		"sessions":      sessionList,
	})
}
