package api

import (
	"context"
	"net/http"
	"strings"

	"mindfs/internal/deploy"
)

type deployPrefixContextKey string

const originalPathKey deployPrefixContextKey = "mindfs.original_path"

// withOriginalPath stashes the pre-strip request path so handlers that derive
// signed proofs from the URL (WebSocket E2EE proof) keep matching the path the
// client computed the proof over.
func withOriginalPath(r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), originalPathKey, r.URL.Path))
}

// OriginalPath returns the request path before any deployment-prefix stripping.
// Falls back to r.URL.Path when no original was recorded (unit tests, non-proxied).
func OriginalPath(r *http.Request) string {
	if v, ok := r.Context().Value(originalPathKey).(string); ok && v != "" {
		return v
	}
	return r.URL.Path
}

// StripDeployPrefix wraps next with a strict deployment-prefix stripper.
//
// For a non-empty prefix, only requests equal to the prefix or under
// "<prefix>/" are served; the prefix is stripped and the rest routed to next.
// Everything else (bare requests, double-prefix, stray paths) returns 404.
// Root deployment (empty prefix) is the documented exception: all paths pass
// through unchanged.
func StripDeployPrefix(prefix string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if prefix == "" {
			next.ServeHTTP(w, r)
			return
		}
		path := r.URL.Path
		if alias := deploy.RelayAssetsAlias(); strings.HasPrefix(path, alias) {
			// Relay rewrites relative bundles to a prefix-derived absolute alias
			// (for example /mindfs-assets/). Map only that exact alias back to
			// the internal static assets route; all other bare paths stay rejected.
			r2 := withOriginalPath(r)
			u2 := *r.URL
			r2.URL = &u2
			r2.URL.Path = "/assets/" + strings.TrimPrefix(path, alias)
			if r2.URL.RawPath != "" {
				r2.URL.RawPath = r2.URL.Path
			}
			next.ServeHTTP(w, r2)
			return
		}
		if path != prefix && !strings.HasPrefix(path, prefix+"/") {
			http.NotFound(w, r)
			return
		}
		rest := "/"
		if path != prefix {
			rest = strings.TrimPrefix(path, prefix)
		}
		// A remainder that still carries the prefix is a double-prefix request.
		if rest != "/" && (rest == prefix || strings.HasPrefix(rest, prefix+"/")) {
			http.NotFound(w, r)
			return
		}
		r2 := withOriginalPath(r)
		u2 := *r.URL
		r2.URL = &u2
		r2.URL.Path = rest
		if r2.URL.RawPath != "" {
			r2.URL.RawPath = rest
		}
		next.ServeHTTP(w, r2)
	})
}
