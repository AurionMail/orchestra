package config

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"aurion-orchestrator/internal/assets"
)

func EnsureCryptpadExtracted(destDir string, dataDir string) error {
	targetDataDir := filepath.Join(dataDir, "data")
	if err := os.MkdirAll(targetDataDir, 0755); err != nil {
		return fmt.Errorf("failed to create persistent data dir: %w", err)
	}

	ensureSymlink := func() error {
		linkPath := filepath.Join(destDir, "data")

		if target, err := os.Readlink(linkPath); err == nil && target == targetDataDir {
			return nil
		}
		_ = os.RemoveAll(linkPath)

		// Create the symlink: runtime/apps/cryptpad/data -> storage/cryptpad/data
		if err := os.Symlink(targetDataDir, linkPath); err != nil {
			return fmt.Errorf("failed to create cryptpad data symlink: %w", err)
		}
		return nil
	}

	markerFile := filepath.Join(destDir, "server.js")
	if _, err := os.Stat(markerFile); err == nil {
		return ensureSymlink()
	}

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("failed to create cryptpad dir: %w", err)
	}

	zipData, err := assets.CryptpadZip.ReadFile("assets/apps/cryptpad.zip")
	if err != nil {
		return fmt.Errorf("failed to read embedded cryptpad.zip: %w", err)
	}

	zipReader, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return fmt.Errorf("failed to open cryptpad.zip: %w", err)
	}

	for _, file := range zipReader.File {
		cleanName := filepath.Clean(file.Name)

		parts := strings.Split(cleanName, string(os.PathSeparator))
		if len(parts) > 1 && parts[0] == "cryptpad" {
			cleanName = filepath.Join(parts[1:]...)
		}

		filePath := filepath.Join(destDir, cleanName)
		if !filepath.HasPrefix(filePath, filepath.Clean(destDir)+string(os.PathSeparator)) && filePath != destDir {
			return fmt.Errorf("illegal file path in zip: %s", file.Name)
		}

		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(filePath, 0755); err != nil {
				return err
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
			return err
		}

		dstFile, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, file.Mode())
		if err != nil {
			return err
		}

		srcFile, err := file.Open()
		if err != nil {
			dstFile.Close()
			return err
		}

		_, err = io.Copy(dstFile, srcFile)
		srcFile.Close()
		dstFile.Close()
		if err != nil {
			return err
		}
	}

	if err := ensureSymlink(); err != nil {
		return err
	}

	return nil
}
