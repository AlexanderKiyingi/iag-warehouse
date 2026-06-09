package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (a *API) PlatformStatus(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"service":        a.Cfg.ServiceName,
		"audience":       a.Cfg.Audience,
		"port":           a.Cfg.Port,
		"gatewayPrefix":  a.Cfg.GatewayAPIPrefix,
		"publicApiUrl":   a.Cfg.PublicAPIURL,
		"events":         a.Bus != nil && a.Bus.Enabled(),
		"kafka":          len(a.Cfg.KafkaBrokers) > 0,
		"consumerGroup":  a.Cfg.KafkaConsumerGroup,
		"topics": gin.H{
			"operations": a.Cfg.KafkaOperationsTopic,
			"commercial": a.Cfg.KafkaCommercialTopic,
			"production": a.Cfg.KafkaProductionTopic,
			"quality":    a.Cfg.KafkaQualityTopic,
		},
	})
}

func (a *API) Bootstrap(c *gin.Context) {
	facilities, _ := a.Store.ListFacilities(c.Request.Context())
	c.JSON(http.StatusOK, gin.H{
		"service":    a.Cfg.ServiceName,
		"facilities": facilities,
		"gateway":    a.Cfg.GatewayAPIPrefix,
	})
}
