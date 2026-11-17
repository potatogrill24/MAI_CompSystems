package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

func Env(key, fallback string) string {
	if s := os.Getenv(key); s != "" {
		return s
	}
	return fallback
}
func JSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if value != nil {
		_ = json.NewEncoder(w).Encode(value)
	}
}
func Error(w http.ResponseWriter, status int, message string) {
	JSON(w, status, map[string]string{"error": message})
}
func Internal(w http.ResponseWriter, err error) {
	slog.Error("request failed", "error", err)
	Error(w, 500, "internal server error")
}
func Decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	raw, readErr := io.ReadAll(r.Body)
	if readErr != nil {
		var size *http.MaxBytesError
		status := 400
		if errors.As(readErr, &size) {
			status = 413
		}
		Error(w, status, "invalid request body")
		return false
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		Error(w, 400, "expected a JSON object")
		return false
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	err := d.Decode(dst)
	if err == nil {
		if e := d.Decode(new(any)); !errors.Is(e, io.EOF) {
			err = errors.New("expected one JSON object")
		}
	}
	if err != nil {
		var size *http.MaxBytesError
		status := 400
		if errors.As(err, &size) {
			status = 413
		}
		Error(w, status, "invalid request body: "+err.Error())
		return false
	}
	return true
}
func Date(s string) bool {
	for _, layout := range []string{"02-01-2006", "2006-01-02"} {
		if _, err := time.Parse(layout, s); err == nil {
			return true
		}
	}
	return false
}

var codePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$`)
var moneyPattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,11})(\.[0-9]{1,2})?$`)

func Code(s string) bool  { return codePattern.MatchString(s) }
func Money(s string) bool { return moneyPattern.MatchString(s) }
func Text(s string) bool  { return strings.TrimSpace(s) != "" && len([]rune(s)) <= 255 }

var Client = &http.Client{Timeout: 15 * time.Second}

type RemoteError struct {
	Status  int
	Message string
}

func (e *RemoteError) Error() string {
	return fmt.Sprintf("upstream status %d: %s", e.Status, e.Message)
}
func Call(ctx context.Context, method, url string, input, output any) error {
	var b bytes.Buffer
	if input != nil {
		if err := json.NewEncoder(&b).Encode(input); err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, url, &b)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &RemoteError{resp.StatusCode, string(raw)}
	}
	if output != nil {
		return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(output)
	}
	return nil
}
func Wrap(mux http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if p := recover(); p != nil {
				slog.Error("panic", "error", p)
				Error(w, 500, "internal server error")
			}
		}()
		w.Header().Set("X-Content-Type-Options", "nosniff")
		mux.ServeHTTP(w, r)
	})
}
func Health(mux *http.ServeMux, ping func(context.Context) error) {
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { JSON(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if ping != nil {
			if err := ping(ctx); err != nil {
				Error(w, 503, "dependency unavailable")
				return
			}
		}
		JSON(w, 200, map[string]string{"status": "ready"})
	})
}
