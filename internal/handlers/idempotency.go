package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"

	"iag-warehouse/backend/internal/middleware"
	"iag-warehouse/backend/internal/store"
)

func idempotencyKey(c *gin.Context) string {
	return c.GetHeader("Idempotency-Key")
}

func (a *API) withIdempotency(c *gin.Context, fn func() (int, any)) {
	key := idempotencyKey(c)
	if key == "" {
		status, resp := fn()
		c.JSON(status, resp)
		return
	}
	uid, ok := middleware.UserID(c)
	if !ok {
		status, resp := fn()
		c.JSON(status, resp)
		return
	}
	if rec, found, err := a.Store.GetIdempotency(c.Request.Context(), uid, key); err == nil && found {
		var body any
		_ = json.Unmarshal(rec.Body, &body)
		c.JSON(rec.StatusCode, body)
		return
	}
	status, resp := fn()
	_ = a.Store.SaveIdempotency(c.Request.Context(), uid, key, c.FullPath(), status, resp)
	c.JSON(status, resp)
}

func created(c *gin.Context, obj any) {
	c.JSON(http.StatusCreated, obj)
}

func ok(c *gin.Context, obj any) {
	c.JSON(http.StatusOK, obj)
}

func notFound(c *gin.Context, msg string) {
	c.JSON(http.StatusNotFound, gin.H{"error": msg})
}

func badRequest(c *gin.Context, msg string) {
	c.JSON(http.StatusBadRequest, gin.H{"error": msg})
}

func conflict(c *gin.Context, msg string) {
	c.JSON(http.StatusConflict, gin.H{"error": msg})
}

func storeErr(c *gin.Context, err error) {
	switch err {
	case store.ErrNotFound:
		notFound(c, "not found")
	case store.ErrConflict:
		conflict(c, "conflict")
	case store.ErrInsufficientStock:
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "insufficient stock"})
	case store.ErrStockNotAvailable:
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "stock on QC hold or damaged"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}
