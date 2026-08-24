package handlers

import (
	"encoding/json"
	"errors"
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

// storeErr maps a store error onto a status code. It matches with errors.Is
// rather than equality because the execution-layer paths wrap their sentinels
// with the specific reason ("fixed_bin needs a target_bin_code"), and that
// message is the most useful part of the response — losing it to a bare 500
// would turn every validation failure into a support ticket.
func storeErr(c *gin.Context, err error) {
	c.JSON(statusForStoreErr(err), gin.H{"error": messageForStoreErr(err)})
}

// statusForStoreErr maps a store sentinel onto a status code. Matching is by
// errors.Is rather than equality because the execution-layer paths wrap their
// sentinels with the specific reason, and handlers running inside
// withIdempotency need the code without writing the response themselves.
func statusForStoreErr(err error) int {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, store.ErrInvalidArgument):
		return http.StatusBadRequest
	case errors.Is(err, store.ErrForbidden):
		return http.StatusForbidden
	// A putaway that finds nowhere to go is not a bad request — the warehouse is
	// genuinely full — so it gets a conflict a client can tell apart from a
	// malformed payload.
	case errors.Is(err, store.ErrConflict), errors.Is(err, store.ErrNoPutawayBin):
		return http.StatusConflict
	case errors.Is(err, store.ErrInsufficientStock), errors.Is(err, store.ErrStockNotAvailable):
		return http.StatusUnprocessableEntity
	default:
		// A lifecycle refusal is a well-formed request about stock that may not
		// move — the same shape as insufficient stock, not a malformed payload.
		var statusErr *store.ItemStatusError
		if errors.As(err, &statusErr) {
			return http.StatusUnprocessableEntity
		}
		return http.StatusInternalServerError
	}
}

// messageForStoreErr keeps the wrapped reason where there is one, since "max_qty
// must be at least min_qty" is worth far more to a caller than "conflict", and
// substitutes a fixed phrase for the bare sentinels that carry no detail.
func messageForStoreErr(err error) string {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return "not found"
	case errors.Is(err, store.ErrNoPutawayBin):
		return "no putaway bin available with room for this quantity"
	case errors.Is(err, store.ErrInsufficientStock):
		return "insufficient stock"
	case errors.Is(err, store.ErrStockNotAvailable):
		return "stock on QC hold or damaged"
	default:
		return err.Error()
	}
}
