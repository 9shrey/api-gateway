package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the top-level configuration for the API Gateway.
type Config struct {
	Server    ServerConfig    `yaml:"server"`
	Redis     RedisConfig     `yaml:"redis"`
	RateLimit RateLimitConfig `yaml:"rate_limit"`
	Auth      AuthConfig      `yaml:"auth"`
	Routes    []RouteConfig   `yaml:"routes"`
}

// AuthConfig holds JWT authentication settings.
type AuthConfig struct {
	Enabled   bool   `yaml:"enabled"`
	JWTSecret string `yaml:"jwt_secret"` // HMAC-SHA256 secret
	Issuer    string `yaml:"issuer"`     // expected issuer claim (optional)
}

// RedisConfig holds connection settings for Redis.
type RedisConfig struct {
	Addr     string `yaml:"addr"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
}

// RateLimitConfig holds default rate limiting parameters.
// These apply globally unless a route overrides them.
type RateLimitConfig struct {
	Enabled bool `yaml:"enabled"`
	Rate    int  `yaml:"rate"`  // tokens per second
	Burst   int  `yaml:"burst"` // max burst capacity
}

// ServerConfig holds the gateway's own listener settings.
type ServerConfig struct {
	Port         int           `yaml:"port"`
	ReadTimeout  time.Duration `yaml:"read_timeout"`
	WriteTimeout time.Duration `yaml:"write_timeout"`
}

// RouteConfig defines a single route: the incoming path prefix
// and the backend servers that handle it.
type RouteConfig struct {
	Path         string      `yaml:"path"`
	Backends     []string    `yaml:"backends"`
	Retry        RetryConfig `yaml:"retry"`
	AuthRequired bool        `yaml:"auth_required"` // if true, JWT auth is enforced on this route
}

// RetryConfig controls retry behaviour for proxied requests.
type RetryConfig struct {
	MaxAttempts int           `yaml:"max_attempts"`
	Timeout     time.Duration `yaml:"timeout"`
	BaseDelay   time.Duration `yaml:"base_delay"` // base delay for exponential backoff between retries
}

// Load reads and parses the YAML config file at the given path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read file %s: %w", path, err)
	}

	// Expand environment variables in the YAML (e.g. ${JWT_SECRET})
	expanded := os.ExpandEnv(string(data))

	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("config: parse yaml: %w", err)
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// validate checks for required fields and applies defaults.
func (c *Config) validate() error {
	if c.Server.Port == 0 {
		c.Server.Port = 8080
	}
	if c.Server.ReadTimeout == 0 {
		c.Server.ReadTimeout = 5 * time.Second
	}
	if c.Server.WriteTimeout == 0 {
		c.Server.WriteTimeout = 10 * time.Second
	}

	// Redis defaults
	if c.Redis.Addr == "" {
		c.Redis.Addr = "localhost:6379"
	}

	// Rate limit defaults
	if c.RateLimit.Enabled && c.RateLimit.Rate == 0 {
		c.RateLimit.Rate = 10
	}
	if c.RateLimit.Enabled && c.RateLimit.Burst == 0 {
		c.RateLimit.Burst = 20
	}

	// Auth defaults
	if c.Auth.Enabled && c.Auth.JWTSecret == "" {
		return fmt.Errorf("config: auth is enabled but jwt_secret is empty")
	}

	if len(c.Routes) == 0 {
		return fmt.Errorf("config: at least one route is required")
	}

	for i, r := range c.Routes {
		if r.Path == "" {
			return fmt.Errorf("config: route[%d] missing path", i)
		}
		if len(r.Backends) == 0 {
			return fmt.Errorf("config: route[%d] (%s) has no backends", i, r.Path)
		}
		// Apply retry defaults
		if c.Routes[i].Retry.MaxAttempts == 0 {
			c.Routes[i].Retry.MaxAttempts = 1
		}
		if c.Routes[i].Retry.Timeout == 0 {
			c.Routes[i].Retry.Timeout = 10 * time.Second
		}
		if c.Routes[i].Retry.BaseDelay == 0 {
			c.Routes[i].Retry.BaseDelay = 100 * time.Millisecond
		}
	}

	return nil
}
