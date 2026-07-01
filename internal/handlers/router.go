package handlers

import (
	"net/http"
	"strings"

	"github.com/alvor-technologies/iag-platform-go/middleware"
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"

	"iag-warehouse/backend/internal/auditlog"
	appmw "iag-warehouse/backend/internal/middleware"
)

type RouterDeps struct {
	API          *API
	Audit        *auditlog.Store
	PlatformAuth *appmw.PlatformAuth
	CORSOrigins  []string
	StrictRBAC   bool
}

func NewRouter(deps RouterDeps) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(otelgin.Middleware(deps.API.Cfg.ServiceName))
	r.Use(gin.Recovery())
	r.Use(middleware.RequestID())
	r.Use(securityHeaders())
	r.Use(corsMiddleware(deps.CORSOrigins))

	api := deps.API
	if deps.PlatformAuth != nil {
		r.Use(deps.PlatformAuth.AttachPrincipal())
	}
	r.Use(appmw.RequestAudit(deps.Audit))

	r.GET("/health", api.Health)
	r.GET("/healthz", api.Health)
	r.GET("/ready", api.Ready)

	v1 := r.Group("/api/v1")
	if deps.PlatformAuth != nil {
		v1.Use(deps.PlatformAuth.RequireAuth())
	}
	if deps.StrictRBAC {
		v1.Use(appmw.StrictRBAC())
	}
	{
		v1.GET("/platform/status", appmw.RequireStaff(), api.PlatformStatus)
		v1.GET("/bootstrap", appmw.RequirePermission("warehouse.view_overview"), api.Bootstrap)

		v1.GET("/facilities", appmw.RequirePermission("warehouse.view_location"), api.ListFacilities)
		v1.POST("/facilities", appmw.RequirePermission("warehouse.add_location"), api.CreateFacility)
		v1.GET("/facilities/:code", appmw.RequirePermission("warehouse.view_location"), api.GetFacility)
		v1.PATCH("/facilities/:code", appmw.RequirePermission("warehouse.change_location"), api.PatchFacility)
		v1.GET("/facilities/:code/zones", appmw.RequirePermission("warehouse.view_location"), api.ListZones)
		v1.POST("/facilities/:code/zones", appmw.RequirePermission("warehouse.add_location"), api.CreateZone)
		v1.PATCH("/zones/:code", appmw.RequirePermission("warehouse.change_location"), api.PatchZone)
		v1.GET("/zones/:code/bins", appmw.RequirePermission("warehouse.view_location"), api.ListBins)
		v1.POST("/zones/:code/bins", appmw.RequirePermission("warehouse.add_location"), api.CreateBin)
		v1.PATCH("/bins/:code", appmw.RequirePermission("warehouse.change_location"), api.PatchBin)
		v1.GET("/bins/:code/stock", appmw.RequirePermission("warehouse.view_stock"), api.BinStock)

		// view_item / view_stock and issue_consumable are reachable by peer
		// services (iag-fleet queries availability and issues parts on WO
		// completion), so they use RequireServiceOrPermission.
		v1.GET("/items", appmw.RequireServiceOrPermission("warehouse.view_item"), api.ListItems)
		v1.POST("/items", appmw.RequirePermission("warehouse.add_item"), api.CreateItem)
		v1.GET("/items/:id", appmw.RequireServiceOrPermission("warehouse.view_item"), api.GetItem)
		v1.PATCH("/items/:id", appmw.RequirePermission("warehouse.change_item"), api.PatchItem)
		v1.GET("/items/:id/balances", appmw.RequireServiceOrPermission("warehouse.view_stock"), api.ItemBalances)

		v1.GET("/receipts", appmw.RequirePermission("warehouse.view_receipt"), api.ListReceipts)
		v1.GET("/receipts/:id", appmw.RequirePermission("warehouse.view_receipt"), api.GetReceipt)
		v1.POST("/receipts", appmw.RequirePermission("warehouse.add_receipt"), api.CreateReceipt)
		v1.POST("/receipts/from-grn", appmw.RequirePermission("warehouse.add_receipt"), api.CreateReceiptFromGRN)
		v1.POST("/receipts/:id/post", appmw.RequirePermission("warehouse.post_receipt"), api.PostReceipt)

		v1.GET("/issues", appmw.RequirePermission("warehouse.view_issue"), api.ListIssues)
		v1.GET("/issues/:id", appmw.RequirePermission("warehouse.view_issue"), api.GetIssue)
		v1.POST("/issues", appmw.RequirePermission("warehouse.add_issue"), api.CreateIssue)
		v1.POST("/issues/:id/post", appmw.RequirePermission("warehouse.post_issue"), api.PostIssue)
		v1.POST("/issues/for-department", appmw.RequireServiceOrPermission("warehouse.issue_consumable"), api.IssueForDepartment)

		// Reachable by peer services (iag-mes posts BOM backflush + finished-goods
		// output on run completion), so these use RequireServiceOrPermission.
		v1.POST("/production/consume", appmw.RequireServiceOrPermission("warehouse.production_consume"), api.ProductionConsume)
		v1.POST("/production/output", appmw.RequireServiceOrPermission("warehouse.production_output"), api.ProductionOutput)

		v1.POST("/transfers", appmw.RequirePermission("warehouse.add_transfer"), api.CreateTransfer)
		v1.POST("/adjustments", appmw.RequirePermission("warehouse.adjust_stock"), api.CreateAdjustment)
		v1.POST("/cycle-counts", appmw.RequirePermission("warehouse.cycle_count"), api.CreateCycleCount)

		v1.GET("/assets", appmw.RequirePermission("warehouse.view_asset"), api.ListAssets)
		v1.POST("/assets", appmw.RequirePermission("warehouse.add_asset"), api.CreateAsset)
		v1.POST("/assets/:tag/check-in", appmw.RequirePermission("warehouse.checkin_asset"), api.CheckInAsset)
		v1.POST("/assets/:tag/check-out", appmw.RequirePermission("warehouse.checkout_asset"), api.CheckOutAsset)
		v1.POST("/assets/:tag/dispose", appmw.RequirePermission("warehouse.dispose_asset"), api.DisposeAsset)
		v1.GET("/asset-disposals", appmw.RequirePermission("warehouse.view_asset"), api.ListDisposals)
		v1.GET("/asset-disposals/approval-tiers", appmw.RequirePermission("warehouse.view_asset"), api.ListDisposalApprovalTiers)
		// Approval routes use a low coarse gate (view_asset); the real authority is
		// the per-tier permission checked inside, so a tier approver who is not a
		// disposer can still sign (segregation of duties).
		v1.POST("/asset-disposals/:id/approve", appmw.RequirePermission("warehouse.view_asset"), api.ApproveDisposal)
		v1.POST("/asset-disposals/:id/reject", appmw.RequirePermission("warehouse.view_asset"), api.RejectDisposal)
		v1.GET("/spare-parts/low-stock", appmw.RequireServiceOrPermission("warehouse.view_stock"), api.LowStock)
		v1.GET("/spare-parts/by-asset/:asset_tag", appmw.RequireServiceOrPermission("warehouse.view_item"), api.SparePartsByAsset)
		v1.GET("/spare-compat", appmw.RequirePermission("warehouse.view_item"), api.ListSpareCompat)
		v1.POST("/spare-compat", appmw.RequirePermission("warehouse.change_item"), api.CreateSpareCompat)
		v1.DELETE("/spare-compat/:id", appmw.RequirePermission("warehouse.change_item"), api.DeleteSpareCompat)

		// Flat stores-domain records (migration 010): alerts, returns, gate
		// passes, warranties, event requests — backing the storesiag views.
		v1.GET("/stock-thresholds", appmw.RequirePermission("warehouse.view_threshold"), api.ListThresholds)
		v1.POST("/stock-thresholds", appmw.RequirePermission("warehouse.add_threshold"), api.CreateThreshold)
		v1.PATCH("/stock-thresholds/:id", appmw.RequirePermission("warehouse.change_threshold"), api.UpdateThreshold)
		v1.DELETE("/stock-thresholds/:id", appmw.RequirePermission("warehouse.change_threshold"), api.DeleteThreshold)

		v1.GET("/returns", appmw.RequirePermission("warehouse.view_return"), api.ListReturns)
		v1.POST("/returns", appmw.RequirePermission("warehouse.add_return"), api.CreateReturn)
		v1.PATCH("/returns/:id", appmw.RequirePermission("warehouse.change_return"), api.UpdateReturn)
		v1.DELETE("/returns/:id", appmw.RequirePermission("warehouse.change_return"), api.DeleteReturn)

		v1.GET("/gate-passes", appmw.RequirePermission("warehouse.view_gatepass"), api.ListGatePasses)
		v1.POST("/gate-passes", appmw.RequirePermission("warehouse.add_gatepass"), api.CreateGatePass)
		v1.PATCH("/gate-passes/:id", appmw.RequirePermission("warehouse.change_gatepass"), api.UpdateGatePass)
		v1.POST("/gate-passes/:id/return", appmw.RequirePermission("warehouse.change_gatepass"), api.ReturnGatePass)
		v1.DELETE("/gate-passes/:id", appmw.RequirePermission("warehouse.change_gatepass"), api.DeleteGatePass)

		v1.GET("/warranties", appmw.RequirePermission("warehouse.view_warranty"), api.ListWarranties)
		v1.POST("/warranties", appmw.RequirePermission("warehouse.add_warranty"), api.CreateWarranty)
		v1.PATCH("/warranties/:id", appmw.RequirePermission("warehouse.change_warranty"), api.UpdateWarranty)
		v1.DELETE("/warranties/:id", appmw.RequirePermission("warehouse.change_warranty"), api.DeleteWarranty)

		v1.GET("/event-requests", appmw.RequirePermission("warehouse.view_event"), api.ListEventRequests)
		v1.POST("/event-requests", appmw.RequirePermission("warehouse.add_event"), api.CreateEventRequest)
		v1.PATCH("/event-requests/:id", appmw.RequirePermission("warehouse.change_event"), api.UpdateEventRequest)
		v1.DELETE("/event-requests/:id", appmw.RequirePermission("warehouse.change_event"), api.DeleteEventRequest)

		v1.GET("/small-tools", appmw.RequirePermission("warehouse.view_tool"), api.ListSmallTools)
		v1.POST("/small-tools", appmw.RequirePermission("warehouse.add_tool"), api.CreateSmallTool)
		v1.PATCH("/small-tools/:id", appmw.RequirePermission("warehouse.change_tool"), api.UpdateSmallTool)
		v1.DELETE("/small-tools/:id", appmw.RequirePermission("warehouse.change_tool"), api.DeleteSmallTool)

		v1.GET("/movements", appmw.RequirePermission("warehouse.view_stock"), api.ListMovements)
		v1.GET("/pick-lists", appmw.RequirePermission("warehouse.view_stock"), api.ListPickLists)
		v1.GET("/pick-lists/:id", appmw.RequirePermission("warehouse.view_stock"), api.GetPickList)
		v1.POST("/pick-lists", appmw.RequirePermission("warehouse.add_pick"), api.CreatePickList)
		v1.POST("/pick-lists/:id/confirm", appmw.RequirePermission("warehouse.confirm_pick"), api.ConfirmPickList)
		v1.POST("/pick-lists/:id/cancel", appmw.RequirePermission("warehouse.confirm_pick"), api.CancelPickList)
		v1.POST("/pack-sessions", appmw.RequirePermission("warehouse.add_pack"), api.CreatePackSession)

		admin := v1.Group("/admin")
		admin.Use(appmw.RequirePermission("warehouse.admin.read"))
		{
			admin.GET("/audit-logs", api.ListAPIAuditLogs)
			admin.GET("/monitoring/summary", api.AdminMonitoringSummary)
			admin.GET("/monitoring/activity", api.AdminMonitoringActivity)
		}
	}

	return r
}

func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Next()
	}
}

func corsMiddleware(allowed []string) gin.HandlerFunc {
	allowSet := map[string]bool{}
	for _, o := range allowed {
		if t := strings.TrimSpace(o); t != "" {
			allowSet[t] = true
		}
	}
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" && allowSet[origin] {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Vary", "Origin")
			c.Header("Access-Control-Allow-Credentials", "true")
			c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Request-ID, Idempotency-Key")
		}
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
