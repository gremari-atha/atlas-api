package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"atlas-api/internal/config"
	"atlas-api/internal/database"
	"atlas-api/internal/middleware"
	"atlas-api/internal/redisrelay"
	"atlas-api/internal/response"
	"atlas-api/internal/scheduler"
	"atlas-api/internal/websocket"

	"atlas-api/internal/modules/account"
	"atlas-api/internal/modules/bot"
	"atlas-api/internal/modules/email"
	"atlas-api/internal/modules/emailforward"
	"atlas-api/internal/modules/logger"
	"atlas-api/internal/modules/product"
	"atlas-api/internal/modules/statistic"
	"atlas-api/internal/modules/tenant"
	"atlas-api/internal/modules/transaction"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
)

type TestPayload struct {
	Name  string `json:"name" validate:"required"`
	Age   int    `json:"age" validate:"required,gte=18"`
	Email string `json:"email" validate:"required,email"`
}

type TestResponse struct {
	Message string `json:"message"`
	Tenant  string `json:"tenant,omitempty"`
	Role    string `json:"role"`
}

func main() {
	// 1. Load Environment Config
	config.LoadEnv(".env")

	// 2. Initialize JSON Structured Logger
	// ponytail: standard logger global setup in one line, replacing winston
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})))

	slog.Info("initializing atlas monolith server...")

	// Create root context with cancel for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 3. Initialize TimescaleDB pool connection
	dbPool, err := database.InitPostgres(ctx)
	if err != nil {
		slog.Error("failed to initialize postgres pool", "err", err)
		os.Exit(1)
	}
	defer dbPool.Close()

	// 4. Initialize Redis Client connection
	redisClient, err := database.InitRedis(ctx)
	if err != nil {
		slog.Error("failed to initialize redis client", "err", err)
		os.Exit(1)
	}
	defer redisClient.Close()

	slog.Info("atlas monolith foundation initialized successfully")

	// Initialize WebSocket Hub
	wsHub := websocket.NewHub(dbPool)
	go wsHub.Run(ctx)
	slog.Info("WebSocket Hub initialized and running")

	// Start Redis Event Relay
	relay := redisrelay.NewEventRelay(redisClient, wsHub)
	relay.Start(ctx)
	slog.Info("Redis Event Relay initialized and running")

	// Initialize Asynq background worker server & client
	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		redisURL = "redis://localhost:6379/0"
	}
	workerServer, err := scheduler.NewWorkerServer(redisURL, dbPool, wsHub)
	if err != nil {
		slog.Error("failed to initialize asynq worker server", "err", err)
		os.Exit(1)
	}
	if err := workerServer.Start(); err != nil {
		slog.Error("failed to start asynq worker server", "err", err)
		os.Exit(1)
	}
	slog.Info("Asynq Worker Server started successfully")

	asynqClient, err := scheduler.NewClient(redisURL)
	if err != nil {
		slog.Error("failed to initialize asynq client", "err", err)
		os.Exit(1)
	}
	defer asynqClient.Close()

	// Initialize Email Forward background worker
	emailForwardWorker := emailforward.NewEmailForwardWorker(dbPool, wsHub)
	emailForwardWorker.Start(ctx)

	// 5. Setup HTTP Router & Middleware Router
	r := chi.NewRouter()
	r.Use(chimiddleware.RequestID)
	r.Use(chimiddleware.RealIP)
	r.Use(middleware.StructuredLogger)
	r.Use(middleware.StructuredRecoverer)
	r.Use(corsMiddleware)

	// Hello World Root Endpoint
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Hello World!"))
	})

	// Construct Auth Middleware
	superadminSecret := os.Getenv("SUPERADMIN_JWT_SECRET")
	if superadminSecret == "" {
		superadminSecret = "superadminsecret" // fallback default
	}
	auth := middleware.NewAuthMiddleware(dbPool, superadminSecret)

	// WebSocket Endpoint
	r.Get("/ws", wsHub.AuthenticateAndUpgrade)

	// Socket Task Dispatch Endpoint
	r.Route("/socket", func(r chi.Router) {
		r.Use(auth.TenantAuth)
		r.Post("/dispatch-task", wsHub.DispatchTaskHandler)
	})

	// Register Business Modules
	tenantHandler := tenant.NewTenantHandler(dbPool)
	tenantHandler.RegisterRoutes(r, auth)

	accountHandler := account.NewAccountHandler(dbPool, asynqClient)
	accountHandler.RegisterRoutes(r, auth)

	productHandler := product.NewProductHandler(dbPool)
	productHandler.RegisterRoutes(r, auth)

	transactionHandler := transaction.NewTransactionHandler(dbPool)
	transactionHandler.RegisterRoutes(r, auth)

	loggerHandler := logger.NewLoggerHandler(dbPool)
	loggerHandler.RegisterRoutes(r, auth)

	statisticHandler := statistic.NewStatisticHandler(dbPool)
	statisticHandler.RegisterRoutes(r, auth)

	emailForwardHandler := emailforward.NewEmailForwardHandler(dbPool)
	emailForwardHandler.RegisterRoutes(r)

	emailHandler := email.NewEmailHandler(dbPool, redisClient, asynqClient)
	emailHandler.RegisterRoutes(r, auth)

	botHandler := bot.NewBotHandler(dbPool, wsHub)
	botHandler.RegisterRoutes(r, auth)

	// 6. Define Test Routes for Phase 3 Middleware
	r.Route("/test", func(r chi.Router) {
		// Validasi body test
		r.With(middleware.ValidateBody[TestPayload]()).Post("/validation", func(w http.ResponseWriter, r *http.Request) {
			body := middleware.GetBody[TestPayload](r)
			slog.Info("validation test endpoint hit successfully", "payload", body)
			response.JSON(w, http.StatusOK, TestResponse{
				Message: "validation passed for " + body.Name,
			})
		})

		// Superadmin Auth test
		r.With(auth.SuperadminAuth).Get("/superadmin", func(w http.ResponseWriter, r *http.Request) {
			uCtx, _ := middleware.GetUserContext(r.Context())
			response.JSON(w, http.StatusOK, TestResponse{
				Message: "welcome superadmin",
				Role:    uCtx.Role,
			})
		})

		// Tenant Auth test
		r.With(auth.TenantAuth).Get("/tenant", func(w http.ResponseWriter, r *http.Request) {
			uCtx, _ := middleware.GetUserContext(r.Context())
			response.JSON(w, http.StatusOK, TestResponse{
				Message: "welcome tenant user",
				Tenant:  uCtx.TenantID,
				Role:    uCtx.Role,
			})
		})
	})

	// 7. Start HTTP Server
	port := os.Getenv("PORT")
	if port == "" {
		port = "5000"
	}
	server := &http.Server{
		Addr:    ":" + port,
		Handler: r,
	}

	go func() {
		slog.Info("HTTP server listening on", "port", port)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("HTTP server error", "err", err)
		}
	}()

	// 8. Graceful Shutdown Setup (Non-Blocking Signals)
	// ponytail: standard OS signal channel mapping for clean shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Block until a signal is received
	sig := <-sigChan
	slog.Info("shutdown signal received, terminating...", "signal", sig.String())

	// Give a timeout limit for graceful shutdown tasks
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	// Shutdown HTTP Server gracefully
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("HTTP server shutdown error", "err", err)
	}

	// Shutdown Asynq worker server
	workerServer.Shutdown()

	// Stop Email Forward worker
	emailForwardWorker.Stop()

	cancel() // cancel root context to notify background routines

	// Wait or force exit if timeout is exceeded
	<-shutdownCtx.Done()
	slog.Info("atlas server stopped cleanly")
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
		} else {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, PATCH, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, x-tenant-id")
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
