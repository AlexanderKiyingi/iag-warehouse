package handlers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"iag-warehouse/backend/internal/models"
)

// Handlers for the flat stores-domain record tables (migration 010): alerts,
// returns, gate passes, warranties, and event requests. JSON bodies use the
// model's snake_case tags; the storesiag write resources send matching keys.

func parsePathID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		badRequest(c, "invalid id")
		return uuid.Nil, false
	}
	return id, true
}

// --- thresholds (alerts) ----------------------------------------------------

func (a *API) ListThresholds(c *gin.Context) {
	items, err := a.Store.ListThresholds(c.Request.Context())
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, gin.H{"items": items})
}

func (a *API) CreateThreshold(c *gin.Context) {
	var body models.StockThreshold
	if err := bindJSONCoerced(c, &body); err != nil || strings.TrimSpace(body.Item) == "" {
		badRequest(c, "item is required")
		return
	}
	a.withIdempotency(c, func() (int, any) {
		row, err := a.Store.CreateThreshold(c.Request.Context(), body)
		if err != nil {
			return http.StatusInternalServerError, gin.H{"error": err.Error()}
		}
		return http.StatusCreated, row
	})
}

func (a *API) UpdateThreshold(c *gin.Context) {
	id, okID := parsePathID(c)
	if !okID {
		return
	}
	var body models.StockThreshold
	if err := bindJSONCoerced(c, &body); err != nil {
		badRequest(c, "invalid JSON")
		return
	}
	row, err := a.Store.UpdateThreshold(c.Request.Context(), id, body)
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, row)
}

func (a *API) DeleteThreshold(c *gin.Context) {
	id, okID := parsePathID(c)
	if !okID {
		return
	}
	if err := a.Store.DeleteThreshold(c.Request.Context(), id); err != nil {
		storeErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// --- returns ----------------------------------------------------------------

func (a *API) ListReturns(c *gin.Context) {
	items, err := a.Store.ListReturns(c.Request.Context())
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, gin.H{"items": items})
}

func (a *API) CreateReturn(c *gin.Context) {
	var body models.StockReturn
	if err := bindJSONCoerced(c, &body); err != nil || strings.TrimSpace(body.Item) == "" {
		badRequest(c, "item is required")
		return
	}
	a.withIdempotency(c, func() (int, any) {
		row, err := a.Store.CreateReturn(c.Request.Context(), body)
		if err != nil {
			return http.StatusInternalServerError, gin.H{"error": err.Error()}
		}
		return http.StatusCreated, row
	})
}

func (a *API) UpdateReturn(c *gin.Context) {
	id, okID := parsePathID(c)
	if !okID {
		return
	}
	var body models.StockReturn
	if err := bindJSONCoerced(c, &body); err != nil {
		badRequest(c, "invalid JSON")
		return
	}
	row, err := a.Store.UpdateReturn(c.Request.Context(), id, body)
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, row)
}

func (a *API) DeleteReturn(c *gin.Context) {
	id, okID := parsePathID(c)
	if !okID {
		return
	}
	if err := a.Store.DeleteReturn(c.Request.Context(), id); err != nil {
		storeErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// --- gate passes ------------------------------------------------------------

func (a *API) ListGatePasses(c *gin.Context) {
	items, err := a.Store.ListGatePasses(c.Request.Context())
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, gin.H{"items": items})
}

func (a *API) CreateGatePass(c *gin.Context) {
	var body models.GatePass
	if err := bindJSONCoerced(c, &body); err != nil || strings.TrimSpace(body.Items) == "" {
		badRequest(c, "items is required")
		return
	}
	a.withIdempotency(c, func() (int, any) {
		row, err := a.Store.CreateGatePass(c.Request.Context(), body)
		if err != nil {
			return http.StatusInternalServerError, gin.H{"error": err.Error()}
		}
		return http.StatusCreated, row
	})
}

func (a *API) UpdateGatePass(c *gin.Context) {
	id, okID := parsePathID(c)
	if !okID {
		return
	}
	var body models.GatePass
	if err := bindJSONCoerced(c, &body); err != nil {
		badRequest(c, "invalid JSON")
		return
	}
	row, err := a.Store.UpdateGatePass(c.Request.Context(), id, body)
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, row)
}

func (a *API) ReturnGatePass(c *gin.Context) {
	id, okID := parsePathID(c)
	if !okID {
		return
	}
	var body struct {
		ReturnDate string `json:"return_date"`
	}
	_ = c.ShouldBindJSON(&body)
	row, err := a.Store.ReturnGatePass(c.Request.Context(), id, body.ReturnDate)
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, row)
}

func (a *API) DeleteGatePass(c *gin.Context) {
	id, okID := parsePathID(c)
	if !okID {
		return
	}
	if err := a.Store.DeleteGatePass(c.Request.Context(), id); err != nil {
		storeErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// --- warranties -------------------------------------------------------------

func (a *API) ListWarranties(c *gin.Context) {
	items, err := a.Store.ListWarranties(c.Request.Context())
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, gin.H{"items": items})
}

func (a *API) CreateWarranty(c *gin.Context) {
	var body models.Warranty
	if err := bindJSONCoerced(c, &body); err != nil || strings.TrimSpace(body.Item) == "" {
		badRequest(c, "item is required")
		return
	}
	a.withIdempotency(c, func() (int, any) {
		row, err := a.Store.CreateWarranty(c.Request.Context(), body)
		if err != nil {
			return http.StatusInternalServerError, gin.H{"error": err.Error()}
		}
		return http.StatusCreated, row
	})
}

func (a *API) UpdateWarranty(c *gin.Context) {
	id, okID := parsePathID(c)
	if !okID {
		return
	}
	var body models.Warranty
	if err := bindJSONCoerced(c, &body); err != nil {
		badRequest(c, "invalid JSON")
		return
	}
	row, err := a.Store.UpdateWarranty(c.Request.Context(), id, body)
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, row)
}

func (a *API) DeleteWarranty(c *gin.Context) {
	id, okID := parsePathID(c)
	if !okID {
		return
	}
	if err := a.Store.DeleteWarranty(c.Request.Context(), id); err != nil {
		storeErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// --- event requests ---------------------------------------------------------

func (a *API) ListEventRequests(c *gin.Context) {
	items, err := a.Store.ListEventRequests(c.Request.Context())
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, gin.H{"items": items})
}

func (a *API) CreateEventRequest(c *gin.Context) {
	var body models.EventRequest
	if err := bindJSONCoerced(c, &body); err != nil || strings.TrimSpace(body.EventName) == "" {
		badRequest(c, "event_name is required")
		return
	}
	a.withIdempotency(c, func() (int, any) {
		row, err := a.Store.CreateEventRequest(c.Request.Context(), body)
		if err != nil {
			return http.StatusInternalServerError, gin.H{"error": err.Error()}
		}
		return http.StatusCreated, row
	})
}

func (a *API) UpdateEventRequest(c *gin.Context) {
	id, okID := parsePathID(c)
	if !okID {
		return
	}
	var body models.EventRequest
	if err := bindJSONCoerced(c, &body); err != nil {
		badRequest(c, "invalid JSON")
		return
	}
	row, err := a.Store.UpdateEventRequest(c.Request.Context(), id, body)
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, row)
}

func (a *API) DeleteEventRequest(c *gin.Context) {
	id, okID := parsePathID(c)
	if !okID {
		return
	}
	if err := a.Store.DeleteEventRequest(c.Request.Context(), id); err != nil {
		storeErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// --- small tools ------------------------------------------------------------

func (a *API) ListSmallTools(c *gin.Context) {
	items, err := a.Store.ListSmallTools(c.Request.Context())
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, gin.H{"items": items})
}

func (a *API) CreateSmallTool(c *gin.Context) {
	var body models.SmallTool
	if err := bindJSONCoerced(c, &body); err != nil || strings.TrimSpace(body.Name) == "" {
		badRequest(c, "name is required")
		return
	}
	a.withIdempotency(c, func() (int, any) {
		row, err := a.Store.CreateSmallTool(c.Request.Context(), body)
		if err != nil {
			return http.StatusInternalServerError, gin.H{"error": err.Error()}
		}
		return http.StatusCreated, row
	})
}

func (a *API) UpdateSmallTool(c *gin.Context) {
	id, okID := parsePathID(c)
	if !okID {
		return
	}
	var body models.SmallTool
	if err := bindJSONCoerced(c, &body); err != nil {
		badRequest(c, "invalid JSON")
		return
	}
	row, err := a.Store.UpdateSmallTool(c.Request.Context(), id, body)
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, row)
}

func (a *API) DeleteSmallTool(c *gin.Context) {
	id, okID := parsePathID(c)
	if !okID {
		return
	}
	if err := a.Store.DeleteSmallTool(c.Request.Context(), id); err != nil {
		storeErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
