package config

import (
	"os"
	"testing"
)

func TestValidateProductionRequiresSecret(t *testing.T) {
	t.Setenv("ENVIRONMENT", "production")
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db")
	t.Setenv("ALLOWED_ORIGINS", "https://app.example.com")
	os.Unsetenv("SERVICE_CLIENT_SECRET")
	_, err := Load()
	if err == nil {
		t.Fatal("expected SERVICE_CLIENT_SECRET required in production")
	}
}

func TestStrictRBAC(t *testing.T) {
	if !(Config{Environment: "production"}).StrictRBAC() {
		t.Fatal("production should enforce strict RBAC")
	}
	if (Config{Environment: "development"}).StrictRBAC() {
		t.Fatal("development should not enforce strict RBAC")
	}
}
