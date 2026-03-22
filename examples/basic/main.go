package main

import (
	"log/slog"
	"net/http"

	duckbug "github.com/duckbugio/duckbug-go"
	duckbughttp "github.com/duckbugio/duckbug-go/integrations/nethttp"
	duckbugslog "github.com/duckbugio/duckbug-go/integrations/slog"
)

func main() {
	duck := duckbug.New(
		"https://duckbug.io/api/ingest/<projectID>:<publicKey>",
		duckbug.WithSensitiveFields("password", "token"),
		duckbug.WithEnvironment("production"),
		duckbug.WithService("example-api"),
	)

	logger := slog.New(duckbugslog.NewHandler(duck, slog.Default().Handler()))
	handler := duckbughttp.Middleware(duck, duckbughttp.WithProductionDefaults())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger.InfoContext(r.Context(), "request received", "path", r.URL.Path)

		w.WriteHeader(http.StatusNoContent)
	}))

	_ = handler
}
