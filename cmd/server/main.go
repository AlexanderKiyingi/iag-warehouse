package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alvor-technologies/iag-platform-go/authclient"
	platformotel "github.com/alvor-technologies/iag-platform-go/otel"

	"iag-warehouse/backend/internal/auditlog"
	"iag-warehouse/backend/internal/config"
	"iag-warehouse/backend/internal/consumer"
	"iag-warehouse/backend/internal/db"
	"iag-warehouse/backend/internal/events"
	"iag-warehouse/backend/internal/handlers"
	"iag-warehouse/backend/internal/middleware"
	"iag-warehouse/backend/internal/migrate"
	"iag-warehouse/backend/internal/outbox"
	"iag-warehouse/backend/internal/store"
)

func main() {
	ctx := context.Background()
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	// OpenTelemetry → otel-collector:4317 (non-blocking dial).
	if tp, err := platformotel.Init(ctx, platformotel.Config{
		ServiceName: cfg.ServiceName,
		Environment: cfg.Environment,
	}); err != nil {
		log.Printf("otel disabled: %v", err)
	} else {
		defer func() {
			sc, c := context.WithTimeout(context.Background(), 5*time.Second)
			defer c()
			_ = tp.Shutdown(sc)
		}()
	}

	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer pool.Close()

	if cfg.AutoMigrate {
		if err := migrate.Up(ctx, pool); err != nil {
			log.Fatalf("migrate: %v", err)
		}
	}

	st := store.New(pool)
	auditStore := auditlog.NewStore(pool)
	outboxStore := outbox.NewStore(pool)

	bus := events.New(events.Config{
		Brokers: cfg.KafkaBrokers,
		Enabled: cfg.EventBusEnabled && len(cfg.KafkaBrokers) > 0,
	})
	bus.SetOutbox(outboxStore)
	st.SetEventBus(bus)
	st.SetCosting(cfg.InventoryCostingEnabled, cfg.BaseCurrency)
	defer bus.Close()

	if bus.Enabled() {
		pub := outbox.NewPublisher(outboxStore, bus)
		go pub.Run(ctx)
	}

	var verifier *authclient.Verifier
	if cfg.AuthMode == "jwt" {
		verifier = authclient.NewVerifier(authclient.Options{
			JWKSURL:  cfg.JWKSURL,
			Issuer:   cfg.JWTIssuer,
			Audience: cfg.Audience,
		})
		// Never crash-loop the whole service on a transient JWKS hiccup at boot:
		// retry briefly, then degrade to the background refresh loop below. Until
		// keys load, tokens fail closed (401) — far better than a 503 outage.
		if err := refreshJWKSWithRetry(ctx, verifier); err != nil {
			log.Printf("warning: initial jwks refresh failed after retries; continuing with background refresh: %v", err)
		}
		go jwksRefreshLoop(verifier)
	}

	platformAuth := middleware.NewPlatformAuth(middleware.PlatformAuthOptions{
		Mode:     cfg.AuthMode,
		Verifier: verifier,
	})

	if cfg.AuthMode == "jwt" && cfg.ServiceClientSecret != "" {
		go registerPermissionsLoop(ctx, cfg)
	} else if cfg.AuthMode == "jwt" {
		log.Printf("warehouse: SERVICE_CLIENT_SECRET unset — skipping permissions registration")
	}

	if len(cfg.KafkaBrokers) > 0 {
		kc := consumer.New(consumer.Config{
			Brokers:          cfg.KafkaBrokers,
			GroupID:          cfg.KafkaConsumerGroup,
			CommercialTopic:  cfg.KafkaCommercialTopic,
			ProductionTopic:  cfg.KafkaProductionTopic,
			QualityTopic:     cfg.KafkaQualityTopic,
			OperationsTopic:  cfg.KafkaOperationsTopic,
			SupplyChainTopic: cfg.KafkaSupplyChainTopic,
		}, st)
		go func() {
			if err := kc.Run(ctx); err != nil {
				log.Printf("kafka consumer stopped: %v", err)
			}
		}()
	}

	api := &handlers.API{Cfg: cfg, Store: st, Audit: auditStore, Bus: bus}
	router := handlers.NewRouter(handlers.RouterDeps{
		API:          api,
		Audit:        auditStore,
		PlatformAuth: platformAuth,
		CORSOrigins:  cfg.CORSOrigins,
		StrictRBAC:   cfg.StrictRBAC(),
	})

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		log.Printf("warehouse listening on :%s (aud=%s)", cfg.Port, cfg.Audience)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

// refreshJWKSWithRetry does a bounded set of boot-time refresh attempts with a
// short linear backoff. On persistent failure it returns the last error so the
// caller can degrade to the background refresh loop instead of exiting.
func refreshJWKSWithRetry(ctx context.Context, v *authclient.Verifier) error {
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		err = v.Refresh(c)
		cancel()
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(time.Duration(attempt+1) * 2 * time.Second):
		}
	}
	return err
}

func jwksRefreshLoop(v *authclient.Verifier) {
	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := v.Refresh(ctx); err != nil {
			log.Printf("jwks refresh: %v", err)
		}
		cancel()
	}
}
