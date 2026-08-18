package config

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
)

func EnsureHydraMigrated(hydraBin string, configPath string, dataDir string) error {
	markerFile := filepath.Join(dataDir, ".hydra_migrated")

	if _, err := os.Stat(markerFile); err == nil {
		return nil
	}

	log.Println("[hydra] First startup detected: Running SQL migrations...")

	// hydra -c /path/to/hydra.yml migrate sql up --yes
	cmd := exec.Command(hydraBin, "-c", configPath, "migrate", "sql", "up", "--yes")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("hydra sql migration failed: %w", err)
	}

	if err := os.WriteFile(markerFile, []byte("migrated"), 0644); err != nil {
		log.Printf("[hydra] Warning: failed to write migration marker file: %v", err)
	}

	log.Println("[hydra] SQL migrations successfully applied.")
	return nil
}
