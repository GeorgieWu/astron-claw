# 错误码结构完善方案

## Context

当前项目的错误码系统使用 HTTP 状态码作为错误标识（400, 401, 404, 413, 500），这导致：
1. 客户端难以精确区分同一 HTTP 状态码下的不同业务错误
2. 缺乏统一的应用层错误码体系
3. WebSocket 错误码（4000-4005）与 HTTP 错误码混用

用户要求建立从 10000 开始的应用层错误码体系，并完善 API 文档。

## Implementation Plan

### 1. 重构 AppError 结构

**文件**: `backend/internal/model/errors.go`

将当前的 `AppError` 结构从：
```go
type AppError struct {
    Code    int    // HTTP status code
    Message string
}
```

改为：
```go
type AppError struct {
    Code       int    // 应用错误码 (10000+)
    HTTPStatus int    // HTTP 状态码
    Message    string
}
```

### 2. 重新分配错误码

按模块划分错误码范围（共 22 个错误）：

**认证错误 (10000-10099)**
- 10001: ErrAuthInvalidToken - "Invalid or missing token"
- 10002: ErrAuthMissingAuth - "Missing authorization"
- 10003: ErrAuthInvalidSession - "Invalid admin session"
- 10004: ErrAuthUnauthorized - "Unauthorized"
- 10005: ErrAuthWrongPassword - "Wrong password"

**管理员错误 (10100-10199)**
- 10101: ErrAdminPasswordExists - "Password already set"
- 10102: ErrAdminPasswordShort - "Password too short"

**聊天/SSE 错误 (10200-10299)**
- 10201: ErrChatEmptyMessage - "Empty message"
- 10202: ErrChatNoBot - "No bot connected"
- 10203: ErrChatInvalidReq - "Invalid request"
- 10204: ErrChatSendFailed - "Failed to send message to bot"
- 10205: ErrChatStreamTimeout - "Stream timeout"
- 10206: ErrChatInternalError - "Internal server error"

**媒体错误 (10300-10399)**
- 10301: ErrMediaFileTooLarge - "File too large"
- 10302: ErrMediaInvalidFile - "Invalid file or unsupported type"
- 10303: ErrMediaBadURLScheme - "Invalid media URL scheme"
- 10304: ErrMediaUnsupportedType - "Unsupported media type"
- 10305: ErrMediaTooMany - "Too many media items (max 10)"

**会话错误 (10400-10499)**
- 10401: ErrSessionNotFound - "Session not found"

**Token 错误 (10500-10599)**
- 10501: ErrTokenNotFound - "Token not found"

**WebSocket 错误 (10600-10699)**
- 10601: ErrWSInvalidToken - "Invalid or missing bot token" (WS close: 4001)
- 10602: ErrWSTokenDeleted - "Token deleted" (WS close: 4003)
- 10603: ErrWSServerRestart - "Server restarting" (WS close: 4000)
- 10604: ErrWSEvicted - "Evicted by newer connection" (WS close: 4005)

**Bot 错误 (10700-10799)**
- 10701: ErrBotUnknownError - "Unknown error from bot"

### 3. 更新 ErrorResponse 函数

修改 `ErrorResponse` 函数返回应用错误码而非 HTTP 状态码：

```go
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
```

响应格式从 `{"code": 400, "error": "..."}` 变为 `{"code": 10201, "error": "..."}`

### 4. 更新 API 文档

**文件**: `docs/api.md`

更新第 1480-1553 行的错误码章节：
- 修改"统一响应格式"说明，明确 `code` 字段为应用错误码
- 更新错误码清单表格，添加应用错误码列
- 保留 HTTP 状态码列用于说明
- WebSocket 关闭码保持不变（4000-4005）

表格格式：
```
| 应用错误码 | HTTP 状态码 | 消息 | 使用场景 |
|-----------|------------|------|---------|
| 10001 | 401 | Invalid or missing token | Token 无效或缺失 |
```

### 5. 创建错误码文档

**新建文件**: `docs/error-codes.md`

创建独立的错误码参考文档，包含：
- 错误码设计原则
- 完整错误码清单（按模块分类）
- 客户端错误处理建议
- 错误码扩展指南

## Critical Files

- `backend/internal/model/errors.go` - 错误定义（核心修改）
- `docs/api.md` - API 文档（错误码章节更新）
- `docs/error-codes.md` - 新建错误码参考文档

## Verification

1. **编译检查**: `cd backend && go build ./...` 确保无编译错误
2. **测试验证**: `cd backend && go test ./...` 确保现有测试通过
3. **API 响应验证**: 启动服务后测试几个错误场景，确认返回的 `code` 字段为 10000+ 的应用错误码
4. **文档一致性**: 检查 `docs/api.md` 和 `docs/error-codes.md` 中的错误码与代码定义一致
