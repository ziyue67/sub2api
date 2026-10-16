package repository

import (
	"github.com/Wei-Shaw/sub2api/internal/regional/control"
	"net/http"
	"strings"
)

func (c *regionalConcurrencyCache) withFundsControl(fallback http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, control.Prefix+"/quota/") {
			if funds := c.fundsHandler.Load(); funds != nil {
				(*funds).ServeHTTP(w, r)
				return
			}
		}
		fallback.ServeHTTP(w, r)
	})
}
