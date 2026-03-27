# MySQL DDL-Aware Migration Design

## Context

The Go backend currently performs two schema-changing startup actions unconditionally:

1. `ensureDatabase()` executes `CREATE DATABASE IF NOT EXISTS`.
2. `RunMigrations()` executes embedded SQL migrations with `golang-migrate`.

In production, the application account does not have DDL privileges. Schema changes are applied separately before the service is upgraded. Startup must therefore continue when the account lacks DDL privileges.

## Goals

- Detect whether the current MySQL account has sufficient DDL privileges for the target database.
- Skip startup DDL actions when privileges are missing.
- Preserve existing failure behavior for real environment problems, such as a missing database or unexpected privilege-detection errors.

## Design

### Privilege Detection

Use `SHOW GRANTS FOR CURRENT_USER()` and parse the returned grant strings.

Treat the account as DDL-capable when either:

- it has `ALL PRIVILEGES` on `*.*` or on the target database, or
- it has all of `CREATE`, `ALTER`, `DROP`, and `INDEX` on `*.*` or on the target database.

If privilege detection itself fails, return an error and keep startup failure behavior unchanged.

### Database Creation

`ensureDatabase()` checks DDL capability before opening the server-level DSN.

- With DDL privilege: keep current `CREATE DATABASE IF NOT EXISTS` behavior.
- Without DDL privilege: log a warning and skip database creation.

If the database does not already exist, the later application connection still fails. This remains an environment error.

### Migration Execution

`RunMigrations()` checks DDL capability before constructing migration state.

- With DDL privilege: keep current migration flow, including Redis locking.
- Without DDL privilege: log a warning and return `nil`.

Skipping migrations must happen before touching Redis migration state.

## Testing

- Add unit tests for parsing `SHOW GRANTS` output.
- Add unit tests proving `ensureDatabase()` skips opening a server-level connection when DDL is unavailable.
- Add unit tests proving `RunMigrations()` returns success immediately when DDL is unavailable.
