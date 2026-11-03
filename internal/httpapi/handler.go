package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/uneronnik/shortener/internal/link"
)

type LinkService interface {
	Shorten(context.Context, string) (link.Link, error)
	Resolve(context.Context, string) (string, error)
	Stats(context.Context, string) (link.Link, error)
}

type Options struct {
	OperationTimeout time.Duration
	Logger           *slog.Logger
}

// NewHandler serves health checks and, when service is provided, link operations.
func NewHandler(service LinkService, baseURL string, options ...Options) http.Handler {
	opts := Options{OperationTimeout: 3 * time.Second, Logger: slog.Default()}
	if len(options) > 0 {
		if options[0].OperationTimeout > 0 {
			opts.OperationTimeout = options[0].OperationTimeout
		}
		if options[0].Logger != nil {
			opts.Logger = options[0].Logger
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte("ok\n"))
	})
	if service != nil {
		h := linkHandler{service: service, baseURL: strings.TrimRight(baseURL, "/"), options: opts}
		mux.HandleFunc("POST /links", h.create)
		mux.HandleFunc("GET /{code}", h.redirect)
		mux.HandleFunc("GET /links/{code}", h.stats)
	}
	return mux
}

type linkHandler struct {
	service LinkService
	baseURL string
	options Options
}

func (h linkHandler) create(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()
	var request *struct {
		URL string `json:"url"`
	}
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&request); err != nil || request == nil {
		if h.readError(w, r, err) {
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		if h.readError(w, r, err) {
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "expected a single JSON object"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), h.options.OperationTimeout)
	defer cancel()
	created, err := h.service.Shorten(ctx, request.URL)
	if err != nil {
		h.writeError(w, r, "shorten", err)
		return
	}
	writeJSON(w, http.StatusCreated, struct {
		Code     string `json:"code"`
		ShortURL string `json:"short_url"`
	}{Code: created.Code, ShortURL: h.baseURL + "/" + created.Code})
}

func (h linkHandler) redirect(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), h.options.OperationTimeout)
	defer cancel()
	url, err := h.service.Resolve(ctx, r.PathValue("code"))
	if err != nil {
		h.writeError(w, r, "resolve", err)
		return
	}
	http.Redirect(w, r, url, http.StatusFound)
}

func (h linkHandler) stats(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), h.options.OperationTimeout)
	defer cancel()
	stats, err := h.service.Stats(ctx, r.PathValue("code"))
	if err != nil {
		h.writeError(w, r, "stats", err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		URL       string    `json:"url"`
		Code      string    `json:"code"`
		Clicks    int64     `json:"clicks"`
		CreatedAt time.Time `json:"created_at"`
	}{URL: stats.URL, Code: stats.Code, Clicks: stats.Clicks, CreatedAt: stats.CreatedAt})
}

func (h linkHandler) readError(w http.ResponseWriter, r *http.Request, err error) bool {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		writeJSON(w, http.StatusRequestTimeout, map[string]string{"error": "request body timeout"})
		return true
	}
	if r.Context().Err() != nil {
		return true
	}
	return false
}

func (h linkHandler) writeError(w http.ResponseWriter, r *http.Request, operation string, err error) {
	if r.Context().Err() != nil {
		return
	}
	status, message := http.StatusInternalServerError, "internal server error"
	switch {
	case errors.Is(err, link.ErrInvalidURL):
		status, message = http.StatusBadRequest, "invalid URL"
	case errors.Is(err, link.ErrNotFound):
		status, message = http.StatusNotFound, "link not found"
	case errors.Is(err, context.DeadlineExceeded):
		status, message = http.StatusServiceUnavailable, "operation timed out"
		h.options.Logger.Warn("link operation failed", "operation", operation, "reason", "deadline_exceeded")
	default:
		reason := "internal_error"
		if errors.Is(err, link.ErrCodeTaken) {
			reason = "code_collision_exhausted"
		}
		if errors.Is(err, context.Canceled) {
			reason = "operation_canceled"
		}
		var networkError net.Error
		if errors.As(err, &networkError) {
			reason = "network_error"
		}
		// Never log arbitrary error text: database errors can include URLs and credentials.
		var state interface{ SQLState() string }
		if errors.As(err, &state) {
			code := state.SQLState()
			if len(code) == 5 && strings.Trim(code, "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ") == "" {
				reason = "postgres_" + code
			}
		}
		h.options.Logger.Error("link operation failed", "operation", operation, "reason", reason)
	}
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}
