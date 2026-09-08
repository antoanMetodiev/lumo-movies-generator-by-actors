// Command server is the Go rewrite of lumo-movies-generator-by-actors
// (originally Spring Boot). Endpoint paths, params, and behavior mirror the
// Java service exactly — see internal/generator for the per-file notes on
// where behavior was intentionally preserved (including the semaphore-
// limited concurrency in AddMoviesByActor and the off-by-one cast-list cap)
// vs. where a Java-only implementation detail was replaced with a plain,
// equivalent SQL statement.
//
// Unlike lumo-movies-svc/lumo-series-svc, this service has no
// SecurityConfig/GatewayAuthenticationFilter in the Java source at all — it
// was never meant to be called through lumo-api-gateway with a Clerk
// session, so there's no auth middleware here either.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"lumo-movies-generator-by-actors/internal/actors"
	"lumo-movies-generator-by-actors/internal/config"
	"lumo-movies-generator-by-actors/internal/dbx"
	"lumo-movies-generator-by-actors/internal/generator"
	"lumo-movies-generator-by-actors/internal/metrics"
	"lumo-movies-generator-by-actors/internal/reqlog"
	"lumo-movies-generator-by-actors/internal/tmdb"
)

func main() {
	cfg := config.Load()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := dbx.Dial(ctx, cfg.DatabaseURL, cfg.DBUsername, cfg.DBPassword)
	if err != nil {
		log.Fatalf("database connection failed: %v", err)
	}
	defer pool.Close()

	// Was missing here — every sibling service (lumo-movies-svc,
	// lumo-series-svc, lumo-movies-generator, lumo-payments-svc,
	// lumo-user-svc) already starts this; this one dialed a pool and never
	// pinged it again, so its share of the (shared, with lumo-movies-svc)
	// movies database went unpinged by this instance specifically whenever
	// it was the only one awake.
	dbx.StartKeepAlivePing(ctx, pool, log.Printf)

	shutdownMetrics, err := metrics.Start(ctx, cfg.GrafanaEnabled, cfg.GrafanaEndpoint, cfg.GrafanaAuth)
	if err != nil {
		log.Printf("metrics exporter did not start: %v", err)
		shutdownMetrics = func(context.Context) error { return nil }
	}
	defer shutdownMetrics(context.Background())

	tmdbClient := tmdb.New(cfg.TMDBBaseURL, cfg.TMDBAPIKey)

	actorsRepo := actors.NewRepository(pool)
	genRepo := generator.NewRepository(pool)
	genSvc := generator.NewService(tmdbClient, genRepo, actorsRepo)
	genHandlers := generator.NewHandlers(genSvc)

	mux := http.NewServeMux()
	genHandlers.Register(mux)
	// management.endpoints.web.exposure.include=health — Render's own health
	// check and any uptime monitor hit this. The Java source has no
	// heartbeat/@Scheduled keep-alive class in this particular service.
	mux.HandleFunc("GET /actuator/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"UP"}`))
	})
	// Plain, non-legacy-named alias of the endpoint above -- this is what
	// lumo-api-gateway's heartbeat.StartFleetHealthPing pings on its own
	// 2-minute schedule to keep every downstream service warm on Render's
	// free tier, independent of an end user ever hitting this service.
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"UP"}`))
	})

	handler := reqlog.Middleware(mux)

	server := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		log.Printf("lumo-movies-generator-by-actors (Go) listening on :%s", cfg.Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
	}
}
