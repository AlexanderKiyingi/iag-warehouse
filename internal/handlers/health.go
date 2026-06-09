package handlers

import (
	"net/http"

	"github.com/alvor-technologies/iag-platform-go/apierr"
	"github.com/gin-gonic/gin"
)

func (a *API) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"service": a.Cfg.ServiceName,
	})
}

func (a *API) Ready(c *gin.Context) {
	if err := a.Store.Ping(c.Request.Context()); err != nil {
		apierr.Write(c, http.StatusServiceUnavailable, apierr.CodeServiceUnavailable, "database unavailable")
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ready"})
}
