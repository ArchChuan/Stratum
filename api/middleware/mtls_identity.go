package middleware

import (
	"crypto/x509"
	"net/http"

	"github.com/gin-gonic/gin"
)

const PlatformMCPWorkloadURI = "spiffe://stratum.local/ns/stratum/sa/stratum-platform-mcp"

func RequirePlatformMCPIdentity() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !hasPlatformMCPIdentity(c.Request) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "platform MCP client identity denied"})
			return
		}
		c.Next()
	}
}

func hasPlatformMCPIdentity(req *http.Request) bool {
	if req.TLS == nil || len(req.TLS.PeerCertificates) == 0 || len(req.TLS.VerifiedChains) == 0 {
		return false
	}
	return hasExactWorkloadURI(req.TLS.PeerCertificates[0])
}

func hasExactWorkloadURI(cert *x509.Certificate) bool {
	return cert != nil && len(cert.URIs) == 1 && cert.URIs[0].String() == PlatformMCPWorkloadURI
}
