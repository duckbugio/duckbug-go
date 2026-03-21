# duckbug-go

Go SDK for DuckBug `errors`, `logs`, and `transactions`.

This module follows the DuckBug SDK architecture from `duckbug-sdk-spec`, while using the real ingest contract from `duckbug/backend`:

- `POST /ingest/{projectID}:{key}/errors`
- `POST /ingest/{projectID}:{key}/errors/batch`
- `POST /ingest/{projectID}:{key}/logs`
- `POST /ingest/{projectID}:{key}/logs/batch`
- `POST /ingest/{projectID}:{key}/transactions`

## Compatibility

- Go `1.25.8+` is the initial compatibility target.
- CI validates the module on Go `1.25.8` and `1.26.1`.
- Releases are expected to be published via git tags in the `v*` format.

## Install

```bash
go get github.com/duckbugio/duckbug-go
```

To install a tagged release:

```bash
go get github.com/duckbugio/duckbug-go@v0.1.0
```

## Quick start

```go
package main

import (
    duckbug "github.com/duckbugio/duckbug-go"
    "github.com/duckbugio/duckbug-go/pond"
)

func main() {
    duck := duckbug.NewDuck(duckbug.Config{
        Pond: pond.Ripple([]string{"password", "token"}),
        Providers: []duckbug.Provider{
            duckbug.NewDuckBugProvider("https://duckbug.io/api/ingest/<projectID>:<publicKey>"),
        },
    })

    duck.SetEnvironment("production")
    duck.SetRelease("checkout@1.2.3")

    duck.CaptureLog("warning", "Payment provider timeout", map[string]any{
        "provider": "stripe",
        "attempt":  2,
    })

    transaction := duck.StartTransaction("POST /checkout", "http.server")
    transaction.
        SetContext(map[string]any{"route": "/checkout"}).
        AddMeasurement("http.response.status_code", 200, "code").
        Finish("ok")
    duck.CaptureTransaction(transaction)

    if err := doWork(); err != nil {
        duck.Quack(err)
    }

    duck.Flush(nil)
}
```

## Branded surface

- `Duck` is the main runtime facade.
- `Quack(err)` is the branded manual error capture API.
- `pond.Ripple(...)` builds the context/sanitization subsystem.
- `StartTransaction(...)` and `CaptureTransaction(...)` provide the performance/trace ingest surface.

## Privacy defaults

- Sensitive nested fields are masked with `***`.
- Sensitive header names are masked with `***`.
- Query/body/session/cookies/files/env are attached in the canonical event model and can be disabled in the DuckBug provider privacy options.
- `env` capture is disabled by default in the first-party provider.

## slog integration

```go
logger := slog.New(duckbugslog.NewHandler(duck, slog.Default().Handler()))
logger.Info("checkout started", "requestId", "req-123")
```

## net/http integration

```go
handler := duckbughttp.Middleware(duck)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    duck.CaptureLogContext(r.Context(), "info", "request received", map[string]any{
        "path": r.URL.Path,
    })
    w.WriteHeader(http.StatusNoContent)
}))
```

## Provider options

The first-party DuckBug provider supports:

- batching with explicit `Flush(...)`
- single-shot `transactions` ingest
- bounded retry/backoff for network errors, `429`, and `5xx`
- `beforeSend` mutation/drop hook
- transport failure hook
- privacy controls for request sections and `env`

## Transactions

Transactions mirror the current DuckBug backend and PHP SDK shape:

- required payload fields: `traceId`, `spanId`, `transaction`, `op`, `startTime`, `endTime`, `duration`
- optional fields: `eventId`, `parentSpanId`, `status`, `context`, `measurements`, `spans`, `release`, `environment`, `dist`, `platform`, `serverName`, `service`, `requestId`, `user`, `sdk`, `runtime`, `extra`
- duplicate transaction ingest is treated as idempotent success by the backend

Example:

```go
tx := duck.StartTransaction("GET /checkout", "http.server")

span := tx.StartChild("db.query", "select order")
span.SetData(map[string]any{
    "sql": "select * from orders where id = ?",
})
span.Finish("ok")

tx.AddMeasurement("db.rows", 1, "")
tx.Finish("ok")

duck.CaptureTransaction(tx)
```

## Current status

Implemented in this iteration:

- core runtime for `errors`, `logs`, and `transactions`
- branded `Duck` / `Quack` / `pond.Ripple`
- first-party DuckBug provider
- `slog` bridge
- `net/http` middleware compatible with `chi`
- schema copies and tests against the current payload contract for `errors` and `logs`
- transaction payload tests aligned with `duckbug/backend` and `duckbug-php`

## Release process

- Push a semver-style tag like `v0.1.0`.
- The release workflow re-runs module checks and creates a GitHub Release.
- Consumers install the module through the standard Go module mechanism via `go get`.
