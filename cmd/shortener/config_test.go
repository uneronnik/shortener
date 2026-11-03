package main

import (
	"net/http"
	"testing"
	"time"
)

func TestTimeoutConfig(t *testing.T) {
	for _, name := range []string{"HTTP_READ_TIMEOUT", "OPERATION_TIMEOUT", "HTTP_WRITE_TIMEOUT"} {
		t.Setenv(name, "")
	}
	cfg, err := loadTimeouts()
	if err != nil || cfg.read != 5*time.Second || cfg.operation != 3*time.Second || cfg.write != 10*time.Second {
		t.Fatalf("config = %+v, %v", cfg, err)
	}
	for _, value := range []string{"0s", "-1s", "secret-invalid", "999999999999h"} {
		t.Setenv("OPERATION_TIMEOUT", value)
		if _, err := loadTimeouts(); err == nil {
			t.Fatalf("accepted %q", value)
		}
	}
	t.Setenv("OPERATION_TIMEOUT", "3s")
	t.Setenv("HTTP_WRITE_TIMEOUT", "8s")
	if _, err := loadTimeouts(); err == nil {
		t.Fatal("accepted insufficient write budget")
	}
	t.Setenv("HTTP_WRITE_TIMEOUT", "9s")
	if _, err := loadTimeouts(); err != nil {
		t.Fatal(err)
	}
	server, err := newHTTPServer("", http.NotFoundHandler())
	if err != nil || server.ReadTimeout != 5*time.Second || server.WriteTimeout != 9*time.Second {
		t.Fatalf("server config = %+v, %v", server, err)
	}
	for _, name := range []string{"HTTP_READ_TIMEOUT", "HTTP_WRITE_TIMEOUT"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, "invalid-secret")
			if _, err := newHTTPServer("", http.NotFoundHandler()); err == nil {
				t.Fatal("accepted invalid timeout")
			}
		})
	}
}
