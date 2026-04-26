package config

import (
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	HTTP HTTPConfig
	DB   DBConfig
}

type HTTPConfig struct {
	Port string
}

type DBConfig struct {
	Host     string
	Port     string
	User     string
	Password string
	Name     string
	SSLMode  string
}

func Load() (Config, error) {
	port := envOrDefault("PORT", "8080")
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return Config{}, fmt.Errorf("invalid PORT %q: must be an integer between 1 and 65535", port)
	}

	db := DBConfig{
		Host:     envOrDefault("DB_HOST", "postgres"),
		Port:     envOrDefault("DB_PORT", "5432"),
		User:     envOrDefault("DB_USER", "stocksim"),
		Password: envOrDefault("DB_PASSWORD", "stocksim"),
		Name:     envOrDefault("DB_NAME", "stocksim"),
		SSLMode:  envOrDefault("DB_SSLMODE", "disable"),
	}

	if n, err := strconv.Atoi(db.Port); err != nil || n < 1 || n > 65535 {
		return Config{}, fmt.Errorf("invalid DB_PORT %q: must be an integer between 1 and 65535", db.Port)
	}

	switch db.SSLMode {
	case "disable", "allow", "prefer", "require", "verify-ca", "verify-full":
	default:
		return Config{}, fmt.Errorf("invalid DB_SSLMODE %q: must be one of disable, allow, prefer, require, verify-ca, verify-full", db.SSLMode)
	}

	return Config{
		HTTP: HTTPConfig{Port: port},
		DB:   db,
	}, nil
}

func (c DBConfig) DSN() string {
	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s",
		c.User, c.Password, c.Host, c.Port, c.Name, c.SSLMode)
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
