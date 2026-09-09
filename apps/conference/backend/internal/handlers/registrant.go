package handlers

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"net/http/httputil"
	"net/url"
	"github.com/gin-gonic/gin"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
	"wso2-coin-backend/internal/config"
)

// RegistrantProxyHandler returns a reverse proxy that forwards requests to the registrant service.
// It preserves the /registrant path prefix, handles X-Forwarded headers, and injects the Choreo
// OAuth2 service-to-service token for production.
func RegistrantProxyHandler(cfg config.ExternalServiceConfig) gin.HandlerFunc {
	remote, err := url.Parse(cfg.Endpoint)
	if err != nil {
		slog.Error("failed to parse registrant service url", "error", err)
		return func(c *gin.Context) {
			c.JSON(http.StatusInternalServerError, gin.H{"message": "internal gateway error"})
		}
	}

	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(remote)
			pr.SetXForwarded()
			// Preserve the full original path including /registrant
			pr.Out.URL.Path = "/" + strings.TrimPrefix(pr.In.URL.Path, "/registrant/")
		},
		ModifyResponse: func(resp *http.Response) error {
			// Strip CORS headers from the downstream response to prevent duplicate
			// header errors in the browser, since the Conference backend's Gin
			// middleware already adds its own CORS headers locally.
			resp.Header.Del("Access-Control-Allow-Origin")
			resp.Header.Del("Access-Control-Allow-Methods")
			resp.Header.Del("Access-Control-Allow-Headers")
			return nil
		},
	}

	// Create a lazy OAuth2 client source for the proxy Transport
	// This matches how internal/clients/wallet/client.go creates credentials.
	if cfg.OAuth.ClientID != "" && cfg.OAuth.ClientSecret != "" {
		oauthCfg := clientcredentials.Config{
			ClientID:     cfg.OAuth.ClientID,
			ClientSecret: cfg.OAuth.ClientSecret,
			TokenURL:     cfg.OAuth.TokenURL,
		}
		
		// The proxy Transport is responsible for fetching and attaching the Bearer token
		ctx := context.Background()
		proxy.Transport = &oauth2Transport{
			source: oauthCfg.TokenSource(ctx),
			base:   http.DefaultTransport,
		}
	}

	return func(c *gin.Context) {
		proxy.ServeHTTP(c.Writer, c.Request)
	}
}

// oauth2Transport intercepts the reverse proxy's outgoing request, fetches a fresh token,
// and injects it as the Authorization header.
type oauth2Transport struct {
	source oauth2.TokenSource
	base   http.RoundTripper
}

func (t *oauth2Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	token, err := t.source.Token()
	if err != nil {
		return nil, err
	}
	
	// Clone the request to modify headers safely
	req2 := req.Clone(req.Context())
	token.SetAuthHeader(req2)
	
	return t.base.RoundTrip(req2)
}
