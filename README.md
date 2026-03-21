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
go get github.com/duckbugio/duckbug-go@v0.2.0
```

## Quick start

```go
package main

import (
    "context"
    "log"
    "os"
    "strings"

    duckbug "github.com/duckbugio/duckbug-go"
)

func main() {
    dsn := strings.TrimSpace(os.Getenv("DUCKBUG_DSN"))
    if dsn == "" {
        log.Fatal("DUCKBUG_DSN is required")
    }

    duck := duckbug.NewDuck(duckbug.Config{
        Providers: []duckbug.Provider{
            duckbug.NewDuckBugProvider(dsn),
        },
    })
    defer duck.Flush(context.Background())

    duck.Log("warning", "payment provider timeout", map[string]any{
        "provider": "stripe",
    })

    if err := doWork(); err != nil {
        duck.Quack(err)
    }
}
```

This is the smallest useful setup: the SDK only needs a DSN. Environment variable naming is up to your application; `DUCKBUG_DSN` is just the simplest convention for examples.

If you want richer context, you can optionally call `SetService(...)`, `SetEnvironment(...)`, `SetRelease(...)`, and `SetServerName(...)` in your app or wrapper.

### HTTP + logging example

```go
package main

import (
    "context"
    "log/slog"
    "net/http"

    duckbug "github.com/duckbugio/duckbug-go"
    duckbughttp "github.com/duckbugio/duckbug-go/integrations/nethttp"
    duckbugslog "github.com/duckbugio/duckbug-go/integrations/slog"
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

    logger := slog.New(duckbugslog.NewHandler(duck, slog.Default().Handler()))
    logger.Warn("checkout degraded", "provider", "stripe")

    handler := duckbughttp.Middleware(
        duck,
        duckbughttp.WithCaptureTransactions(true),
        duckbughttp.WithTransactionSampleRate(0.10),
    )(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusNoContent)
    }))

    _ = handler

    duck.Flush(context.Background())
}
```

## Branded surface

- `Duck` is the main runtime facade.
- `Quack(err)` is the branded manual error capture API.
- `pond.Ripple(...)` builds the context/sanitization subsystem.
- `Log(...)` is the canonical log capture API.
- `StartTransaction(...)` and `CaptureTransaction(...)` provide the performance/trace ingest surface.

## Privacy defaults

- Sensitive nested fields are masked with `***`.
- Sensitive header names are masked with `***`.
- Query/body/session/cookies/files/env are attached in the canonical event model and can be disabled in the DuckBug provider privacy options.
- `env` capture is disabled by default in the first-party provider.
- `net/http` middleware does not read request bodies unless you explicitly enable `WithReadBody(true)`.

## Runtime defaults

- The first-party provider uses a background queue by default, so `Log`, `Quack`, `CaptureTransaction` and `slog` bridge calls do not block the hot path on network I/O.
- The default transport is tuned for application safety: short connection timeout and no retry storm on the request path.
- `Flush(...)` waits for the provider queue and sends any buffered log/error batches, so it should be called on graceful shutdown.

## slog integration

```go
logger := slog.New(duckbugslog.NewHandler(duck, slog.Default().Handler()))
logger.Info("checkout started", "requestId", "req-123")
```

By default the SDK captures `WARN+` from `slog`. To lower the threshold:

```go
logger := slog.New(
    duckbugslog.NewHandler(
        duck,
        slog.Default().Handler(),
        duckbugslog.WithMinLevel(slog.LevelInfo),
    ),
)
```

## zap integration

```go
import (
    "os"

    duckbugzap "github.com/duckbugio/duckbug-go/integrations/zap"
    "go.uber.org/zap"
    "go.uber.org/zap/zapcore"
)

observerCore := zapcore.NewCore(
    zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()),
    zapcore.AddSync(os.Stdout),
    zapcore.InfoLevel,
)
logger := zap.New(duckbugzap.NewCore(duck, observerCore)).With(zap.String("service", "api"))
logger.Warn("checkout degraded", zap.String("provider", "stripe"))
```

## zerolog integration

```go
import (
    "os"

    duckbugzerolog "github.com/duckbugio/duckbug-go/integrations/zerolog"
    "github.com/rs/zerolog"
)

logger := zerolog.New(duckbugzerolog.NewWriter(duck, os.Stdout)).
    With().
    Timestamp().
    Str("service", "api").
    Logger()
logger.Warn().Str("provider", "stripe").Msg("checkout degraded")
```

## net/http integration

```go
handler := duckbughttp.Middleware(
    duck,
    duckbughttp.WithCaptureTransactions(true),
    duckbughttp.WithTransactionSampleRate(0.10),
)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    duck.LogContext(r.Context(), "info", "request received", map[string]any{
        "path": r.URL.Path,
    })
    w.WriteHeader(http.StatusNoContent)
}))
```

## Provider options

The first-party DuckBug provider supports:

- async non-blocking enqueue with bounded in-memory queue
- batching with explicit `Flush(...)` and periodic background flush
- single-shot `transactions` ingest
- safe-by-default transport timeouts and configurable retry/backoff
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
- `slog`, `zap`, and `zerolog` bridges
- `net/http` middleware compatible with `chi`
- schema copies and tests against the current payload contract for `errors` and `logs`
- transaction payload tests aligned with `duckbug/backend` and `duckbug-php`

## Release process

- Push a semver-style tag like `v0.2.0`.
- The release workflow re-runs module checks and creates a GitHub Release.
- Consumers install the module through the standard Go module mechanism via `go get`.
