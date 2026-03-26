# 错误码参考文档

## 概述

Astron Claw 使用统一的应用层错误码体系（10000+），与 HTTP 状态码分离，便于客户端精确识别错误类型。

## 错误码设计原则

1. **应用错误码**：10000+ 的整数，用于 JSON 响应的 `code` 字段
2. **HTTP 状态码**：仍用于 HTTP 响应状态行，表示请求处理结果
3. **WebSocket 关闭码**：4000-4999 自定义范围，用于 WebSocket 连接关闭
4. **模块化分段**：按功能模块划分错误码范围，便于扩展

## 错误码范围分配

| 范围 | 模块 | 说明 |
|------|------|------|
| 10001-10099 | 认证 (Auth) | Token 验证、Session 管理、密码认证 |
| 10100-10199 | 管理员 (Admin) | 管理员设置、权限相关 |
| 10200-10299 | 聊天 (Chat/SSE) | 消息发送、SSE 流相关 |
| 10300-10399 | 媒体 (Media) | 文件上传、媒体处理 |
| 10400-10499 | 会话 (Session) | 会话管理 |
| 10500-10599 | Token | Token CRUD 操作 |
| 10600-10699 | WebSocket | Bot WebSocket 连接 |
| 10700-10799 | Bot | Bot 内部错误 |

## 完整错误码清单

### 认证错误 (10001-10099)

| 错误码 | HTTP 状态 | 消息 | 说明 |
|--------|----------|------|------|
| 10001 | 401 | Invalid or missing token | Token 无效或缺失 |
| 10002 | 401 | Missing authorization | 缺少 Authorization Header |
| 10003 | 401 | Invalid admin session | Admin Session 无效或过期 |
| 10004 | 401 | Unauthorized | Admin 未认证 |
| 10005 | 401 | Wrong password | 管理员登录密码错误 |

### 管理员错误 (10100-10199)

| 错误码 | HTTP 状态 | 消息 | 说明 |
|--------|----------|------|------|
| 10101 | 400 | Password already set | 重复设置密码 |
| 10102 | 400 | Password too short | 密码少于 4 个字符 |

### 聊天/SSE 错误 (10200-10299)

| 错误码 | HTTP 状态 | 消息 | 说明 |
|--------|----------|------|------|
| 10201 | 400 | Empty message | 消息内容和媒体均为空 |
| 10202 | 400 | No bot connected | Token 对应的 Bot 未在线 |
| 10203 | 400 | Invalid request | 请求格式错误 |
| 10204 | 500 | Failed to send message to bot | 消息推送到 Bot 失败 |
| 10205 | — | Stream timeout | SSE 流超时（仅 SSE 事件） |
| 10206 | — | Internal server error | 内部错误（仅 SSE 事件） |

### 媒体错误 (10300-10399)

| 错误码 | HTTP 状态 | 消息 | 说明 |
|--------|----------|------|------|
| 10301 | 413 | File too large | 文件超过大小限制 |
| 10302 | 400 | Invalid file or unsupported type | 无效文件或不支持的类型 |
| 10303 | 400 | Invalid media URL scheme | 媒体 URL 非 http/https |
| 10304 | 400 | Unsupported media type | 不支持的媒体类型 |
| 10305 | 400 | Too many media items (max 10) | 媒体项超过 10 个 |

### 会话错误 (10400-10499)

| 错误码 | HTTP 状态 | 消息 | 说明 |
|--------|----------|------|------|
| 10401 | 404 | Session not found | 指定的会话不存在 |

### Token 错误 (10500-10599)

| 错误码 | HTTP 状态 | 消息 | 说明 |
|--------|----------|------|------|
| 10501 | 404 | Token not found | 指定的 Token 不存在 |

### WebSocket 错误 (10600-10699)

| 错误码 | WS 关闭码 | 消息 | 说明 |
|--------|----------|------|------|
| 10601 | 4001 | Invalid or missing bot token | Bot Token 无效 |
| 10602 | 4003 | Token deleted | Token 被删除 |
| 10603 | 4000 | Server restarting | 服务重启 |
| 10604 | 4005 | Evicted by newer connection | 被新连接驱逐 |

### Bot 错误 (10700-10799)

| 错误码 | HTTP 状态 | 消息 | 说明 |
|--------|----------|------|------|
| 10701 | — | Unknown error from bot | Bot 返回未知错误 |

## 客户端错误处理建议

### HTTP 接口

```typescript
const resp = await fetch('/api/endpoint', {
  headers: { 'Authorization': `Bearer ${token}` }
});
const data = await resp.json();

if (data.code === 0) {
  // 成功
} else if (data.code === 10001) {
  // Token 无效，跳转登录
  redirectToLogin();
} else if (data.code === 10202) {
  // Bot 未连接，提示用户
  showError('Bot 未连接，请稍后重试');
} else {
  // 其他错误
  showError(data.error);
}
```

### WebSocket 连接

```typescript
ws.onclose = (event) => {
  const code = event.code;

  if (code === 4000) {
    // 服务重启，立即重连
    reconnect();
  } else if (code === 4001) {
    // Token 无效，停止重连
    redirectToLogin();
  } else if (code === 4003) {
    // Token 被删除
    showError('Token 已被删除');
  } else if (code === 4005) {
    // 被新连接驱逐
    showError('已有新连接，当前连接已断开');
  } else {
    // 其他错误，指数退避重连
    exponentialBackoffReconnect();
  }
};
```

## 错误码扩展指南

### 添加新错误码

1. 在 `backend/internal/model/errors.go` 中定义新的 `AppError`
2. 选择合适的错误码范围（如认证错误用 10001-10099）
3. 指定 HTTP 状态码（如果是 HTTP 错误）或 WebSocket 关闭码
4. 更新本文档和 `docs/api.md`

### 示例

```go
// 在 errors.go 中添加
ErrAuthTokenExpired = AppError{
    Code:       10006,
    HTTPStatus: http.StatusUnauthorized,
    Message:    "Token expired",
}
```

### 注意事项

- 错误码一旦分配，不应修改，以保持向后兼容
- 新增错误码应在对应范围内顺序分配
- 错误消息应简洁明确，便于客户端展示
- WebSocket 错误需同时指定应用错误码和 WS 关闭码
