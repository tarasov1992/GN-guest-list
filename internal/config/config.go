package config

import (
	"errors"
	"os"
)

type Config struct {
	DatabaseURL   string
	HTTPAddr      string
	AdminPassword string
}

func Load() (Config, error) {
	cfg := Config{HTTPAddr: ":8080"}
	var errs []error

	cfg.DatabaseURL = os.Getenv("DATABASE_URL")
	if cfg.DatabaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL is not set"))
	}
	if v := os.Getenv("HTTP_ADDR"); v != "" {
		cfg.HTTPAddr = v
	}
	cfg.AdminPassword = os.Getenv("ADMIN_PASSWORD")
	if len(cfg.AdminPassword) < 16 {
		errs = append(errs, errors.New("ADMIN_PASSWORD must be at least 16 characters"))
	}

	if len(errs) > 0 {
		return Config{}, errors.Join(errs...)
	}

	return cfg, nil
}
