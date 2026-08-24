package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// ValuationHealth handles GET /admin/valuation-health.
//
// Principle P5 says a quantity movement and its financial posting are one
// transaction. The mechanism for that is built — the movement event is enqueued
// into the outbox inside the same transaction as the stock write — but the
// valuation it carries is zero unless costing is switched on, and finance's
// consumer no-ops on a zero-cost movement. The failure mode is therefore
// silent: every request succeeds, stock is correct, and the general ledger
// simply never hears about any of it.
//
// This endpoint exists to make that state loud. It answers three questions an
// operator or a controller actually asks — is costing on, how much movement
// went through unvalued, and which items would value at zero if it were
// switched on right now — so that enabling it is a decision made with the
// numbers in view rather than a flag flipped hopefully.
func (a *API) ValuationHealth(c *gin.Context) {
	ctx := c.Request.Context()
	since := time.Now().AddDate(0, 0, -30)
	if raw := c.Query("since"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			badRequest(c, "since must be an RFC3339 timestamp")
			return
		}
		since = parsed
	}

	summary, err := a.Store.ValuationHealth(ctx, since)
	if err != nil {
		storeErr(c, err)
		return
	}

	enabled := a.Cfg.InventoryCostingEnabled
	// Say what it means, not just what it is. "costing_enabled: false" reads as
	// a setting; the sentence reads as a consequence, and the consequence is
	// what gets it fixed.
	verdict := "Costing is on. Valued movements are reaching finance."
	switch {
	case !enabled:
		verdict = "Costing is OFF: stock moves but no value reaches finance, so the GL " +
			"inventory account does not follow this warehouse. Set INVENTORY_COSTING_ENABLED=true."
	case summary.UnvaluedMovements > 0:
		verdict = "Costing is on, but some movements carried no value — usually an item " +
			"with no average cost yet, which values its issues at zero. Check items_without_cost."
	}

	c.JSON(http.StatusOK, gin.H{
		"costing_enabled":     enabled,
		"base_currency":       a.Cfg.BaseCurrency,
		"since":               since,
		"movements":           summary.TotalMovements,
		"valued_movements":    summary.ValuedMovements,
		"unvalued_movements":  summary.UnvaluedMovements,
		"valuation_delta":     summary.ValuationDelta,
		"items_without_cost":  summary.ItemsWithoutCost,
		"stock_value_on_hand": summary.StockValueOnHand,
		"verdict":             verdict,
	})
}
