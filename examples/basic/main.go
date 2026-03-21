package main

import (
	"log/slog"
	"net/http"

	duckbug "github.com/duckbugio/duckbug-go"
	duckbughttp "github.com/duckbugio/duckbug-go/integrations/nethttp"
	duckbugslog "github.com/duckbugio/duckbug-go/integrations/slog"
	"github.com/duckbugio/duckbug-go/pond"
)

func main() {
	collector := pond.Ripple([]string{"password", "token"})
	duck := duckbug.NewDuck(duckbug.Config{
		Pond: collector,
		Providers: []duckbug.Provider{
			duckbug.NewDuckBugProvider("https://duckbug.io/api/ingest/<projectID>:<publicKey>"),
		},
	})

	logger := slog.New(duckbugslog.NewHandler(duck, slog.Default().Handler()))
	handler := duckbughttp.Middleware(duck)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger.InfoContext(r.Context(), "request received", "path", r.URL.Path)

		tx := duck.StartTransaction(r.Method+" "+r.URL.Path, "http.server")
		tx.SetContext(map[string]any{"route": r.URL.Path}).Finish("ok")
		duck.CaptureTransactionContext(r.Context(), tx)

		w.WriteHeader(http.StatusNoContent)
	}))

	_ = handler
}
