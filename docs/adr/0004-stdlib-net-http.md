# 0004. net/http standard library for routing

Date: 2026-05-01

## Status

Accepted

## Context

The API exposes eight endpoints with method-specific routing (`GET /stocks`, `POST /stocks`, `POST /chaos`, etc.) and two path parameters (`{wallet_id}`, `{stock_name}`). Go 1.22 added method-aware route patterns and named path parameters to the stdlib `http.ServeMux`, removing the historical reason most Go HTTP services pull in a third-party router.

The alternatives considered:

- **chi.** Lightweight, similar mental model to stdlib, but still one external dependency to track for CVEs and one extra API surface for a future maintainer to learn before reading the routes.
- **gin / echo.** Bring middleware chains and binding helpers, at the cost of an idiomatic Go shape: their `Context` is what handlers sign up for, not `http.Handler`. For an API this small most of the imported behaviour goes unused.
- **gorilla/mux.** The historical default. Maintenance was sporadic for years; the project was archived in late 2022 and unarchived later under new maintainers, which is not a foundation to lean on.

## Decision

Use `net/http` directly. Routes are registered on a single `http.ServeMux` in `internal/api/server.go` using the Go 1.22+ pattern syntax (`s.mux.HandleFunc("GET /wallets/{wallet_id}", s.handleGetWallet)`). The `Server` struct implements `http.Handler` by delegating to its mux, which keeps composition (middleware, test wrapping) at the level of the standard interface.

## Consequences

- Zero third-party HTTP routing dependencies. `go.mod` is shorter and the supply-chain surface is smaller, which matters when every dependency is one more transitive tree to keep an eye on.
- Wrong-method requests such as `PUT /stocks` return `405 Method Not Allowed` automatically because the patterns are method-scoped; no extra code or test path required.
- There is no built-in middleware chain. The current code does not need one: structured logging happens in the service layer and error mapping happens in the handler. If middleware is added later, the standard `func(http.Handler) http.Handler` decorator pattern is sufficient and the existing `ServeHTTP` is the natural point to plug it in.
- There is no body-binding helper. Handlers decode with `json.NewDecoder(r.Body).Decode(&req)` and rely on service-layer validation, which is a few mechanical lines per handler in exchange for not learning a binding DSL.
