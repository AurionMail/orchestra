package config

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"aurion-orchestrator/internal/assets"
)

func EnsureWebmailExtracted(destDir string) error {
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("failed to create webmail dir: %w", err)
	}

	tarData, err := assets.WebmailTar.ReadFile("assets/apps/webmail.tar.gz")
	if err != nil {
		return fmt.Errorf("failed to read embedded webmail.tar.gz: %w", err)
	}

	gzReader, err := gzip.NewReader(bytes.NewReader(tarData))
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gzReader.Close()

	tarReader := tar.NewReader(gzReader)

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read tar header: %w", err)
		}

		cleanName := filepath.Clean(header.Name)

		parts := strings.Split(cleanName, string(os.PathSeparator))
		if len(parts) > 1 && (parts[0] == "bulwark-standalone") {
			cleanName = filepath.Join(parts[1:]...)
		}
		if cleanName == "" || cleanName == "." {
			continue
		}

		filePath := filepath.Join(destDir, cleanName)
		if !strings.HasPrefix(filePath, filepath.Clean(destDir)+string(os.PathSeparator)) && filePath != destDir {
			return fmt.Errorf("illegal file path in tar: %s", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(filePath, 0755); err != nil {
				return err
			}

		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
				return err
			}
			dstFile, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, header.FileInfo().Mode())
			if err != nil {
				return err
			}
			if _, err := io.Copy(dstFile, tarReader); err != nil {
				dstFile.Close()
				return err
			}
			dstFile.Close()

		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
				return err
			}
			_ = os.Remove(filePath)
			if err := os.Symlink(header.Linkname, filePath); err != nil {
				return fmt.Errorf("failed to create symlink %s -> %s: %w", filePath, header.Linkname, err)
			}
		}
	}

	return nil
}
