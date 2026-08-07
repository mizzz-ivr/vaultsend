package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/example/vaultsend/internal/config"
	apphttp "github.com/example/vaultsend/internal/http"
	"github.com/example/vaultsend/internal/queue"
	"github.com/example/vaultsend/internal/service"
	"github.com/example/vaultsend/internal/storage"
	"github.com/example/vaultsend/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}
	auditCfg, err := config.LoadSecurityAuditConfig(cfg.AppEnv)
	if err != nil {
		log.Fatalf("failed to load security audit config: %v", err)
	}
	if len(cfg.AccessGrantSecret) < 32 {
		log.Fatal("ACCESS_GRANT_SECRET must be at least 32 bytes for the API process")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}
	defer pool.Close()

	awsCfg, err := awscfg.LoadDefaultConfig(ctx, awscfg.WithRegion(cfg.AWSRegion))
	if err != nil {
		log.Fatalf("failed to load aws config: %v", err)
	}

	baseQueries := store.New(pool)
	queries := store.NewTransactionalAuditQueries(baseQueries)
	auditSvc := &service.SecurityAuditService{
		Store:      queries,
		HMACSecret: auditCfg.HMACSecret,
		HMACKeyID:  auditCfg.HMACKeyID,
	}
	s3Store := storage.NewS3ObjectStore(s3.NewFromConfig(awsCfg))
	uploadSvc := &service.UploadService{
		Store:               queries,
		ObjectStore:         s3Store,
		S3Bucket:            cfg.S3Bucket,
		PartSizeBytes:       cfg.UploadPartSize,
		UploadURLTTL:        cfg.UploadURLTTL,
		UploadSessionTTL:    cfg.UploadURLTTL,
		MaxFileSizeBytes:    cfg.UploadMaxFileSize,
		MaxPresignedPartNum: cfg.UploadMaxParts,
	}
	stripeClient := &service.StripeClient{
		SecretKey:     cfg.StripeSecretKey,
		WebhookSecret: cfg.StripeWebhookSecret,
		PriceIDPro:    cfg.StripePriceIDPro,
	}
	billingSvc := &service.BillingService{Store: queries, Stripe: stripeClient, FrontendURL: cfg.FrontendURL}
	uploadSvc.Billing = billingSvc

	mailQueue := queue.NewSQSQueue(sqs.NewFromConfig(awsCfg), cfg.SQSQueueURL)
	orgSvc := &service.OrgService{
		Store:           queries,
		Billing:         billingSvc,
		InvitationStore: queries,
		Queue:           mailQueue,
		FrontendURL:     cfg.FrontendURL,
	}
	shipmentSvc := &service.ShipmentService{Store: queries, Queue: mailQueue, FrontendURL: cfg.FrontendURL, Billing: billingSvc, Org: orgSvc}
	guard := service.NewAccessGuard()
	guard.VerifyMaxAttempts = cfg.VerifyMaxAttempts
	guard.DownloadLimit = cfg.DownloadRateLimit

	authSvc := &service.AuthService{
		Store:      queries,
		SessionTTL: time.Duration(cfg.SessionTTLHours) * time.Hour,
	}

	accessSvc := &service.AccessService{
		Store:             queries,
		ObjectStore:       s3Store,
		DownloadURLTTL:    cfg.PresignedURLTTL,
		AccessGrantTTL:    cfg.AccessGrantTTL,
		AccessGrantSecret: cfg.AccessGrantSecret,
		Guard:             guard,
	}

	handler := apphttp.NewServer(cfg, baseQueries, uploadSvc, shipmentSvc, accessSvc, authSvc, billingSvc, orgSvc, auditSvc)
	server := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           handler,
		ReadHeaderTimeout: cfg.HTTPReadHeaderTimeout,
		ReadTimeout:       cfg.HTTPReadTimeout,
		WriteTimeout:      cfg.HTTPWriteTimeout,
		IdleTimeout:       cfg.HTTPIdleTimeout,
		MaxHeaderBytes:    cfg.HTTPMaxHeaderBytes,
	}

	go func() {
		log.Printf("api server starting env=%s port=%s", cfg.AppEnv, cfg.Port)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("http server crashed: %v", err)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
	}
}
