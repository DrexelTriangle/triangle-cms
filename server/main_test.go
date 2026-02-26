package main

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

type fakeServer struct {
	addr           string
	listenFn       func(certFile, keyFile string) error
	shutdownFn     func(ctx context.Context) error
	closeFn        func() error
	shutdownCalled bool
	closeCalled    bool
}

func (f *fakeServer) ListenAndServeTLS(certFile, keyFile string) error {
	if f.listenFn != nil {
		return f.listenFn(certFile, keyFile)
	}
	return nil
}

func (f *fakeServer) Shutdown(ctx context.Context) error {
	f.shutdownCalled = true
	if f.shutdownFn != nil {
		return f.shutdownFn(ctx)
	}
	return nil
}

func (f *fakeServer) Close() error {
	f.closeCalled = true
	if f.closeFn != nil {
		return f.closeFn()
	}
	return nil
}

func (f *fakeServer) Addr() string {
	return f.addr
}

func TestRun_TLSLoadFailure(t *testing.T) {
	newServerCalled := false
	err := run(runDeps{
		loadX509KeyPair: func(certFile, keyFile string) (tls.Certificate, error) {
			return tls.Certificate{}, errors.New("bad certificate")
		},
		newServer: func(cert tls.Certificate, mux *http.ServeMux, logger *slog.Logger) httpsServer {
			newServerCalled = true
			return &fakeServer{}
		},
		signalCh: make(chan os.Signal, 1),
	})

	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "tls certificate load failed") {
		t.Fatalf("expected tls load error, got %v", err)
	}
	if newServerCalled {
		t.Fatal("expected server not to be created on cert load failure")
	}
}

func TestRun_TLSLoadPathsFromEnv(t *testing.T) {
	t.Setenv(certFileEnv, "/tmp/custom-cert.crt")
	t.Setenv(keyFileEnv, "/tmp/custom-key.key")

	gotCertPath := ""
	gotKeyPath := ""
	err := run(runDeps{
		loadX509KeyPair: func(certFile, keyFile string) (tls.Certificate, error) {
			gotCertPath = certFile
			gotKeyPath = keyFile
			return tls.Certificate{}, errors.New("bad certificate")
		},
		signalCh: make(chan os.Signal, 1),
	})

	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if gotCertPath != "/tmp/custom-cert.crt" {
		t.Fatalf("expected cert path from env, got %q", gotCertPath)
	}
	if gotKeyPath != "/tmp/custom-key.key" {
		t.Fatalf("expected key path from env, got %q", gotKeyPath)
	}
}

func TestRun_ServerExitError(t *testing.T) {
	srv := &fakeServer{
		addr: ":8080",
		listenFn: func(certFile, keyFile string) error {
			return errors.New("listen failed")
		},
	}

	err := run(runDeps{
		loadX509KeyPair: func(certFile, keyFile string) (tls.Certificate, error) {
			return tls.Certificate{}, nil
		},
		newServer: func(cert tls.Certificate, mux *http.ServeMux, logger *slog.Logger) httpsServer {
			return srv
		},
		signalCh: make(chan os.Signal, 1),
	})

	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "https server exited with error") {
		t.Fatalf("expected server exit error, got %v", err)
	}
	if srv.shutdownCalled {
		t.Fatal("expected shutdown not to be called when server exits with error")
	}
}

func TestRun_GracefulShutdownOnSignal(t *testing.T) {
	releaseListen := make(chan struct{})
	sigCh := make(chan os.Signal, 1)
	sigCh <- syscall.SIGTERM

	srv := &fakeServer{
		addr: ":8080",
		listenFn: func(certFile, keyFile string) error {
			<-releaseListen
			return http.ErrServerClosed
		},
		shutdownFn: func(ctx context.Context) error {
			close(releaseListen)
			return nil
		},
	}

	err := run(runDeps{
		loadX509KeyPair: func(certFile, keyFile string) (tls.Certificate, error) {
			return tls.Certificate{}, nil
		},
		newServer: func(cert tls.Certificate, mux *http.ServeMux, logger *slog.Logger) httpsServer {
			return srv
		},
		signalCh:        sigCh,
		shutdownTimeout: time.Second,
	})

	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !srv.shutdownCalled {
		t.Fatal("expected shutdown to be called")
	}
	if srv.closeCalled {
		t.Fatal("expected close not to be called on successful shutdown")
	}
}

func TestRun_ShutdownFailureCallsClose(t *testing.T) {
	releaseListen := make(chan struct{})
	sigCh := make(chan os.Signal, 1)
	sigCh <- syscall.SIGTERM

	srv := &fakeServer{
		addr: ":8080",
		listenFn: func(certFile, keyFile string) error {
			<-releaseListen
			return http.ErrServerClosed
		},
		shutdownFn: func(ctx context.Context) error {
			return errors.New("shutdown failed")
		},
		closeFn: func() error {
			close(releaseListen)
			return nil
		},
	}

	err := run(runDeps{
		loadX509KeyPair: func(certFile, keyFile string) (tls.Certificate, error) {
			return tls.Certificate{}, nil
		},
		newServer: func(cert tls.Certificate, mux *http.ServeMux, logger *slog.Logger) httpsServer {
			return srv
		},
		signalCh:        sigCh,
		shutdownTimeout: time.Second,
	})

	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "graceful shutdown failed") {
		t.Fatalf("expected graceful shutdown error, got %v", err)
	}
	if !srv.closeCalled {
		t.Fatal("expected close to be called when shutdown fails")
	}
}
