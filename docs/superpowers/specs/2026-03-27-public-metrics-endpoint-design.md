# Public Metrics Endpoint Design

## Context

The backend exposes Prometheus metrics at `GET /api/metrics`.

Global token middleware already treats `/api/metrics` as a public path, but the route handler still performs its own admin session or bearer-token validation. That causes Prometheus scrapes to receive `401 Unauthorized`.

`DELETE /api/metrics` is an administrative action and should remain protected.

## Goals

- Allow Prometheus to fetch `GET /api/metrics` without any authentication.
- Keep `DELETE /api/metrics` behind the existing admin-session bearer-token check.
- Limit the change to the metrics route behavior only.

## Design

### GET /api/metrics

Remove the manual admin auth check from `getMetrics`.

The handler should only:

- render Prometheus exposition text from Redis-backed telemetry, and
- return the existing Prometheus content type and error handling.

### DELETE /api/metrics

Keep the current bearer-token validation flow unchanged so only an authenticated admin session can reset metrics.

### Testing

Add a route test that proves:

- unauthenticated `GET /api/metrics` returns `200 OK`, and
- unauthenticated `DELETE /api/metrics` still returns `401 Unauthorized`.
