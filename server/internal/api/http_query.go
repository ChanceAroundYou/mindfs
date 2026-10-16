package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

func parseNonNegativeInt64Query(r *http.Request, key string) (int64, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return 0, errInvalidRequest(key + " must be non-negative")
	}
	return value, nil
}

func parseOptionalTimeQuery(r *http.Request, key string) (time.Time, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return time.Time{}, nil
	}
	value, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		value, err = time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return time.Time{}, errInvalidRequest(key + " must be RFC3339")
		}
	}
	return value.UTC(), nil
}

func parsePositiveIntQuery(r *http.Request, key string) (int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, errInvalidRequest(key + " must be positive")
	}
	return value, nil
}

func truthyQuery(r *http.Request, key string) bool {
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get(key))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
