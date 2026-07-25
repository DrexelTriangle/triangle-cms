package main

import (
	"context"
	"crypto/tls"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"server/internal/activity"
	"server/internal/auth"
	"server/internal/database"
	"server/internal/middleware"
	"server/internal/routes"

	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/joho/godotenv"

	_ "server/docs"
)

const (
	certFilePath           = "./certs/localhost.crt"
	keyFilePath            = "./certs/localhost.key"
	certFileEnv            = "TLS_CERT_FILE"
	keyFileEnv             = "TLS_KEY_FILE"
	defaultShutdownTimeout = 10 * time.Second
)

type httpsServer interface {
	ListenAndServeTLS(certFile, keyFile string) error
	Shutdown(ctx context.Context) error
	Close() error
}

type stdHTTPServer struct {
	*http.Server
}

type runDeps struct {
	loadX509KeyPair func(certFile, keyFile string) (tls.Certificate, error)
	newServer       func(cert tls.Certificate, mux *http.ServeMux, logger *slog.Logger) httpsServer
	signalCh        <-chan os.Signal
	signalNotify    func(c chan<- os.Signal, sig ...os.Signal)
	signalStop      func(c chan<- os.Signal)
	shutdownTimeout time.Duration
	oidcVerifier    *oidc.IDTokenVerifier
	oidcCfg         auth.OIDCConfig
}

// @title Triangle CMS API
// @version 1.0
// @description REST API for The Triangle newspaper CMS
// @host localhost:8080
// @BasePath /
// @schemes https
// @tag.name homepage
// @tag.name health
// @tag.name authors
// @tag.name articles
// @tag.name activity
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @description Enter: Bearer <your-token>

func main() {
	godotenv.Load(".env")

	dbName, user, password, host, port, err := dbConfigFromEnv()
	if err != nil {
		slog.Error("invalid database configuration", "error", err)
		os.Exit(1)
	}

	db, err := database.InitializeConnection(context.Background(), dbName, user, password, host, port)
	if err != nil {
		slog.Error("database initialization failed", "error", err, "host", host, "port", port, "db_name", dbName)
		os.Exit(1)
	}

	// Activity/audit log is stored in the shared MariaDB database (not an embedded
	// on-disk store), so multiple CMS instances can record and read the same
	// history without holding an exclusive directory lock.
	activityStore, err := activity.NewSQLStore(context.Background(), db)
	if err != nil {
		slog.Error("failed to initialize activity store", "error", err)
		os.Exit(1)
	}
	defer activityStore.Close()
	activity.SetDefaultStore(activityStore)

	logger := slog.New(activity.NewTeeHandler(
		slog.NewJSONHandler(os.Stdout, nil),
		activity.NewStoreHandler(activityStore, nil),
	)).With("service", "cms")
	slog.SetDefault(logger)

	if err := database.EnsureArticlesSchema(context.Background(), db); err != nil {
		slog.Error("failed to migrate articles schema", "error", err)
		os.Exit(1)
	}

	if err := database.EnsureAuthorsSchema(context.Background(), db); err != nil {
		slog.Error("failed to migrate authors schema", "error", err)
		os.Exit(1)
	}

	if err := database.EnsureUsersTable(context.Background(), db); err != nil {
		slog.Error("failed to create users table", "error", err)
		os.Exit(1)
	}
	if err := database.EnsurePollsTable(context.Background(), db); err != nil {
		slog.Error("failed to create poll table", "error", err)
		os.Exit(1)
	}

	if err := database.EnsureSessionsTable(context.Background(), db); err != nil {
		slog.Error("failed to create sessions table", "error", err)
		os.Exit(1)
	}
	if err := database.EnsureSettingsTable(context.Background(), db); err != nil {
		slog.Error("failed to create settings table", "error", err)
		os.Exit(1)
	}
	// One-time backfill of the new SEO columns from the WordPress Yoast export so
	// imported articles keep their original focus keyphrase / meta description /
	// SEO title. No-op when the export table is absent or already applied.
	if err := database.BackfillArticleSEOFromYoast(context.Background(), db); err != nil {
		slog.Error("failed to backfill article SEO from Yoast export", "error", err)
		os.Exit(1)
	}
	if err := database.EnsureTaxonomyTable(context.Background(), db); err != nil {
		slog.Error("failed to create taxonomy table", "error", err)
		os.Exit(1)
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("CMS_REBUILD_TAXONOMY_COUNTS_ON_STARTUP")), "true") {
		if err := database.RebuildTaxonomyArticleCounts(context.Background(), db); err != nil {
			slog.Error("failed to rebuild taxonomy article counts", "error", err)
			os.Exit(1)
		}
		slog.Info("rebuilt taxonomy article counts at startup")
	}
	if err := database.EnsurePollSettings(context.Background(), db); err != nil {
		slog.Error("failed to seed poll settings", "error", err)
		os.Exit(1)
	}

	row := db.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ?", dbName)
	var tableCount int
	if err := row.Scan(&tableCount); err != nil {
		slog.Error("cms startup", "event", "startup", "stage", "db_table_count", "error", err)
	} else {
		slog.Info("cms startup", "event", "startup", "stage", "db_table_count", "table_count", tableCount)
	}

	var verifier *oidc.IDTokenVerifier
	var oidcCfg auth.OIDCConfig
	if issuerURL := strings.TrimSpace(os.Getenv("OIDC_ISSUER_URL")); issuerURL != "" {
		provider, err := oidc.NewProvider(context.Background(), issuerURL)
		if err != nil {
			slog.Error("failed to initialize OIDC provider", "error", err)
			os.Exit(1)
		}
		clientID := strings.TrimSpace(os.Getenv("OIDC_CLIENT_ID"))
		verifier = provider.Verifier(&oidc.Config{
			ClientID:          clientID,
			SkipClientIDCheck: clientID == "",
		})
		frontendURL := getenvOrDefault("FRONTEND_ORIGIN", "http://localhost:5173")
		redirectURL := getenvOrDefault("OIDC_REDIRECT_URI", frontendURL+"/auth/callback")
		oidcCfg = auth.OIDCConfig{
			ClientID:     clientID,
			ClientSecret: os.Getenv("OIDC_CLIENT_SECRET"),
			AuthURL:      provider.Endpoint().AuthURL,
			TokenURL:     provider.Endpoint().TokenURL,
			RedirectURL:  redirectURL,
			FrontendURL:  frontendURL,
		}
		slog.Info("OIDC authentication enabled", "issuer", issuerURL)
	} else {
		slog.Warn("AUTH DISABLED: OIDC_ISSUER_URL not set")
		slog.Warn("AUTH DISABLED: protected routes are NOT registered; only public read routes are available")
	}

	if err := run(defaultRunDeps(verifier, oidcCfg), db); err != nil {
		slog.Error("server terminated", "error", err)
		os.Exit(1)
	}
}

func dbConfigFromEnv() (dbName, user, password, host string, port int, err error) {
	dbName = strings.TrimSpace(os.Getenv("DB_NAME"))
	user = strings.TrimSpace(os.Getenv("DB_USER"))
	password = os.Getenv("DB_PASSWORD")
	host = strings.TrimSpace(os.Getenv("DB_HOST"))
	portStr := strings.TrimSpace(os.Getenv("DB_PORT"))

	if dbName == "" {
		return "", "", "", "", 0, fmt.Errorf("DB_NAME is required")
	}
	if user == "" {
		return "", "", "", "", 0, fmt.Errorf("DB_USER is required")
	}
	if host == "" {
		host = "127.0.0.1"
	}
	if portStr == "" {
		port = 3306
		return dbName, user, password, host, port, nil
	}
	port, err = strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return "", "", "", "", 0, fmt.Errorf("DB_PORT must be a valid TCP port, got %q", portStr)
	}
	return dbName, user, password, host, port, nil
}

func defaultRunDeps(verifier *oidc.IDTokenVerifier, oidcCfg auth.OIDCConfig) runDeps {
	return runDeps{
		loadX509KeyPair: tls.LoadX509KeyPair,
		newServer:       newDefaultServer,
		signalNotify:    signal.Notify,
		signalStop:      signal.Stop,
		shutdownTimeout: defaultShutdownTimeout,
		oidcVerifier:    verifier,
		oidcCfg:         oidcCfg,
	}
}

func newDefaultServer(cert tls.Certificate, mux *http.ServeMux, logger *slog.Logger) httpsServer {
	return &stdHTTPServer{
		Server: &http.Server{
			Addr:    ":8080",
			Handler: middleware.Chain(mux, middleware.Logging, middleware.Recovery),
			TLSConfig: &tls.Config{
				Certificates: []tls.Certificate{cert},
			},
			ErrorLog: slog.NewLogLogger(logger.Handler(), slog.LevelError),
		},
	}
}

func run(deps runDeps, conn *sql.DB) error {
	if deps.loadX509KeyPair == nil {
		deps.loadX509KeyPair = tls.LoadX509KeyPair
	}
	if deps.newServer == nil {
		deps.newServer = newDefaultServer
	}
	if deps.signalNotify == nil {
		deps.signalNotify = signal.Notify
	}
	if deps.signalStop == nil {
		deps.signalStop = signal.Stop
	}
	if deps.shutdownTimeout <= 0 {
		deps.shutdownTimeout = defaultShutdownTimeout
	}

	certPath := getenvOrDefault(certFileEnv, certFilePath)
	keyPath := getenvOrDefault(keyFileEnv, keyFilePath)

	cert, err := deps.loadX509KeyPair(certPath, keyPath)
	if err != nil {
		return fmt.Errorf("tls certificate load failed: %w", err)
	}

	mux := http.NewServeMux()
	routes.Register(mux, conn, deps.oidcVerifier, deps.oidcCfg)
	server := deps.newServer(cert, mux, slog.Default())

	serverErr := make(chan error, 1)
	go func() {
		err := server.ListenAndServeTLS("", "")
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

	stop := deps.signalCh
	if stop == nil {
		ch := make(chan os.Signal, 1)
		deps.signalNotify(ch, syscall.SIGINT, syscall.SIGTERM)
		defer deps.signalStop(ch)
		stop = ch
	}

	select {
	case err := <-serverErr:
		if err != nil {
			return fmt.Errorf("https server exited with error: %w", err)
		}
		return nil
	case <-stop:
	}

	ctx, cancel := context.WithTimeout(context.Background(), deps.shutdownTimeout)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		if closeErr := server.Close(); closeErr != nil {
			slog.Error("forced close failed", "error", closeErr)
		}
		return fmt.Errorf("graceful shutdown failed: %w", err)
	}

	if err := <-serverErr; err != nil {
		return fmt.Errorf("https server exited with error: %w", err)
	}

	return nil
}

func getenvOrDefault(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}
