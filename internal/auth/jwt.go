package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Standard JWT errors.
var (
	ErrMalformedToken = errors.New("auth: malformed token")
	ErrInvalidSig     = errors.New("auth: invalid signature")
	ErrTokenExpired   = errors.New("auth: token expired")
	ErrInvalidIssuer  = errors.New("auth: invalid issuer")
)

// Claims represents the JWT payload fields we care about.
// Additional fields from the token are ignored.
type Claims struct {
	Subject   string `json:"sub"`            // user ID
	Issuer    string `json:"iss,omitempty"`   // token issuer
	ExpiresAt int64  `json:"exp"`            // expiration (unix timestamp)
	IssuedAt  int64  `json:"iat,omitempty"`  // issued at (unix timestamp)
}

// JWTValidator validates JWT tokens using HMAC-SHA256 (HS256).
// In production, you might use RS256 with public/private keys,
// but HMAC is simpler and appropriate for single-issuer systems.
type JWTValidator struct {
	secret []byte
	issuer string // expected issuer; empty = skip issuer check
}

// NewJWTValidator creates a validator with the given HMAC secret and expected issuer.
func NewJWTValidator(secret string, issuer string) *JWTValidator {
	return &JWTValidator{
		secret: []byte(secret),
		issuer: issuer,
	}
}

// Validate parses and validates a JWT token string.
// It checks: structure, signature, expiration, and optionally the issuer.
func (v *JWTValidator) Validate(tokenStr string) (*Claims, error) {
	// Split into header.payload.signature
	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		return nil, ErrMalformedToken
	}

	headerB64, payloadB64, sigB64 := parts[0], parts[1], parts[2]

	// Verify signature: HMAC-SHA256(header.payload, secret)
	signingInput := headerB64 + "." + payloadB64
	if !v.verifySignature(signingInput, sigB64) {
		return nil, ErrInvalidSig
	}

	// Decode and parse the payload
	payloadJSON, err := base64URLDecode(payloadB64)
	if err != nil {
		return nil, fmt.Errorf("%w: payload decode: %v", ErrMalformedToken, err)
	}

	var claims Claims
	if err := json.Unmarshal(payloadJSON, &claims); err != nil {
		return nil, fmt.Errorf("%w: payload unmarshal: %v", ErrMalformedToken, err)
	}

	// Check expiration
	if claims.ExpiresAt > 0 && time.Now().Unix() > claims.ExpiresAt {
		return nil, ErrTokenExpired
	}

	// Check issuer if configured
	if v.issuer != "" && claims.Issuer != v.issuer {
		return nil, ErrInvalidIssuer
	}

	return &claims, nil
}

// verifySignature checks the HMAC-SHA256 signature of the token.
func (v *JWTValidator) verifySignature(signingInput, sigB64 string) bool {
	mac := hmac.New(sha256.New, v.secret)
	mac.Write([]byte(signingInput))
	expectedSig := mac.Sum(nil)

	actualSig, err := base64URLDecode(sigB64)
	if err != nil {
		return false
	}

	return hmac.Equal(expectedSig, actualSig)
}

// GenerateToken creates a signed JWT token. This is provided as a convenience
// for testing — in production, token issuance is handled by an auth service.
func GenerateToken(secret string, claims Claims) (string, error) {
	// Header: always HS256
	header := `{"alg":"HS256","typ":"JWT"}`
	headerB64 := base64URLEncode([]byte(header))

	// Set issued-at if not provided
	if claims.IssuedAt == 0 {
		claims.IssuedAt = time.Now().Unix()
	}

	// Payload
	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("auth: marshal claims: %w", err)
	}
	payloadB64 := base64URLEncode(payloadJSON)

	// Signature
	signingInput := headerB64 + "." + payloadB64
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signingInput))
	sig := mac.Sum(nil)
	sigB64 := base64URLEncode(sig)

	return signingInput + "." + sigB64, nil
}

// base64URLEncode encodes data using base64url (no padding), per RFC 7515.
func base64URLEncode(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}

// base64URLDecode decodes base64url-encoded data (no padding), per RFC 7515.
func base64URLDecode(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}
