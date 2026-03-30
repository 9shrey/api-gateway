package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"
)

// This program starts a simple HTTP backend server for testing the API gateway.
// Run multiple instances on different ports to verify load balancing:
//
//	go run examples/backend/main.go -port 8081 -name backend-1
//	go run examples/backend/main.go -port 8082 -name backend-2
//	go run examples/backend/main.go -port 8083 -name backend-3

func main() {
	port := flag.Int("port", 8081, "port to listen on")
	name := flag.String("name", "backend-1", "server name for identification")
	flag.Parse()

	mux := http.NewServeMux()

	// Catch-all handler — returns the server identity and echoes request details.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"server":     *name,
			"port":       *port,
			"path":       r.URL.Path,
			"method":     r.Method,
			"headers":    flattenHeaders(r.Header),
			"timestamp":  time.Now().UTC().Format(time.RFC3339),
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(resp); err != nil {
			log.Printf("[%s] failed to encode response: %v", *name, err)
		}

		log.Printf("[%s] %s %s from %s", *name, r.Method, r.URL.Path, r.RemoteAddr)
	})

	// Health check endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","server":"%s"}`, *name)
	})

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("[%s] starting on %s", *name, addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Printf("[%s] server error: %v", *name, err)
		os.Exit(1)
	}
}

// flattenHeaders converts multi-value headers into a simple map for JSON output.
func flattenHeaders(h http.Header) map[string]string {
	flat := make(map[string]string, len(h))
	for k, v := range h {
		if len(v) == 1 {
			flat[k] = v[0]
		} else {
			flat[k] = fmt.Sprintf("%v", v)
		}
	}
	return flat
}
