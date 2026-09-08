package handlers

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"log/slog"
	"github.com/gin-gonic/gin"
)

// RegistrantProxyHandler forwards requests to the registrant microservice.
func RegistrantProxyHandler(targetURL string) gin.HandlerFunc {
	remote, err := url.Parse(targetURL)
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
			pr.Out.Host = remote.Host
		},
	}

	return func(c *gin.Context) {
		proxy.ServeHTTP(c.Writer, c.Request)
	}
}
