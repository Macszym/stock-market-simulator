package config

import (
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	HTTP HTTPConfig
}

type HTTPConfig struct {
	Port string
}

func Load() (Config, error) {
	port := envOrDefault("PORT", "8080")
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return Config{}, fmt.Errorf("invalid PORT %q: must be an integer between 1 and 65535", port)
	}

	return Config{
		HTTP: HTTPConfig{Port: port},
	}, nil
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
