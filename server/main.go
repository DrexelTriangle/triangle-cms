package main

import (
	"crypto/tls"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"server/internal/middleware"
	"server/internal/routes"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	const certFile = "./certs/localhost.crt"
	const keyFile = "./certs/localhost.key"

	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		slog.Error("tls certificate load failed", "cert_file", certFile, "key_file", keyFile, "error", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()
	routes.Register(mux)

	server := http.Server{
		Addr:    ":8080",
		Handler: middleware.Chain(mux, middleware.Recovery, middleware.Logging),
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
		},
		ErrorLog: slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}

	if err := server.ListenAndServeTLS("", ""); err != nil {
		if !errors.Is(err, http.ErrServerClosed) {
			slog.Error("https server exited with error", "addr", server.Addr, "error", err)
			os.Exit(1)
		}
	}
}
