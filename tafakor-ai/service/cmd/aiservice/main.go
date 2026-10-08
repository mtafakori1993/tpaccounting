// Command aiservice runs the Tafakor AI assistant backend.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/tafakorpardazan/tafakor-ai/internal/accounting"
	"github.com/tafakorpardazan/tafakor-ai/internal/config"
	"github.com/tafakorpardazan/tafakor-ai/internal/httpapi"
	"github.com/tafakorpardazan/tafakor-ai/internal/provider"
	"github.com/tafakorpardazan/tafakor-ai/internal/store"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%s/ai-api/healthz", envOr("AI_PORT", "8090")))
		if err != nil || resp.StatusCode != 200 {
			os.Exit(1)
		}
		return
	}
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	st, err := store.OpenSQLite(filepath.Join(cfg.DataDir, "tafakor-ai.db"))
	if err != nil {
		return err
	}
	defer st.Close()
	ai, err := provider.NewOpenRouter(cfg.LLM.BaseURL, cfg.LLM.APIKey, cfg.LLM.Model, cfg.LLM.ProxyURL, cfg.NoProxy, cfg.LLM.Timeout)
	if err != nil {
		return err
	}
	ai.Fallbacks = cfg.FallbackModels
	acc := accounting.New(cfg.AccountingURL)
	srv := httpapi.New(cfg, st, acc, ai)
	wire(srv, cfg, st, acc, ai)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go retention(ctx, st, cfg.AuditRetention)

	hs := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", cfg.Bind, cfg.Port),
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       2 * time.Minute,
		// No global WriteTimeout: assistant answers stream over SSE; handlers bound their own work.
		IdleTimeout: 2 * time.Minute,
	}
	errCh := make(chan error, 1)
	go func() {
		slog.Info("tafakor-ai listening", "addr", hs.Addr, "accounting", cfg.AccountingURL, "model", cfg.LLM.Model,
			"proxy", config.RedactURL(cfg.LLM.ProxyURL), "key_configured", cfg.LLM.APIKey != "")
		if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return hs.Shutdown(sctx)
}

// retention purges audit rows (and expired tokens/idempotency keys) older than the retention period, daily.
func retention(ctx context.Context, st store.Repository, keep time.Duration) {
	t := time.NewTicker(24 * time.Hour)
	defer t.Stop()
	for {
		if n, err := st.PurgeAudit(ctx, time.Now().Add(-keep)); err == nil && n > 0 {
			slog.Info("audit retention", "purged", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
