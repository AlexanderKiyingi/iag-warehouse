package middleware

import (
	"github.com/alvor-technologies/iag-platform-go/apierr"
	"github.com/gin-gonic/gin"
)

// RequireStaff gates operator endpoints (platform status, cross-service probes).
func RequireStaff() gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := PlatformClaims(c)
		if !ok || claims == nil {
			apierr.Unauthorized(c, "authentication required")
			return
		}
		if !claims.IsStaff && !claims.IsSuperuser {
			apierr.Forbidden(c, "staff access required")
			return
		}
		c.Next()
	}
}
