package config

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"time"

	"checkin/internal/cloudsync"
)

// doorSecretRe keeps the secret safe to use as a URL path segment and in mux patterns.
var doorSecretRe = regexp.MustCompile(`^[A-Za-z0-9_-]{16,}$`)

type Config struct {
	DatabaseURL   string
	HTTPAddr      string
	AdminPassword string
	DoorSecret    string
	YandexURL     string
	GoogleURL     string
	Source        string
	SyncInterval  time.Duration
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
	cfg.Source = os.Getenv("SOURCE")
	switch cfg.Source {
	case cloudsync.SourceYD, cloudsync.SourceGD:
	case "":
		errs = append(errs, errors.New("SOURCE is not set"))
	default:
		errs = append(errs, fmt.Errorf("SOURCE must be YD or GD, got %q", cfg.Source))
	}
	cfg.YandexURL = os.Getenv("YANDEX_URL")
	if cfg.YandexURL == "" && cfg.Source == cloudsync.SourceYD {
		errs = append(errs, errors.New("YANDEX_URL is not set (SOURCE=YD)"))
	}
	cfg.GoogleURL = os.Getenv("GOOGLE_URL")
	if cfg.GoogleURL == "" && cfg.Source == cloudsync.SourceGD {
		errs = append(errs, errors.New("GOOGLE_URL is not set (SOURCE=GD)"))
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
