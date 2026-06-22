package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (a *API) ListFacilities(c *gin.Context) {
	items, err := a.Store.ListFacilities(c.Request.Context())
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, gin.H{"items": items})
}

func (a *API) CreateFacility(c *gin.Context) {
	var body struct {
		Code     string         `json:"code"`
		Name     string         `json:"name"`
		SiteType string         `json:"site_type"`
		Attrs    map[string]any `json:"attrs"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Code == "" || body.Name == "" || body.SiteType == "" {
		badRequest(c, "code, name, and site_type are required")
		return
	}
	a.withIdempotency(c, func() (int, any) {
		f, err := a.Store.CreateFacility(c.Request.Context(), body.Code, body.Name, body.SiteType, body.Attrs)
		if err != nil {
			return http.StatusInternalServerError, gin.H{"error": err.Error()}
		}
		return http.StatusCreated, f
	})
}

func (a *API) GetFacility(c *gin.Context) {
	f, err := a.Store.GetFacilityByCode(c.Request.Context(), c.Param("code"))
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, f)
}

func (a *API) PatchFacility(c *gin.Context) {
	var body struct {
		Name     *string        `json:"name"`
		SiteType *string        `json:"site_type"`
		Attrs    map[string]any `json:"attrs"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		badRequest(c, "invalid JSON")
		return
	}
	f, err := a.Store.UpdateFacility(c.Request.Context(), c.Param("code"), body.Name, body.SiteType, body.Attrs)
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, f)
}

func (a *API) ListZones(c *gin.Context) {
	items, err := a.Store.ListZones(c.Request.Context(), c.Param("code"))
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, gin.H{"items": items})
}

func (a *API) CreateZone(c *gin.Context) {
	var body struct {
		Code     string         `json:"code"`
		Name     string         `json:"name"`
		ZoneType string         `json:"zone_type"`
		Attrs    map[string]any `json:"attrs"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Code == "" || body.ZoneType == "" {
		badRequest(c, "code and zone_type are required")
		return
	}
	a.withIdempotency(c, func() (int, any) {
		z, err := a.Store.CreateZone(c.Request.Context(), c.Param("code"), body.Code, body.Name, body.ZoneType, body.Attrs)
		if err != nil {
			return http.StatusInternalServerError, gin.H{"error": err.Error()}
		}
		return http.StatusCreated, z
	})
}

func (a *API) ListBins(c *gin.Context) {
	items, err := a.Store.ListBinsByZone(c.Request.Context(), c.Param("code"))
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, gin.H{"items": items})
}

func (a *API) CreateBin(c *gin.Context) {
	var body struct {
		Code            string         `json:"code"`
		CapacityKg      *float64       `json:"capacity_kg"`
		TemperatureBand *string        `json:"temperature_band"`
		Attrs           map[string]any `json:"attrs"`
	}
	if err := bindJSONCoerced(c, &body); err != nil || body.Code == "" {
		badRequest(c, "code is required")
		return
	}
	a.withIdempotency(c, func() (int, any) {
		b, err := a.Store.CreateBin(c.Request.Context(), c.Param("code"), body.Code, body.CapacityKg, body.TemperatureBand, body.Attrs)
		if err != nil {
			return http.StatusInternalServerError, gin.H{"error": err.Error()}
		}
		return http.StatusCreated, b
	})
}

func (a *API) BinStock(c *gin.Context) {
	items, err := a.Store.ListBinStock(c.Request.Context(), c.Param("code"))
	if err != nil {
		storeErr(c, err)
		return
	}
	ok(c, gin.H{"items": items})
}
