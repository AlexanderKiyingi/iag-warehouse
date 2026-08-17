package config

import (
	"os"
	"testing"
)

// clearRuntimeEnv removes every variable HardenedRuntime consults, so each case
// starts from a known state rather than inheriting the developer's shell or CI.
func clearRuntimeEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"ENVIRONMENT", "APP_ENV",
		"RAILWAY_ENVIRONMENT", "RAILWAY_PROJECT_ID", "GIN_MODE",
	} {
		t.Setenv(k, "") // registers restore-on-cleanup
		os.Unsetenv(k)
	}
}

// StrictRBAC used to return IsProduction(), which required ENVIRONMENT to be
// set to "production" — a variable the Railway runbooks never told anyone to
// set. Unset, it fell back to "development" and permission checks ran
// fail-OPEN: a token carrying an empty permissions array was granted every
// permission the service defines. These cases pin the replacement behaviour.
func TestHardenedRuntime(t *testing.T) {
	t.Run("deployed on Railway with ENVIRONMENT unset", func(t *testing.T) {
		clearRuntimeEnv(t)
		t.Setenv("RAILWAY_ENVIRONMENT", "production")
		if !(Config{Environment: "development"}).HardenedRuntime() {
			t.Fatal("a hosted instance must harden even when ENVIRONMENT is unset — this is the regression")
		}
	})

	t.Run("deployed via the container image with ENVIRONMENT unset", func(t *testing.T) {
		clearRuntimeEnv(t)
		t.Setenv("GIN_MODE", "release")
		if !(Config{Environment: "development"}).HardenedRuntime() {
			t.Fatal("GIN_MODE=release marks a deployed runtime and must harden")
		}
	})

	t.Run("laptop with nothing set stays lenient", func(t *testing.T) {
		clearRuntimeEnv(t)
		if (Config{Environment: "development"}).HardenedRuntime() {
			t.Fatal("local development should not be forced fail-closed")
		}
	})

	t.Run("explicit dev on hosted infra opts out", func(t *testing.T) {
		clearRuntimeEnv(t)
		t.Setenv("RAILWAY_ENVIRONMENT", "development")
		t.Setenv("ENVIRONMENT", "development")
		if (Config{Environment: "development"}).HardenedRuntime() {
			t.Fatal("a deliberately-declared dev instance should stay lenient")
		}
	})

	t.Run("explicit production hardens", func(t *testing.T) {
		clearRuntimeEnv(t)
		t.Setenv("ENVIRONMENT", "production")
		if !(Config{Environment: "production"}).HardenedRuntime() {
			t.Fatal("production must harden")
		}
	})

	t.Run("production on a hand-built Config hardens", func(t *testing.T) {
		clearRuntimeEnv(t)
		if !(Config{Environment: "production"}).HardenedRuntime() {
			t.Fatal("an explicit production value must harden without depending on process env")
		}
	})

	t.Run("explicit non-dev environment hardens", func(t *testing.T) {
		clearRuntimeEnv(t)
		t.Setenv("ENVIRONMENT", "staging")
		if !(Config{Environment: "staging"}).HardenedRuntime() {
			t.Fatal("staging was configured on purpose and is not dev-like")
		}
	})
}

// StrictRBAC is the caller-facing name; it must track HardenedRuntime exactly.
func TestStrictRBACTracksHardenedRuntime(t *testing.T) {
	clearRuntimeEnv(t)
	t.Setenv("RAILWAY_PROJECT_ID", "proj_123")

	c := Config{Environment: "development"}
	if c.StrictRBAC() != c.HardenedRuntime() {
		t.Fatal("StrictRBAC diverged from HardenedRuntime")
	}
	if !c.StrictRBAC() {
		t.Fatal("a hosted instance must enforce fail-closed RBAC")
	}
}
