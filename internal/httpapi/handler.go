package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
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

// NewHandler serves health checks and, when service is provided, link operations.
func NewHandler(service LinkService, baseURL string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte("ok\n"))
	})
	if service != nil {
		h := linkHandler{service: service, baseURL: strings.TrimRight(baseURL, "/")}
		mux.HandleFunc("POST /links", h.create)
		mux.HandleFunc("GET /{code}", h.redirect)
		mux.HandleFunc("GET /links/{code}", h.stats)
	}
	return mux
}

type linkHandler struct {
	service LinkService
	baseURL string
}

func (h linkHandler) create(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()
	var request *struct {
		URL string `json:"url"`
	}
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&request); err != nil || request == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "expected a single JSON object"})
		return
	}
	created, err := h.service.Shorten(r.Context(), request.URL)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, struct {
		Code     string `json:"code"`
		ShortURL string `json:"short_url"`
	}{Code: created.Code, ShortURL: h.baseURL + "/" + created.Code})
}

func (h linkHandler) redirect(w http.ResponseWriter, r *http.Request) {
	url, err := h.service.Resolve(r.Context(), r.PathValue("code"))
	if err != nil {
		writeError(w, err)
		return
	}
	http.Redirect(w, r, url, http.StatusFound)
}

func (h linkHandler) stats(w http.ResponseWriter, r *http.Request) {
	stats, err := h.service.Stats(r.Context(), r.PathValue("code"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		URL       string    `json:"url"`
		Code      string    `json:"code"`
		Clicks    int64     `json:"clicks"`
		CreatedAt time.Time `json:"created_at"`
	}{URL: stats.URL, Code: stats.Code, Clicks: stats.Clicks, CreatedAt: stats.CreatedAt})
}

func writeError(w http.ResponseWriter, err error) {
	status, message := http.StatusInternalServerError, "internal server error"
	switch {
	case errors.Is(err, link.ErrInvalidURL):
		status, message = http.StatusBadRequest, "invalid URL"
	case errors.Is(err, link.ErrNotFound):
		status, message = http.StatusNotFound, "link not found"
	}
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}
