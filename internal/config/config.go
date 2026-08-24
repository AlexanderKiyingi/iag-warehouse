package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/alvor-technologies/iag-platform-go/corsenv"
	"github.com/alvor-technologies/iag-platform-go/runtimeenv"
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

	KafkaBrokers          []string
	KafkaClientID         string
	KafkaConsumerGroup    string
	KafkaOperationsTopic  string
	KafkaCommercialTopic  string
	KafkaProductionTopic  string
	KafkaQualityTopic     string
	KafkaSupplyChainTopic string
	KafkaDLQTopic         string
	EventBusEnabled       bool
	// RequireDisposalApproval gates asset disposal behind the tiered approval
	// workflow: when true a disposal is created pending_approval and only retires
	// the asset once its amount-band tiers have signed. Default false keeps
	// disposal immediate. Set via WAREHOUSE_REQUIRE_DISPOSAL_APPROVAL.
	RequireDisposalApproval bool
	// InventoryCostingEnabled turns on weighted-average costing: priced receipts
	// recompute item avg_cost and valued movement events (unit/total/avg cost)
	// are emitted for finance to book the perpetual-inventory GL. Default false —
	// movements stay cost-less and finance's inventory consumer no-ops. Set via
	// INVENTORY_COSTING_ENABLED. See docs/PERPETUAL_INVENTORY_EVENTS.md.
	InventoryCostingEnabled bool
	// BaseCurrency stamps the valuation figures on movement events. Default UGX.
	BaseCurrency string

	// CountRequireSeparateApprover enforces segregation of duties on cycle
	// counts: whoever raised, submitted or counted a sheet cannot approve it.
	// Defaults ON — this is the control that makes a count worth anything, and a
	// deployment that wants it off should have to say so. Set via
	// WAREHOUSE_COUNT_REQUIRE_SEPARATE_APPROVER=false (single-operator sites and
	// test environments are the honest reasons to).
	CountRequireSeparateApprover bool
	// SlipRequireSeparateAuthorizer applies the same rule to gate passes and
	// handover slips: the person who wrote out what is leaving cannot also be the
	// one who authorises it. Defaults ON. Set via
	// WAREHOUSE_SLIP_REQUIRE_SEPARATE_AUTHORIZER=false.
	SlipRequireSeparateAuthorizer bool
	// IssueRequireReference enforces the rule that no stock moves without a
	// document behind it: an issue must name a cost centre, a production order
	// or a work order. A department is who took it, not why, and on its own it
	// leaves the movement unattributable — usage variance, shrinkage and work
	// order costing all reduce to guesswork once anonymous issues are in the
	// ledger. Defaults ON. Set via WAREHOUSE_ISSUE_REQUIRE_REFERENCE=false.
	//
	// Turning it off is a deliberate choice to accept unattributable stock
	// movement; the refusal names the three fields, so a caller that hits it is
	// one field away from compliance rather than blocked.
	IssueRequireReference bool
	// ItemLifecycleEnforced makes wh_items.status mean something: a draft item
	// cannot be transacted at all, a restricted one cannot be received or
	// issued without the override permission, and an obsolete or blocked one is
	// refused outright. Defaults ON once the column exists — an install that has
	// not curated its item statuses can set
	// WAREHOUSE_ITEM_LIFECYCLE_ENFORCED=false, but every item defaults to
	// 'active' on migration, so the honest default is on.
	ItemLifecycleEnforced bool
	// OrgName is printed at the head of gate passes and handover slips.
	OrgName string
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
		Environment:             env,
		ServiceName:             getenv("SERVICE_NAME", "warehouse"),
		Port:                    getenv("PORT", "4005"),
		LogLevel:                getenv("LOG_LEVEL", "info"),
		DatabaseURL:             strings.TrimSpace(os.Getenv("DATABASE_URL")),
		AutoMigrate:             getenv("AUTO_MIGRATE", "true") != "false",
		AuthMode:                authMode,
		JWTIssuer:               getenv("JWT_ISSUER", "http://localhost:3001"),
		JWKSURL:                 getenv("JWKS_URL", "http://localhost:3001/.well-known/jwks.json"),
		Audience:                getenv("AUDIENCE", "iag.warehouse"),
		RequireDisposalApproval: strings.EqualFold(os.Getenv("WAREHOUSE_REQUIRE_DISPOSAL_APPROVAL"), "true"),
		ServiceClientID:         getenv("SERVICE_CLIENT_ID", "iag-warehouse"),
		ServiceClientSecret:     os.Getenv("SERVICE_CLIENT_SECRET"),
		CORSOrigins:             splitCSV(corsenv.Allowlist("http://localhost:3000,http://localhost:8080")),
		GatewayAPIPrefix:        getenv("GATEWAY_API_PREFIX", "/api/v1/warehouse"),
		PublicAPIURL:            getenv("PUBLIC_API_URL", "http://localhost:8080"),
		KafkaBrokers:            splitCSV(getenv("KAFKA_BROKERS", "")),
		KafkaClientID:           getenv("KAFKA_CLIENT_ID", "iag-warehouse"),
		KafkaConsumerGroup:      getenv("KAFKA_CONSUMER_GROUP", "iag.warehouse"),
		KafkaOperationsTopic:    getenv("KAFKA_OPERATIONS_TOPIC", "iag.operations"),
		KafkaCommercialTopic:    getenv("KAFKA_COMMERCIAL_TOPIC", "iag.commercial"),
		KafkaProductionTopic:    getenv("KAFKA_PRODUCTION_TOPIC", "iag.production"),
		KafkaQualityTopic:       getenv("KAFKA_QUALITY_TOPIC", "iag.quality"),
		KafkaSupplyChainTopic:   getenv("KAFKA_SUPPLY_CHAIN_TOPIC", "iag.supply-chain"),
		KafkaDLQTopic:           getenv("KAFKA_DLQ_TOPIC", "iag.warehouse.dlq"),
		EventBusEnabled:         strings.EqualFold(getenv("EVENT_BUS_ENABLED", "true"), "true"),
		InventoryCostingEnabled: strings.EqualFold(os.Getenv("INVENTORY_COSTING_ENABLED"), "true"),
		BaseCurrency:            getenv("BASE_CURRENCY", "UGX"),
		// Both segregation-of-duties gates default ON: they are only disabled by
		// an explicit "false", never by an unset variable.
		CountRequireSeparateApprover:  !strings.EqualFold(os.Getenv("WAREHOUSE_COUNT_REQUIRE_SEPARATE_APPROVER"), "false"),
		SlipRequireSeparateAuthorizer: !strings.EqualFold(os.Getenv("WAREHOUSE_SLIP_REQUIRE_SEPARATE_AUTHORIZER"), "false"),
		IssueRequireReference:         !strings.EqualFold(os.Getenv("WAREHOUSE_ISSUE_REQUIRE_REFERENCE"), "false"),
		ItemLifecycleEnforced:         !strings.EqualFold(os.Getenv("WAREHOUSE_ITEM_LIFECYCLE_ENFORCED"), "false"),
		OrgName:                       getenv("WAREHOUSE_ORG_NAME", "Inspire Africa Group"),
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

// StrictRBAC denies access when a verified token carries no permissions
// (fail-closed).
func (c Config) StrictRBAC() bool { return c.HardenedRuntime() }

// HardenedRuntime reports whether production safeguards apply.
//
// The decision lives in shared/platform-go/runtimeenv so there is one copy of
// it. It used to be inlined here, and in twelve other services, which is how
// two of them were missed when the fail-open bug was fixed.
func (c Config) HardenedRuntime() bool {
	return runtimeenv.Hardened(c.Environment)
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
