package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	"github.com/example/vaultsend/internal/config"
	"github.com/example/vaultsend/internal/store"
	"github.com/example/vaultsend/internal/worker"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	cfg, err := config.LoadSecurityAuditOutboxConfig()
	if err != nil {
		log.Fatalf("failed to load security audit outbox config: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}
	defer pool.Close()

	outboxWorker := &worker.SecurityAuditOutboxWorker{
		Store:            store.New(pool),
		Interval:         cfg.PollInterval,
		BatchSize:        cfg.BatchSize,
		Retention:        cfg.Retention,
		CleanupBatchSize: cfg.CleanupBatchSize,
	}
	log.Printf(
		"security audit outbox worker starting interval=%s batch_size=%d retention=%s cleanup_batch_size=%d",
		cfg.PollInterval,
		cfg.BatchSize,
		cfg.Retention,
		cfg.CleanupBatchSize,
	)
	if err := outboxWorker.Run(ctx); err != nil {
		log.Fatalf("security audit outbox worker stopped with error: %v", err)
	}
	log.Printf("security audit outbox worker stopped")
}
