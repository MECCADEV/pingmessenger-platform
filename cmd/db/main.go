// Command db applies tracked PostgreSQL migrations and development-only seeds.
// It uses pgx directly; no ORM or database/sql driver is involved.
package main

import (
	"context"
	"crypto/sha256"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

func main() {
	mode := flag.String("mode", "migrate", "migrate or seed")
	migrationsDir := flag.String("migrations-dir", "migrations", "directory containing ordered migration SQL files")
	seedsDir := flag.String("seeds-dir", "seeds", "directory containing ordered seed SQL files")
	wait := flag.Duration("wait", 0, "retry connection/migration errors for this duration")
	flag.Parse()
	_ = godotenv.Load()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		fail("DATABASE_URL is required")
	}
	if *mode == "seed" && os.Getenv("APP_ENV") != "development" && os.Getenv("APP_ENV") != "test" {
		fail("seeds may run only with APP_ENV=development or test")
	}
	if *mode != "migrate" && *mode != "seed" {
		fail("mode must be migrate or seed")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		fail("connect: %v", err)
	}
	defer pool.Close()
	dir, table := *migrationsDir, "schema_migrations"
	if *mode == "seed" {
		dir, table = *seedsDir, "schema_seeds"
	}
	deadline := time.Now().Add(*wait)
	for {
		err = apply(ctx, pool, dir, table)
		if err == nil {
			return
		}
		if *wait == 0 || time.Now().After(deadline) {
			fail("%s: %v", *mode, err)
		}
		time.Sleep(time.Second)
	}
}

func apply(ctx context.Context, pool *pgxpool.Pool, dir, table string) error {
	files, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil {
		return err
	}
	sort.Strings(files)
	if len(files) == 0 {
		return fmt.Errorf("no SQL files found in %s", dir)
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock(hashtext('pingmessenger-db-migrations'))"); err != nil {
		return err
	}
	defer conn.Exec(ctx, "SELECT pg_advisory_unlock(hashtext('pingmessenger-db-migrations'))")
	if _, err := conn.Exec(ctx, fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (filename TEXT PRIMARY KEY, checksum TEXT NOT NULL, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())", pgx.Identifier{table}.Sanitize())); err != nil {
		return err
	}
	for _, file := range files {
		sql, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		name, checksum := filepath.Base(file), fmt.Sprintf("%x", sha256.Sum256(sql))
		var recorded string
		err = conn.QueryRow(ctx, fmt.Sprintf("SELECT checksum FROM %s WHERE filename = $1", pgx.Identifier{table}.Sanitize()), name).Scan(&recorded)
		if err == nil {
			if recorded != checksum {
				return fmt.Errorf("checksum changed for applied file %s", name)
			}
			continue
		}
		if err != pgx.ErrNoRows {
			return err
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, string(sql)); err == nil {
			_, err = tx.Exec(ctx, fmt.Sprintf("INSERT INTO %s (filename, checksum) VALUES ($1, $2)", pgx.Identifier{table}.Sanitize()), name, checksum)
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("%s: %w", name, err)
		}
		if err = tx.Commit(ctx); err != nil {
			return err
		}
		fmt.Printf("applied %s\n", name)
	}
	return nil
}
func fail(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...); os.Exit(1) }
