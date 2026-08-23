package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"iag-warehouse/backend/internal/middleware"
	"iag-warehouse/backend/internal/models"
)

// callerHolds reports whether the signed-in caller holds a permission, without
// aborting the request.
//
// middleware.RequirePermission answers the same question by refusing the call,
// which is right for gating a route and wrong for a modifier like the item
// status override — there the answer changes what the request may contain, not
// whether it is allowed at all.
//
// Absent claims mean the deployment is not running strict RBAC (a local stack,
// or a service-to-service caller). Those already pass RequirePermission, so
// answering false here would make the override the one permission an
// unauthenticated local stack could not exercise.
func callerHolds(c *gin.Context, code string) bool {
	claims, ok := middleware.PlatformClaims(c)
	if !ok {
		return true
	}
	return claims.IsSuperuser || claims.IsStaff || claims.HasPermission(code)
}

// PermOverrideItemStatus lifts the `restricted` item status for one request.
const PermOverrideItemStatus = "warehouse.override_item_status"

// SetItemStatus handles PATCH /items/:id/status.
//
// Status is deliberately not settable through the general item update: it is
// the difference between a part that can be bought and one that cannot, and it
// carries its own permission for that reason. Folding it into PATCH /items
// would mean anyone who can correct a typo in a description can also retire the
// part.
func (a *API) SetItemStatus(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		badRequest(c, "invalid item id")
		return
	}
	var body struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	if err := bindJSONCoerced(c, &body); err != nil {
		badRequest(c, err.Error())
		return
	}
	if !models.ValidItemStatus(body.Status) {
		badRequest(c, "status must be one of draft, active, restricted, obsolete, blocked")
		return
	}

	before, err := a.Store.GetItem(c.Request.Context(), id)
	if err != nil {
		storeErr(c, err)
		return
	}
	item, err := a.Store.SetItemStatus(c.Request.Context(), id, body.Status)
	if err != nil {
		storeErr(c, err)
		return
	}

	// Taking a part out of circulation, or putting one back, is the kind of
	// decision somebody asks about six months later. Record who and why.
	a.recordOverride(c, overrideRecord{
		Kind:       OverrideItemStatus,
		Subject:    item.SKU,
		FromState:  before.Status,
		ToState:    item.Status,
		Reason:     body.Reason,
		RefType:    "item",
		RefID:      item.ID.String(),
		Permission: "warehouse.change_item_status",
	})

	c.JSON(http.StatusOK, item)
}
