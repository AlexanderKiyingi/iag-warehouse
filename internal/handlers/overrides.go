package handlers

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"iag-warehouse/backend/internal/middleware"
	"iag-warehouse/backend/internal/store"
)

// Override kinds live in the store package, next to the table they are written
// to. Aliased here so handler call sites read naturally.
const (
	OverrideItemStatus    = store.OverrideKindItemStatus
	OverrideFEFO          = store.OverrideKindFEFO
	OverrideNegativeStock = store.OverrideKindNegativeStock
	OverrideEmergency     = store.OverrideKindEmergency
	OverrideTolerance     = store.OverrideKindTolerance
	OverrideGate          = store.OverrideKindGate
	OverrideSoD           = store.OverrideKindSoD
)

type overrideRecord struct {
	Kind       string
	Subject    string
	FromState  string
	ToState    string
	Reason     string
	RefType    string
	RefID      string
	Permission string
}

// recordOverride appends to the override log and never fails the request.
//
// The alternative — propagating the error — means a full disk on the audit
// table stops stock moving. That trade is wrong for a warehouse: the log exists
// so somebody can review what happened, and a review of a factory that stopped
// is not the review anybody wanted. A failure is logged at error level, which
// is what the on-call alert watches.
func (a *API) recordOverride(c *gin.Context, r overrideRecord) {
	o := store.ControlOverride{
		Kind:       r.Kind,
		Subject:    r.Subject,
		FromState:  r.FromState,
		ToState:    r.ToState,
		Reason:     r.Reason,
		RefType:    r.RefType,
		RefID:      r.RefID,
		Permission: r.Permission,
	}
	if uid, ok := middleware.UserID(c); ok {
		o.ActorID = &uid
	}
	if claims, ok := middleware.PlatformClaims(c); ok && claims != nil {
		if claims.Email != "" {
			o.ActorName = claims.Email
		} else {
			o.ActorName = claims.Subject
		}
	}
	if _, err := a.Store.RecordControlOverride(c.Request.Context(), o); err != nil {
		slog.Error("control override not recorded",
			"kind", r.Kind, "subject", r.Subject, "actor", o.ActorName, "err", err)
	}
}

// ListControlOverrides handles GET /control-overrides — the exception report.
func (a *API) ListControlOverrides(c *gin.Context) {
	f := store.ListControlOverridesFilter{Kind: c.Query("kind")}
	if raw := c.Query("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			f.Limit = n
		}
	}
	// Default to the last 90 days. An exception report that opens on all of
	// history buries this month's three overrides under last year's hundred.
	since := time.Now().AddDate(0, 0, -90)
	if raw := c.Query("since"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			badRequest(c, "since must be an RFC3339 timestamp")
			return
		}
		since = parsed
	}
	f.Since = &since

	items, err := a.Store.ListControlOverrides(c.Request.Context(), f)
	if err != nil {
		storeErr(c, err)
		return
	}
	counts, err := a.Store.CountControlOverridesByKind(c.Request.Context(), since)
	if err != nil {
		storeErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"items":  items,
		"counts": counts,
		"since":  since,
	})
}
