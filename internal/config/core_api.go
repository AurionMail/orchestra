package config

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"

	_ "github.com/lib/pq"
)

func EnsureCoreAPIMigrated(dataDir, runtimeDir, dbDSN string) error {
	markerFile := filepath.Join(dataDir, ".core_api_migrated")

	if _, err := os.Stat(markerFile); err == nil {
		return nil
	}

	sqlPath := filepath.Join(runtimeDir, "migrations", "init.sql")
	if _, err := os.Stat(sqlPath); os.IsNotExist(err) {
		log.Printf("[core-api] Warning: migration file %s not found. Skipping migration.", sqlPath)
		return nil
	}

	log.Println("[core-api] First startup detected: Running init.sql database migration...")

	query, err := os.ReadFile(sqlPath)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", sqlPath, err)
	}

	db, err := sql.Open("postgres", dbDSN)
	if err != nil {
		return fmt.Errorf("failed to connect to database for core-api migration: %w", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		return fmt.Errorf("database unreachable for core-api migration: %w", err)
	}

	if _, err := db.Exec(string(query)); err != nil {
		return fmt.Errorf("failed to execute init.sql: %w", err)
	}

	if err := os.WriteFile(markerFile, []byte("migrated"), 0644); err != nil {
		log.Printf("[core-api] Warning: failed to write migration marker file: %v", err)
	}

	log.Println("[core-api] SQL migrations successfully applied.")
	return nil
}
