package main

import (
	"fmt"
	"os"
	"time"
)

type timeouts struct{ read, operation, write time.Duration }

func loadTimeouts() (timeouts, error) {
	cfg := timeouts{read: 5 * time.Second, operation: 3 * time.Second, write: 10 * time.Second}
	for _, setting := range []struct {
		name   string
		target *time.Duration
	}{
		{"HTTP_READ_TIMEOUT", &cfg.read}, {"OPERATION_TIMEOUT", &cfg.operation}, {"HTTP_WRITE_TIMEOUT", &cfg.write},
	} {
		if raw := os.Getenv(setting.name); raw != "" {
			value, err := time.ParseDuration(raw)
			if err != nil || value <= 0 {
				return timeouts{}, fmt.Errorf("%s must be a positive duration", setting.name)
			}
			*setting.target = value
		}
	}
	// Subtraction avoids overflowing a sum of user-supplied durations.
	if cfg.write <= cfg.read || cfg.write-cfg.read <= cfg.operation {
		return timeouts{}, fmt.Errorf("HTTP_WRITE_TIMEOUT must exceed HTTP_READ_TIMEOUT + OPERATION_TIMEOUT")
	}
	return cfg, nil
}
