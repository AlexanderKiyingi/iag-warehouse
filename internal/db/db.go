package db

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	platformdb "github.com/alvor-technologies/iag-platform-go/db"
)

const Schema = "warehouse"

func NewPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	// Sizing comes from the shared platform package: pgx's defaults are an
	// unbounded pool with no warm minimum, and ~23 services share one Postgres
	// instance, so the sum of those defaults is what runs the instance out of
	// connections. BuildPoolConfig is used rather than Connect because this
	// service keeps its own AfterConnect hook below.
	//
	// search_path was already a startup parameter here rather than an
	// AfterConnect SET, which is the pooler-safe form; the shared package sets
	// the same thing, so behaviour is unchanged.
	// Choose the DSN BEFORE building, so an explicit argument is honoured even
	// when DATABASE_URL is unset — which is exactly how the tests and CLI tools
	// call this.
	pcfg := platformdb.ConfigFromEnv(Schema + ", public")
	if strings.TrimSpace(databaseURL) != "" {
		pcfg.URL = databaseURL
	}
	cfg, err := platformdb.BuildPoolConfig(pcfg)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = make(map[string]string)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = Schema + ", public"
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+Schema)
		// IF NOT EXISTS is not concurrency-safe: it checks, then creates, and two
		// connections opening at once against a database that has no schema yet
		// both pass the check and the loser gets a duplicate-key violation on
		// pg_namespace. This runs on every pooled connection, so the pool races
		// itself the first time it warms up — the service then fails to boot with
		// "duplicate key value violates unique constraint pg_namespace_nspname_index",
		// which reads like data corruption and is really just two connections
		// being polite at the same moment.
		//
		// A 23505 here can only mean another connection created the schema first,
		// which is the outcome we wanted. Anything else is a real error.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil
		}
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return pool, nil
}
