package handlers

import (
	"iag-warehouse/backend/internal/auditlog"
	"iag-warehouse/backend/internal/config"
	"iag-warehouse/backend/internal/events"
	"iag-warehouse/backend/internal/store"
)

type API struct {
	Cfg   *config.Config
	Store *store.Store
	Audit *auditlog.Store
	Bus   *events.Bus
}
