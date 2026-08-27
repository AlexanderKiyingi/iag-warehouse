package migrate

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"iag-warehouse/backend/migrations"
)

const migrateAdvisoryLockKey1 int32 = 881234502
const migrateAdvisoryLockKey2 int32 = 400500337

const migrationTable = `
CREATE TABLE IF NOT EXISTS warehouse.schema_migrations (
	version TEXT PRIMARY KEY,
	applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
`

func Up(ctx context.Context, pool *pgxpool.Pool) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("migrate begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1, $2)`, migrateAdvisoryLockKey1, migrateAdvisoryLockKey2); err != nil {
		return fmt.Errorf("migrate advisory lock: %w", err)
	}
	if _, err := tx.Exec(ctx, migrationTable); err != nil {
		return fmt.Errorf("migration table: %w", err)
	}

	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	for _, name := range files {
		version := strings.TrimSuffix(name, ".sql")
		var exists bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM warehouse.schema_migrations WHERE version = $1)`,
			version,
		).Scan(&exists); err != nil {
			return fmt.Errorf("check migration %s: %w", version, err)
		}
		if exists {
			continue
		}

		body, err := migrations.FS.ReadFile(name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		if err := execSQL(ctx, tx, string(body)); err != nil {
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO warehouse.schema_migrations (version) VALUES ($1)`,
			version,
		); err != nil {
			return fmt.Errorf("record migration %s: %w", name, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("migrate commit: %w", err)
	}
	committed = true
	return nil
}

// Status reports which embedded migrations have been applied to this database
// and which have not, in filename order.
//
// It exists so a production run can be inspected before it happens: applying
// migrations out of band means somebody is typing a command against a live
// database, and "show me what this would do" is the difference between a
// deployment step and a gamble. It creates nothing and takes no locks — a
// database with no migration table yet simply reports everything as pending.
func Status(ctx context.Context, pool *pgxpool.Pool) (applied, pending []string, err error) {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return nil, nil, fmt.Errorf("read migrations: %w", err)
	}
	var versions []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			versions = append(versions, strings.TrimSuffix(e.Name(), ".sql"))
		}
	}
	sort.Strings(versions)

	done := map[string]bool{}
	rows, err := pool.Query(ctx, `
		SELECT version FROM warehouse.schema_migrations`)
	if err != nil {
		// No migration table yet is a legitimate state — a fresh database has
		// every migration pending — and must not read as a failure.
		if isUndefinedTable(err) {
			return nil, versions, nil
		}
		return nil, nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, nil, err
		}
		done[v] = true
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	for _, v := range versions {
		if done[v] {
			applied = append(applied, v)
		} else {
			pending = append(pending, v)
		}
	}
	return applied, pending, nil
}

func isUndefinedTable(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42P01"
}

func execSQL(ctx context.Context, tx pgx.Tx, sql string) error {
	sql = strings.TrimSpace(strings.ReplaceAll(sql, "\r\n", "\n"))
	if sql == "" {
		return nil
	}
	for _, chunk := range splitStatements(sql) {
		chunk = strings.TrimSpace(chunk)
		if chunk == "" {
			continue
		}
		if _, err := tx.Exec(ctx, chunk); err != nil {
			snippet := chunk
			if len(snippet) > 400 {
				snippet = snippet[:400] + "…"
			}
			return fmt.Errorf("exec migration chunk: %w\n--\n%s", err, snippet)
		}
	}
	return nil
}

// splitStatements splits a migration into chunks on a ";" followed by a blank
// line, but never inside a dollar-quoted block.
//
// The previous strings.Split(sql, ";\n\n") had no notion of quoting, so a
// DO $tag$ ... $tag$ body containing a statement followed by a blank line -
// ordinary formatting inside PL/pgSQL - was cut in half and both halves sent as
// invalid SQL. MES 008_machine_telemetry_policies could never be applied for exactly
// that reason: its "END IF;" is followed by a blank line, and Postgres rejected
// the fragment with "unterminated dollar-quoted string".
func splitStatements(sql string) []string {
	var out []string
	start := 0
	tag := "" // the open dollar-quote tag, empty when outside one
	for i := 0; i < len(sql); i++ {
		if tag != "" {
			if sql[i] == '$' && strings.HasPrefix(sql[i:], tag) {
				i += len(tag) - 1
				tag = ""
			}
			continue
		}
		if sql[i] == '$' {
			if t := dollarTagAt(sql[i:]); t != "" {
				tag = t
				i += len(t) - 1
				continue
			}
		}
		if sql[i] == ';' && strings.HasPrefix(sql[i:], ";\n\n") {
			out = append(out, sql[start:i+1])
			start = i + 1
		}
	}
	return append(out, sql[start:])
}

// dollarTagAt returns the dollar-quote tag opening at s (e.g. "$$" or "$body$"),
// or "" if s does not open one.
func dollarTagAt(s string) string {
	j := 1
	for j < len(s) {
		c := s[j]
		if c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			j++
			continue
		}
		break
	}
	if j < len(s) && s[j] == '$' {
		return s[:j+1]
	}
	return ""
}
