package controllers

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"k8s.io/klog/v2"
)

// NewPassThroughHandler creates a new HTTP handler that proxies requests to the
// appropriate controller endpoint based on the discovery information.
//
// Note that this assume the target endpoints do not contain path segments; that
// is, the endpoint URLs will be of the form "http://localhost:1234/" and not
// "http://localhost:1234/path".
func NewPassThroughHandler(discovery *ControllerManagerDiscovery) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log := klog.FromContext(r.Context())
		endpoint, _, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")

		target, err := discovery.LookupPassThroughEndpoint(r.Context(), endpoint)
		if err != nil {
			log.V(5).Info("Pass through endpoint not found", "endpoint", endpoint)
			http.NotFound(w, r)
			return
		}

		targetURL, err := url.Parse(target)
		if err != nil {
			log.V(5).Info("Failed to parse pass through target URL",
				"endpoint", endpoint, "target", target, "error", err)
			http.Error(w, "Bad target URL", http.StatusInternalServerError)
			return
		}

		log.V(5).Info("Proxying pass through request", "endpoint", endpoint, "target", target)
		httputil.NewSingleHostReverseProxy(targetURL).ServeHTTP(w, r)
	})
}
