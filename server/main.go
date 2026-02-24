package main

import (
	"crypto/tls"
	"net/http"
	"server/internal/middleware"
	"server/internal/routes"
)

func main() {
	cert, err := tls.LoadX509KeyPair("./certs/localhost.crt", "./certs/localhost.key")
	if err != nil {
		//handle
	}

	mux := http.NewServeMux()
	routes.Register(mux)

	server := http.Server{
		Addr:    ":8080",
		Handler: middleware.Logging(mux),
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
		},
	}

	server.ListenAndServeTLS("", "")
	if err != nil {
		//handle
	}
}
