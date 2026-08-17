// Command migrate applies the embedded schema migrations out of band.
//
// Production sets AUTO_MIGRATE=false and the config validator refuses to start
// the server with it on, which is correct — a web service racing several
// replicas to migrate on boot is how you get a half-applied schema. But the
// production checklist then says "run migrations in CI/CD" and, until this
// command existed, there was nothing to run: migrate.Up was reachable only from
// the server's boot path. So there was no supported way to migrate production at
// all.
//
//	go run ./cmd/migrate -dry-run    # show what would be applied, change nothing
//	go run ./cmd/migrate             # apply, inside one transaction
//
// DATABASE_URL is read directly rather than through config.Load, because the
// full validator demands a service client secret, a CORS allowlist and an
// audience — none of which a migration run has any business needing, and all of
// which would block it for no reason.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"

	"iag-warehouse/backend/internal/db"
	"iag-warehouse/backend/internal/migrate"
)

func main() {
	dryRun := flag.Bool("dry-run", false, "list pending migrations and exit without applying anything")
	timeout := flag.Duration("timeout", 5*time.Minute, "overall time limit for the run")
	flag.Parse()

	_ = godotenv.Load()

	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	pool, err := db.NewPool(ctx, databaseURL)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer pool.Close()

	applied, pending, err := migrate.Status(ctx, pool)
	if err != nil {
		log.Fatalf("status: %v", err)
	}

	fmt.Printf("%s: %d applied, %d pending\n", redact(databaseURL), len(applied), len(pending))
	for _, v := range pending {
		fmt.Printf("  pending  %s\n", v)
	}
	if len(pending) == 0 {
		fmt.Println("nothing to do")
		return
	}
	if *dryRun {
		fmt.Println("dry run — nothing was applied")
		return
	}

	start := time.Now()
	if err := migrate.Up(ctx, pool); err != nil {
		// Up runs everything in one transaction, so a failure here has rolled the
		// whole batch back and the schema is exactly as it was.
		log.Fatalf("migrate failed (rolled back, schema unchanged): %v", err)
	}

	_, stillPending, err := migrate.Status(ctx, pool)
	if err != nil {
		log.Fatalf("verify: %v", err)
	}
	if len(stillPending) > 0 {
		log.Fatalf("migrate reported success but %d migration(s) are still pending: %v", len(stillPending), stillPending)
	}
	fmt.Printf("applied %d migration(s) in %s\n", len(pending), time.Since(start).Round(time.Millisecond))
}

// redact strips any credentials from the connection string before it is printed,
// since this command's output is the sort of thing that gets pasted into a
// deployment log or a chat window.
func redact(databaseURL string) string {
	at := strings.LastIndex(databaseURL, "@")
	scheme := strings.Index(databaseURL, "://")
	if at < 0 || scheme < 0 || at < scheme {
		return databaseURL
	}
	return databaseURL[:scheme+3] + "***@" + databaseURL[at+1:]
}
