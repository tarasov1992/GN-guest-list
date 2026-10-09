package config

import (
	"errors"
	"os"
	"regexp"
	"time"
)

// doorSecretRe keeps the secret safe to use as a URL path segment and in mux patterns.
var doorSecretRe = regexp.MustCompile(`^[A-Za-z0-9_-]{16,}$`)

type Config struct {
	DatabaseURL     string
	HTTPAddr        string
	AdminPassword   string
	DoorSecret      string
	YandexPublicURL string
	SyncInterval    time.Duration
}

func Load() (Config, error) {
	cfg := Config{HTTPAddr: ":8080", SyncInterval: time.Hour}
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
	cfg.DoorSecret = os.Getenv("DOOR_SECRET")
	if !doorSecretRe.MatchString(cfg.DoorSecret) {
		errs = append(errs, errors.New("DOOR_SECRET must be at least 16 characters of A-Z, a-z, 0-9, _ or -"))
	}
	cfg.YandexPublicURL = os.Getenv("YANDEX_PUBLIC_URL")
	if cfg.YandexPublicURL == "" {
		errs = append(errs, errors.New("YANDEX_PUBLIC_URL is not set"))
	}
	if v := os.Getenv("SYNC_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < time.Minute {
			errs = append(errs, errors.New("SYNC_INTERVAL must be a duration of at least 1m, e.g. 30m or 1h"))
		} else {
			cfg.SyncInterval = d
		}
	}

	if len(errs) > 0 {
		return Config{}, errors.Join(errs...)
	}

	return cfg, nil
}
