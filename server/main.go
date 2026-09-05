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
	"server/internal/akismet"
	"server/internal/auth"
	"server/internal/database"
	"server/internal/embeddings"
	"server/internal/handlers"
	"server/internal/middleware"
	"server/internal/routes"
	"server/internal/slack"

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
	serverModeEnv          = "CMS_SERVER_MODE"
	serverModeHTTPS        = "https"
	serverModeInternalHTTP = "internal-http"
	akismetAPIKeyEnv       = "AKISMET_API_KEY"
	akismetBlogURLEnv      = "AKISMET_BLOG_URL"
	slackWebhookURLEnv     = "SLACK_WEBHOOK_URL"
	slackSigningSecretEnv  = "SLACK_SIGNING_SECRET"
	slackQueueURLEnv       = "SLACK_CLASSIFIEDS_QUEUE_URL"
	defaultShutdownTimeout = 10 * time.Second
)

type httpsServer interface {
	ListenAndServe() error
	ListenAndServeTLS(certFile, keyFile string) error
	Shutdown(ctx context.Context) error
	Close() error
}

type stdHTTPServer struct {
	*http.Server
}

type runDeps struct {
	loadX509KeyPair func(certFile, keyFile string) (tls.Certificate, error)
	newServer       func(cert *tls.Certificate, mux *http.ServeMux, logger *slog.Logger) httpsServer
	signalCh        <-chan os.Signal
	signalNotify    func(c chan<- os.Signal, sig ...os.Signal)
	signalStop      func(c chan<- os.Signal)
	shutdownTimeout time.Duration
	oidcVerifier    *oidc.IDTokenVerifier
	oidcCfg         auth.OIDCConfig
	spamChecker     akismet.Checker
	slackNotifier   slack.Notifier
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

	// Share activity history across blue/green instances through MariaDB.
	activityStore, err := activity.NewSQLStore(context.Background(), db)
	if err != nil {
		slog.Error("failed to initialize activity store", "error", err)
		os.Exit(1)
	}
	activity.SetDefaultStore(activityStore)

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "cms")
	slog.SetDefault(logger)

	if err := database.EnsureArticlesSchema(context.Background(), db); err != nil {
		slog.Error("failed to migrate articles schema", "error", err)
		os.Exit(1)
	}

	// Optional indexes improve performance without changing query results.
	if err := database.EnsureArticlesPublishedIndex(context.Background(), db); err != nil {
		slog.Error("failed to build article pub_date index; public listings filesort", "error", err)
	}

	if err := database.EnsureArticlesSlugIndex(context.Background(), db); err != nil {
		slog.Error("failed to index article slugs; article lookups scan the table", "error", err)
	}
	if err := database.EnsureArticleAuthorsIndex(context.Background(), db); err != nil {
		slog.Error("failed to index article authors; byline lookups scan the join table", "error", err)
	}
	if err := database.EnsureArticlesBreakingNewsIndex(context.Background(), db); err != nil {
		slog.Error("failed to index breaking-news articles; the homepage banner lookup scans the table", "error", err)
	}

	// Search falls back to LIKE if FULLTEXT index creation fails.
	if err := database.EnsureArticlesSearchIndex(context.Background(), db); err != nil {
		slog.Error("failed to build article search index; search falls back to LIKE", "error", err)
	}

	// Semantic search is optional; VECTOR columns require MariaDB 11.7+.
	if err := database.EnsureArticleEmbeddingsTable(context.Background(), db); err != nil {
		slog.Error("failed to create article embeddings table; search stays lexical", "error", err)
	}

	if err := database.EnsureAuthorsSchema(context.Background(), db); err != nil {
		slog.Error("failed to migrate authors schema", "error", err)
		os.Exit(1)
	}
	if err := database.EnsureCommentsTable(context.Background(), db); err != nil {
		slog.Error("failed to create comments table", "error", err)
		os.Exit(1)
	}
	if err := database.EnsureMediaTable(context.Background(), db); err != nil {
		slog.Error("failed to create media table", "error", err)
		os.Exit(1)
	}

	// Warn on a missing media mount; upload handlers reject writes independently.
	if err := handlers.CheckMediaStorage(); err != nil {
		slog.Error("media storage check failed; uploads will be refused", "error", err)
	}

	if err := database.EnsureUsersTable(context.Background(), db); err != nil {
		slog.Error("failed to create users table", "error", err)
		os.Exit(1)
	}
	if err := database.EnsurePollsTable(context.Background(), db); err != nil {
		slog.Error("failed to create poll table", "error", err)
		os.Exit(1)
	}
	if err := database.EnsurePollsSchema(context.Background(), db); err != nil {
		slog.Error("failed to create poll archive tables", "error", err)
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
	// Preserve imported Yoast metadata once, when the export table exists.
	if err := database.BackfillArticleSEOFromYoast(context.Background(), db); err != nil {
		slog.Error("failed to backfill article SEO from Yoast export", "error", err)
		os.Exit(1)
	}
	// Resolve Yoast templates on every startup to cover databases backfilled earlier.
	if expanded, err := database.ExpandYoastTitleTemplates(context.Background(), db); err != nil {
		slog.Error("failed to expand Yoast SEO title templates", "error", err)
		os.Exit(1)
	} else if expanded > 0 {
		slog.Info("expanded Yoast SEO title templates", "articles", expanded)
	}
	if err := database.EnsureTaxonomyTable(context.Background(), db); err != nil {
		slog.Error("failed to create taxonomy table", "error", err)
		os.Exit(1)
	}
	if err := database.EnsureClassifiedsTable(context.Background(), db); err != nil {
		slog.Error("failed to create classifieds table", "error", err)
		os.Exit(1)
	}

	// Required for section queries. Rebuild every startup because ETL reseeds
	// replace and renumber articles, invalidating the derived category table.
	if err := database.EnsureArticleCategoriesTable(context.Background(), db); err != nil {
		slog.Error("failed to create article categories index", "error", err)
		os.Exit(1)
	}
	if err := database.RebuildArticleCategories(context.Background(), db); err != nil {
		slog.Error("failed to rebuild article categories index", "error", err)
		os.Exit(1)
	}

	if strings.EqualFold(strings.TrimSpace(os.Getenv("CMS_REBUILD_TAXONOMY_COUNTS_ON_STARTUP")), "true") {
		if err := database.RebuildTaxonomyArticleCounts(context.Background(), db); err != nil {
			slog.Error("failed to rebuild taxonomy article counts", "error", err)
			os.Exit(1)
		}
		slog.Info("rebuilt taxonomy article counts at startup")

		// Diagnostic, so a failure here must not keep the server down.
		if err := database.ReportOrphanedArticles(context.Background(), db); err != nil {
			slog.Error("failed to check for articles matching no section", "error", err)
		}
	}
	// Runs after the settings table exists, since the legacy poll question lives
	// in cms_settings. No-op once cms_polls has any row.
	if err := database.MigrateLegacyPoll(context.Background(), db); err != nil {
		slog.Error("failed to migrate legacy poll", "error", err)
		os.Exit(1)
	}

	row := db.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ?", dbName)
	var tableCount int
	if err := row.Scan(&tableCount); err != nil {
		slog.Error("cms startup", "event", "startup", "stage", "db_table_count", "error", err)
	} else {
		slog.Info("cms startup", "event", "startup", "stage", "db_table_count", "table_count", tableCount)
	}

	// Provision schema without requiring OIDC or TLS configuration.
	if strings.TrimSpace(os.Getenv("CMS_MIGRATE_ONLY")) == "1" {
		slog.Info("cms startup", "event", "startup", "stage", "migrate_only_complete", "table_count", tableCount)
		return
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

	spamChecker, err := akismetCheckerFromEnv()
	if err != nil {
		slog.Error("invalid Akismet configuration", "error", err)
		os.Exit(1)
	}
	slackNotifier, err := slackNotifierFromEnv()
	if err != nil {
		slog.Error("invalid Slack configuration", "error", err)
		os.Exit(1)
	}
	logSlackModerationState(slackNotifier)

	if spamChecker == nil {
		slog.Warn("Akismet comment spam filtering disabled: AKISMET_API_KEY not set")
	} else {
		slog.Info("Akismet comment spam filtering enabled")
	}

	if err := run(defaultRunDeps(verifier, oidcCfg, spamChecker, slackNotifier), db); err != nil {
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

func defaultRunDeps(verifier *oidc.IDTokenVerifier, oidcCfg auth.OIDCConfig, spamChecker akismet.Checker, slackNotifier slack.Notifier) runDeps {
	return runDeps{
		loadX509KeyPair: tls.LoadX509KeyPair,
		newServer:       newDefaultServer,
		signalNotify:    signal.Notify,
		signalStop:      signal.Stop,
		shutdownTimeout: defaultShutdownTimeout,
		oidcVerifier:    verifier,
		oidcCfg:         oidcCfg,
		spamChecker:     spamChecker,
		slackNotifier:   slackNotifier,
	}
}

func newDefaultServer(cert *tls.Certificate, mux *http.ServeMux, logger *slog.Logger) httpsServer {
	tlsConfig := (*tls.Config)(nil)
	if cert != nil {
		tlsConfig = &tls.Config{Certificates: []tls.Certificate{*cert}}
	}
	return &stdHTTPServer{
		Server: &http.Server{
			Addr: ":8080",
			// Chain wraps in reverse: metrics must record recovered 500s, and
			// compression must wrap Recovery (see middleware.Compression).
			Handler:   middleware.Chain(mux, middleware.Metrics, middleware.Logging, middleware.Compression, middleware.Recovery),
			TLSConfig: tlsConfig,
			ErrorLog:  slog.NewLogLogger(logger.Handler(), slog.LevelError),
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

	mode := strings.TrimSpace(os.Getenv(serverModeEnv))
	if mode == "" {
		mode = serverModeHTTPS
	}
	if mode != serverModeHTTPS && mode != serverModeInternalHTTP {
		return fmt.Errorf("%s must be %q or %q, got %q", serverModeEnv, serverModeHTTPS, serverModeInternalHTTP, mode)
	}

	var cert *tls.Certificate
	if mode == serverModeHTTPS {
		certPath := getenvOrDefault(certFileEnv, certFilePath)
		keyPath := getenvOrDefault(keyFileEnv, keyFilePath)

		loadedCert, err := deps.loadX509KeyPair(certPath, keyPath)
		if err != nil {
			return fmt.Errorf("tls certificate load failed: %w", err)
		}
		cert = &loadedCert
	}

	// An unset URL disables semantic search. Keep query embedding timeouts short.
	embedder := embeddings.New(os.Getenv("EMBEDDINGS_URL"), 2*time.Second)

	mux := http.NewServeMux()
	routes.Register(mux, conn, deps.oidcVerifier, deps.oidcCfg, deps.spamChecker, deps.slackNotifier, embedder)
	server := deps.newServer(cert, mux, slog.Default())

	schedulerCtx, stopScheduler := context.WithCancel(context.Background())
	defer stopScheduler()
	go database.RunScheduler(schedulerCtx, conn, database.DefaultScheduleInterval, slog.Default())

	// Background article batches need a longer timeout than search queries.
	go embeddings.NewReconciler(conn, embeddings.New(os.Getenv("EMBEDDINGS_URL"), 2*time.Minute)).Run(schedulerCtx)

	serverErr := make(chan error, 1)
	go func() {
		var err error
		if mode == serverModeInternalHTTP {
			err = server.ListenAndServe()
		} else {
			err = server.ListenAndServeTLS("", "")
		}
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

func akismetCheckerFromEnv() (akismet.Checker, error) {
	apiKey := strings.TrimSpace(os.Getenv(akismetAPIKeyEnv))
	if apiKey == "" {
		return nil, nil
	}

	blogURL := strings.TrimSpace(os.Getenv(akismetBlogURLEnv))
	if blogURL == "" {
		return nil, fmt.Errorf("%s is required when %s is set", akismetBlogURLEnv, akismetAPIKeyEnv)
	}

	return akismet.NewClient(akismet.Config{
		APIKey:  apiKey,
		BlogURL: blogURL,
	})
}

// slackNotifierFromEnv returns nil for an unset webhook and rejects malformed URLs.
func slackNotifierFromEnv() (slack.Notifier, error) {
	webhookURL := strings.TrimSpace(os.Getenv(slackWebhookURLEnv))
	if webhookURL == "" {
		return nil, nil
	}

	client, err := slack.NewClient(slack.Config{
		WebhookURL: webhookURL,
		QueueURL:   strings.TrimSpace(os.Getenv(slackQueueURLEnv)),
	})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", slackWebhookURLEnv, err)
	}
	return client, nil
}

// Slack moderation needs both a notification webhook and a callback signing secret.
func logSlackModerationState(notifier slack.Notifier) {
	hasSecret := strings.TrimSpace(os.Getenv(slackSigningSecretEnv)) != ""

	switch {
	case notifier != nil && hasSecret:
		slog.Info("Slack classified moderation enabled")
	case notifier != nil:
		slog.Warn("Slack classified moderation half-configured: " + slackSigningSecretEnv +
			" not set; notifications will be posted but the Approve/Reject buttons will be refused")
	case hasSecret:
		slog.Warn("Slack classified moderation half-configured: " + slackWebhookURLEnv +
			" not set; no notification is posted, so the interactivity endpoint will never be called")
	default:
		slog.Warn("Slack classified moderation disabled: neither " + slackWebhookURLEnv + " nor " +
			slackSigningSecretEnv + " is set; submissions wait in the CMS queue")
	}
}
