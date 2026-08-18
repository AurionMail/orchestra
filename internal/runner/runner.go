package runner

import (
	"context"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// ServiceConfig defines parameters for executing a child process.
type ServiceConfig struct {
	Name       string            // e.g., "cryptpad", "hydra", "sso"
	Executable string            // Path to executable (e.g., node path or binary)
	Args       []string          // Command line arguments
	WorkDir    string            // Working directory
	Env        map[string]string // Custom environment variables for this service
}

// Manager supervises the extraction of runtimes and execution of child processes.
type Manager struct {
	dataDir    string
	runtimeDir string
	processes  map[string]*exec.Cmd
	mu         sync.Mutex
}

// NewManager creates a supervisor instance and ensures storage paths exist.
func NewManager(dataDir string) (*Manager, error) {
	absDataDir, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, fmt.Errorf("invalid data directory path: %w", err)
	}

	runtimeDir := filepath.Join(absDataDir, "runtime")

	// Ensure runtime directory exists
	if err := os.MkdirAll(runtimeDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create runtime dir: %w", err)
	}

	return &Manager{
		dataDir:    absDataDir,
		runtimeDir: runtimeDir,
		processes:  make(map[string]*exec.Cmd),
	}, nil
}

// ExtractEmbeddedFS extracts packaged runtimes/apps from //go:embed into the runtime dir on disk.
func (m *Manager) ExtractEmbeddedFS(embedFS embed.FS, targetSubDir string) error {
	log.Printf("[runner] Unpacking runtime assets to %s...", m.runtimeDir)

	return fs.WalkDir(embedFS, targetSubDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(targetSubDir, path)
		if err != nil {
			return err
		}

		outPath := filepath.Join(m.runtimeDir, relPath)

		if d.IsDir() {
			return os.MkdirAll(outPath, 0755)
		}

		data, err := embedFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read embedded file %s: %w", path, err)
		}

		// Preserve executable permissions for binary files
		mode := fs.FileMode(0644)
		if d.Type().IsRegular() && (filepath.Ext(path) == "" || filepath.Ext(path) == ".sh") {
			mode = 0755
		}

		if err := os.WriteFile(outPath, data, mode); err != nil {
			return fmt.Errorf("failed to write file %s: %w", outPath, err)
		}

		return nil
	})
}

// GetRuntimePath returns the absolute path inside the runtime directory.
func (m *Manager) GetRuntimePath(subPath string) string {
	return filepath.Join(m.runtimeDir, subPath)
}

// StartService launches a child process, injects environment variables, and streams logs.
func (m *Manager) StartService(ctx context.Context, cfg ServiceConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.processes[cfg.Name]; exists {
		return fmt.Errorf("service %s is already running", cfg.Name)
	}

	cmd := exec.CommandContext(ctx, cfg.Executable, cfg.Args...)
	if cfg.WorkDir != "" {
		cmd.Dir = cfg.WorkDir
	} else {
		cmd.Dir = m.runtimeDir
	}

	// 1. Inherit parent system environment variables
	envMap := make(map[string]string)
	for _, env := range os.Environ() {
		pair := filepath.SplitList(env)
		if len(pair) > 0 {
			// Split KEY=VALUE
			for i := 0; i < len(env); i++ {
				if env[i] == '=' {
					envMap[env[:i]] = env[i+1:]
					break
				}
			}
		}
	}

	// 2. Inject custom service environment variables from central .env
	for k, v := range cfg.Env {
		envMap[k] = v
	}

	// Build final process env slice
	var finalEnv []string
	for k, v := range envMap {
		finalEnv = append(finalEnv, fmt.Sprintf("%s=%s", k, v))
	}
	cmd.Env = finalEnv

	// 3. Pipe stdout/stderr with custom prefixes for centralized logging
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe for %s: %w", cfg.Name, err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create stderr pipe for %s: %w", cfg.Name, err)
	}

	logPrefix := fmt.Sprintf("[%s] ", cfg.Name)
	go streamLogs(stdout, logPrefix)
	go streamLogs(stderr, logPrefix+"(ERR) ")

	// 4. Start the process
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start process %s: %w", cfg.Name, err)
	}

	m.processes[cfg.Name] = cmd
	log.Printf("[runner] Started service %s (PID: %d)", cfg.Name, cmd.Process.Pid)

	// Monitor process termination in background
	go func() {
		err := cmd.Wait()
		m.mu.Lock()
		delete(m.processes, cfg.Name)
		m.mu.Unlock()

		if err != nil {
			log.Printf("[runner] Service %s stopped with error: %v", cfg.Name, err)
		} else {
			log.Printf("[runner] Service %s stopped gracefully", cfg.Name)
		}
	}()

	return nil
}

// StopAll performs a graceful shutdown on all managed child processes.
func (m *Manager) StopAll(timeout time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.processes) == 0 {
		return
	}

	log.Printf("[runner] Shutting down %d child processes...", len(m.processes))
	var wg sync.WaitGroup

	for name, cmd := range m.processes {
		if cmd.Process == nil {
			continue
		}

		wg.Add(1)
		go func(svcName string, process *os.Process) {
			defer wg.Done()

			log.Printf("[runner] Sending SIGTERM to %s (PID %d)...", svcName, process.Pid)
			_ = process.Signal(syscall.SIGTERM)

			// Wait or force kill with SIGKILL if timeout expires
			done := make(chan struct{})
			go func() {
				_, _ = process.Wait()
				close(done)
			}()

			select {
			case <-done:
				log.Printf("[runner] %s terminated gracefully", svcName)
			case <-time.After(timeout):
				log.Printf("[runner] %s timeout reached, sending SIGKILL...", svcName)
				_ = process.Kill()
			}
		}(name, cmd.Process)
	}

	wg.Wait()
	m.processes = make(map[string]*exec.Cmd)
}

// Helper to pipe child logs to standard output
func streamLogs(r io.Reader, prefix string) {
	buf := make([]byte, 1024)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			log.Printf("%s%s", prefix, string(buf[:n]))
		}
		if err != nil {
			break
		}
	}
}
