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
	"server/internal/database"
	"server/internal/middleware"
	"server/internal/routes"

	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/joho/godotenv"
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
	Addr() string
}

type stdHTTPServer struct {
	*http.Server
}

func (s *stdHTTPServer) Addr() string {
	return s.Server.Addr
}

type runDeps struct {
	loadX509KeyPair func(certFile, keyFile string) (tls.Certificate, error)
	newServer       func(cert tls.Certificate, mux *http.ServeMux, logger *slog.Logger) httpsServer
	signalCh        <-chan os.Signal
	signalNotify    func(c chan<- os.Signal, sig ...os.Signal)
	signalStop      func(c chan<- os.Signal)
	shutdownTimeout time.Duration
}

func main() {
	godotenv.Load(".env")

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "cms")
	slog.SetDefault(logger)

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

	// Just for testing the database
	row := db.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ?", dbName)
	var tableCount int
	if err := row.Scan(&tableCount); err != nil {
		slog.Error("cms startup", "event", "startup", "stage", "db_table_count", "error", err)
	} else {
		slog.Info("cms startup", "event", "startup", "stage", "db_table_count", "table_count", tableCount)
	}

	if err := run(defaultRunDeps(), db); err != nil {
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

func defaultRunDeps() runDeps {
	return runDeps{
		loadX509KeyPair: tls.LoadX509KeyPair,
		newServer:       newDefaultServer,
		signalNotify:    signal.Notify,
		signalStop:      signal.Stop,
		shutdownTimeout: defaultShutdownTimeout,
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
	routes.Register(mux, conn)
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
