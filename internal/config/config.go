package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/alvor-technologies/iag-platform-go/corsenv"
	"github.com/joho/godotenv"
)

type Config struct {
	Environment string
	ServiceName string
	Port        string
	LogLevel    string

	DatabaseURL string
	AutoMigrate bool

	AuthMode            string
	JWTIssuer           string
	JWKSURL             string
	Audience            string
	ServiceClientID     string
	ServiceClientSecret string
	AuthTokenURL        string
	CORSOrigins         []string
	GatewayAPIPrefix    string
	PublicAPIURL        string

	KafkaBrokers           []string
	KafkaClientID          string
	KafkaConsumerGroup     string
	KafkaOperationsTopic   string
	KafkaCommercialTopic   string
	KafkaProductionTopic   string
	KafkaQualityTopic      string
	KafkaSupplyChainTopic  string
	EventBusEnabled        bool
}

func Load() (*Config, error) {
	_ = godotenv.Load()

	env := strings.ToLower(strings.TrimSpace(getenv("ENVIRONMENT", "development")))
	authMode := strings.ToLower(strings.TrimSpace(getenv("AUTH_MODE", "jwt")))
	switch authMode {
	case "jwt":
	default:
		return nil, fmt.Errorf("AUTH_MODE must be jwt (got %q)", authMode)
	}

	c := &Config{
		Environment:           env,
		ServiceName:           getenv("SERVICE_NAME", "warehouse"),
		Port:                  getenv("PORT", "4005"),
		LogLevel:              getenv("LOG_LEVEL", "info"),
		DatabaseURL:           strings.TrimSpace(os.Getenv("DATABASE_URL")),
		AutoMigrate:           getenv("AUTO_MIGRATE", "true") != "false",
		AuthMode:              authMode,
		JWTIssuer:             getenv("JWT_ISSUER", "http://localhost:3001"),
		JWKSURL:               getenv("JWKS_URL", "http://localhost:3001/.well-known/jwks.json"),
		Audience:              getenv("AUDIENCE", "iag.warehouse"),
		ServiceClientID:       getenv("SERVICE_CLIENT_ID", "iag-warehouse"),
		ServiceClientSecret:   os.Getenv("SERVICE_CLIENT_SECRET"),
		CORSOrigins:           splitCSV(corsenv.Allowlist("http://localhost:3000,http://localhost:8080")),
		GatewayAPIPrefix:      getenv("GATEWAY_API_PREFIX", "/api/v1/warehouse"),
		PublicAPIURL:          getenv("PUBLIC_API_URL", "http://localhost:8080"),
		KafkaBrokers:          splitCSV(getenv("KAFKA_BROKERS", "")),
		KafkaClientID:         getenv("KAFKA_CLIENT_ID", "iag-warehouse"),
		KafkaConsumerGroup:    getenv("KAFKA_CONSUMER_GROUP", "iag.warehouse"),
		KafkaOperationsTopic:  getenv("KAFKA_OPERATIONS_TOPIC", "iag.operations"),
		KafkaCommercialTopic:  getenv("KAFKA_COMMERCIAL_TOPIC", "iag.commercial"),
		KafkaProductionTopic:  getenv("KAFKA_PRODUCTION_TOPIC", "iag.production"),
		KafkaQualityTopic:     getenv("KAFKA_QUALITY_TOPIC", "iag.quality"),
		KafkaSupplyChainTopic: getenv("KAFKA_SUPPLY_CHAIN_TOPIC", "iag.supply-chain"),
		EventBusEnabled:       strings.EqualFold(getenv("EVENT_BUS_ENABLED", "true"), "true"),
	}

	if c.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	if c.AuthTokenURL == "" {
		c.AuthTokenURL = strings.TrimRight(c.JWTIssuer, "/") + "/oauth/token"
	}
	return c, c.Validate()
}

func (c Config) Validate() error {
	if c.DatabaseURL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	if c.IsProduction() {
		if c.HasWildcardCORS() {
			return fmt.Errorf("set ALLOWED_ORIGINS in production (not *)")
		}
		if c.ServiceClientSecret == "" {
			return fmt.Errorf("SERVICE_CLIENT_SECRET is required in production")
		}
		if len(c.ServiceClientSecret) < 16 {
			return fmt.Errorf("SERVICE_CLIENT_SECRET must be at least 16 characters in production")
		}
		if c.AutoMigrate {
			return fmt.Errorf("AUTO_MIGRATE must be false in production (run migrations out of band)")
		}
	}
	return nil
}

func (c Config) IsProduction() bool {
	return c.Environment == "production" || c.Environment == "prod"
}

// StrictRBAC denies access when JWT permissions are empty (fail-closed).
func (c Config) StrictRBAC() bool {
	return c.IsProduction()
}

func (c Config) HasWildcardCORS() bool {
	for _, o := range c.CORSOrigins {
		if strings.TrimSpace(o) == "*" {
			return true
		}
	}
	return false
}

func getenv(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
