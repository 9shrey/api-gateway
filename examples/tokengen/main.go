package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/9shrey/api-gateway/internal/auth"
)

// This utility generates a signed JWT token for testing the API gateway.
//
// Usage:
//   go run examples/tokengen/main.go -secret "my-secret-key" -sub "user-123" -exp 1h
//
// The generated token can be used with curl:
//   curl -H "Authorization: Bearer <token>" http://localhost:8080/api/users

func main() {
	secret := flag.String("secret", "my-secret-key", "HMAC secret for signing (must match gateway config)")
	subject := flag.String("sub", "user-123", "subject (user ID) claim")
	issuer := flag.String("iss", "api-gateway", "issuer claim")
	expiry := flag.Duration("exp", 1*time.Hour, "token expiry duration from now")
	flag.Parse()

	claims := auth.Claims{
		Subject:   *subject,
		Issuer:    *issuer,
		ExpiresAt: time.Now().Add(*expiry).Unix(),
	}

	token, err := auth.GenerateToken(*secret, claims)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(token)
}
