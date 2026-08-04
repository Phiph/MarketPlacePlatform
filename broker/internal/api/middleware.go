package api

import (
	"context"
	"net/http"
	"strings"

	"marketplace-broker/internal/tenant"
)

type contextKey string

const teamContextKey contextKey = "team"

// withAuth resolves the caller's team from a Bearer API key and rejects the
// request if it's missing or unrecognised. This stands in for real authn
// (see internal/tenant) - fine for a local demo, not production.
func withAuth(dir *tenant.Directory, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		apiKey, ok := strings.CutPrefix(auth, "Bearer ")
		if !ok || apiKey == "" {
			writeError(w, http.StatusUnauthorized, "missing bearer token")
			return
		}

		team, ok := dir.Resolve(apiKey)
		if !ok {
			writeError(w, http.StatusUnauthorized, "invalid API key")
			return
		}

		ctx := context.WithValue(r.Context(), teamContextKey, team)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func teamFromContext(ctx context.Context) string {
	team, _ := ctx.Value(teamContextKey).(string)
	return team
}
