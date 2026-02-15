// Package main implements a Go-based service supervisor similar to s6-overlay
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"
)

var (
	debugMode bool
	version   = "v0.1.3"
)

// Socket path for inter-process communication
const socketPath = "/tmp/go-overlay.sock"

// ANSI color codes
const (
	ColorReset   = "\033[0m"
	ColorRed     = "\033[31m"
	ColorGreen   = "\033[32m"
	ColorYellow  = "\033[33m"
	ColorBlue    = "\033[34m"
	ColorMagenta = "\033[35m"
	ColorCyan    = "\033[36m"
	ColorWhite   = "\033[37m"
	ColorGray    = "\033[90m"

	// Bold colors
	ColorBoldRed     = "\033[1;31m"
	ColorBoldGreen   = "\033[1;32m"
	ColorBoldYellow  = "\033[1;33m"
	ColorBoldBlue    = "\033[1;34m"
	ColorBoldMagenta = "\033[1;35m"
	ColorBoldCyan    = "\033[1;36m"
	ColorBoldWhite   = "\033[1;37m"
)

// ServiceState represents the current state of a service
type ServiceState int

// Service state constants
const (
	ServiceStatePending ServiceState = iota
	ServiceStateStarting
	ServiceStateRunning
	ServiceStateStopping
	ServiceStateStopped
	ServiceStateFailed
)

func (s ServiceState) String() string {
	switch s {
	case ServiceStatePending:
		return "PENDING"
	case ServiceStateStarting:
		return "STARTING"
	case ServiceStateRunning:
		return "RUNNING"
	case ServiceStateStopping:
		return "STOPPING"
	case ServiceStateStopped:
		return "STOPPED"
	case ServiceStateFailed:
		return "FAILED"
	default:
		return "UNKNOWN"
	}
}

// CommandType represents the type of IPC command
type CommandType string

// IPC command type constants
const (
	CmdListServices   CommandType = "list_services"
	CmdRestartService CommandType = "restart_service"
	CmdGetStatus      CommandType = "get_status"
)

// IPCCommand represents a command sent via IPC
type IPCCommand struct {
	Type        CommandType `json:"type"`
	ServiceName string      `json:"service_name,omitempty"`
}

// ServiceInfo contains information about a service
type ServiceInfo struct {
	Name      string        `json:"name"`
	LastError string        `json:"last_error,omitempty"`
	Uptime    time.Duration `json:"uptime"`
	State     ServiceState  `json:"state"`
	PID       int           `json:"pid"`
	Required  bool          `json:"required"`
}

// IPCResponse represents a response to an IPC command
type IPCResponse struct {
	Message  string        `json:"message,omitempty"`
	Services []ServiceInfo `json:"services,omitempty"`
	Success  bool          `json:"success"`
}

// Global variables for graceful shutdown
var (
	shutdownCtx    context.Context
	shutdownCancel context.CancelFunc
	activeServices = make(map[string]*ServiceProcess)
	servicesMutex  sync.RWMutex
	shutdownWg     sync.WaitGroup

	// IPC server
	ipcServer    net.Listener
	globalConfig *Config
)

// Timeouts contains configuration for various timeout values
type Timeouts struct {
	PostScript      int `toml:"post_script_timeout,omitempty"`
	ServiceShutdown int `toml:"service_shutdown_timeout,omitempty"`
	GlobalShutdown  int `toml:"global_shutdown_timeout,omitempty"`
	DependencyWait  int `toml:"dependency_wait_timeout,omitempty"`
}

// DependsOnField supports both single string and array of strings
type DependsOnField []string

func (d *DependsOnField) UnmarshalTOML(data interface{}) error {
	switch v := data.(type) {
	case string:
		*d = []string{v}
	case []interface{}:
		deps := make([]string, len(v))
		for i, item := range v {
			str, ok := item.(string)
			if !ok {
				return fmt.Errorf("depends_on array must contain only strings")
			}
			deps[i] = str
		}
		*d = deps
	default:
		return fmt.Errorf("depends_on must be a string or array of strings")
	}
	return nil
}

// Support decoding a single string via encoding.TextUnmarshaler (used by toml v2 for strings)
// Note: We avoid implementing encoding.TextUnmarshaler on DependsOnField
// to prevent go-toml/v2 from decoding arrays element-by-element and
// overwriting the field. UnmarshalTOML above handles both string and array.

// WaitAfterField supports both int (global wait) and map (per-dependency wait)
type WaitAfterField struct {
	PerDep   map[string]int // Per-dependency wait times
	Global   int            // Global wait time for all dependencies
	IsPerDep bool           // Flag to indicate which mode is used
}

// UnmarshalTOML decodes both integer and map forms into WaitAfterField.
func (w *WaitAfterField) UnmarshalTOML(data interface{}) error {
	switch v := data.(type) {
	case int64:
		w.Global = int(v)
		w.IsPerDep = false
	case map[string]interface{}:
		w.PerDep = make(map[string]int)
		for key, val := range v {
			intVal, ok := val.(int64)
			if !ok {
				return fmt.Errorf("wait_after map values must be integers")
			}
			w.PerDep[key] = int(intVal)
		}
		w.IsPerDep = true
	default:
		return fmt.Errorf("wait_after must be an integer or a map of dependency names to wait times")
	}
	return nil
}

// Note: We intentionally only keep the value-receiver implementation above,
// since pointer receivers cannot be duplicated with the same method name.

// GetWaitTime returns the wait time for a specific dependency
func (w *WaitAfterField) GetWaitTime(depName string) int {
	if w.IsPerDep {
		if waitTime, exists := w.PerDep[depName]; exists {
			return waitTime
		}
		return 0 // No wait if not specified in map
	}
	return w.Global
}

// HealthCheckConfig defines health check settings for a service
type HealthCheckConfig struct {
	// HTTP endpoint to check (e.g., "http://localhost:8080/health")
	Endpoint string `toml:"endpoint,omitempty"`
	// Command to run for health check (exit 0 = healthy)
	Command string `toml:"command,omitempty"`
	// Interval between health checks in seconds (default: 30)
	Interval int `toml:"interval,omitempty"`
	// Number of consecutive failures before marking unhealthy (default: 3)
	Retries int `toml:"retries,omitempty"`
	// Timeout for each health check in seconds (default: 5)
	Timeout int `toml:"timeout,omitempty"`
	// Initial delay before first health check in seconds (default: 10)
	StartDelay int `toml:"start_delay,omitempty"`
}

// RestartPolicy defines restart behavior for services
type RestartPolicy string

// Restart policy constants
const (
	RestartNever     RestartPolicy = "never"      // Never restart (default)
	RestartOnFailure RestartPolicy = "on-failure" // Restart only on non-zero exit
	RestartAlways    RestartPolicy = "always"     // Always restart when stopped
)

type Service struct {
	Name      string          `toml:"name"`
	Command   string          `toml:"command"`
	LogFile   string          `toml:"log_file,omitempty"`
	PreScript string          `toml:"pre_script,omitempty"`
	PosScript string          `toml:"pos_script,omitempty"`
	User      string          `toml:"user,omitempty"`
	Args      []string        `toml:"args"`
	DependsOn DependsOnField  `toml:"depends_on,omitempty"`
	WaitAfter *WaitAfterField `toml:"wait_after,omitempty"`
	Enabled   *bool           `toml:"enabled,omitempty"`  // Changed to pointer to detect if set
	Required  bool            `toml:"required,omitempty"` // If true, failure stops whole system
	Oneshot   bool            `toml:"oneshot,omitempty"`  // If true, dependent services wait for successful completion

	// Health Check Configuration
	HealthCheck *HealthCheckConfig `toml:"health_check,omitempty"`

	// Restart Policy Configuration
	Restart      RestartPolicy `toml:"restart,omitempty"`       // "never", "on-failure", "always"
	RestartDelay int           `toml:"restart_delay,omitempty"` // Delay between restarts in seconds (default: 1)
	MaxRestarts  int           `toml:"max_restarts,omitempty"`  // Max restart attempts (0 = unlimited)

	// Environment Variables
	Env     map[string]string `toml:"env,omitempty"`      // Inline environment variables
	EnvFile string            `toml:"env_file,omitempty"` // Path to .env file
}

type Config struct {
	Services []Service `toml:"services"`
	Timeouts Timeouts  `toml:"timeouts,omitempty"`
}

// Internal raw representations to support flexible TOML decoding (go-toml/v2)
type serviceRaw struct {
	Name         string             `toml:"name"`
	Command      string             `toml:"command"`
	LogFile      string             `toml:"log_file,omitempty"`
	PreScript    string             `toml:"pre_script,omitempty"`
	PosScript    string             `toml:"pos_script,omitempty"`
	User         string             `toml:"user,omitempty"`
	Args         []string           `toml:"args"`
	DependsOn    interface{}        `toml:"depends_on,omitempty"`
	WaitAfter    interface{}        `toml:"wait_after,omitempty"`
	Enabled      *bool              `toml:"enabled,omitempty"`
	Required     bool               `toml:"required,omitempty"`
	Oneshot      bool               `toml:"oneshot,omitempty"`
	HealthCheck  *HealthCheckConfig `toml:"health_check,omitempty"`
	Restart      string             `toml:"restart,omitempty"`
	RestartDelay int                `toml:"restart_delay,omitempty"`
	MaxRestarts  int                `toml:"max_restarts,omitempty"`
	Env          map[string]string  `toml:"env,omitempty"`
	EnvFile      string             `toml:"env_file,omitempty"`
}

type configRaw struct {
	Services []serviceRaw `toml:"services"`
	Timeouts Timeouts     `toml:"timeouts,omitempty"`
}

func parseConfig(r io.Reader) (Config, error) {
	var raw configRaw
	if err := toml.NewDecoder(r).Decode(&raw); err != nil {
		return Config{}, err
	}

	cfg := Config{Timeouts: raw.Timeouts}
	for i := range raw.Services {
		sr := &raw.Services[i]
		if sr.Name == "" {
			// Skip stray entries (e.g., partial tables without a name)
			continue
		}
		var wa *WaitAfterField
		switch v := sr.WaitAfter.(type) {
		case nil:
			// leave nil
		case int64:
			wa = &WaitAfterField{Global: int(v), IsPerDep: false}
		case map[string]interface{}:
			mp := make(map[string]int)
			for k, anyVal := range v {
				iv, ok := anyVal.(int64)
				if !ok {
					return Config{}, fmt.Errorf("wait_after map values must be integers")
				}
				mp[k] = int(iv)
			}
			wa = &WaitAfterField{PerDep: mp, IsPerDep: true}
		default:
			return Config{}, fmt.Errorf("wait_after must be an integer or a map of dependency names to wait times")
		}

		// convert depends_on
		var deps DependsOnField
		switch dv := sr.DependsOn.(type) {
		case nil:
		case string:
			deps = []string{dv}
		case []interface{}:
			out := make([]string, len(dv))
			for i, item := range dv {
				s, ok := item.(string)
				if !ok {
					return Config{}, fmt.Errorf("depends_on array must contain only strings")
				}
				out[i] = s
			}
			deps = out
		default:
			return Config{}, fmt.Errorf("depends_on must be a string or array of strings")
		}

		svc := Service{
			Name:         sr.Name,
			Command:      sr.Command,
			Args:         sr.Args,
			LogFile:      sr.LogFile,
			PreScript:    sr.PreScript,
			PosScript:    sr.PosScript,
			DependsOn:    deps,
			WaitAfter:    wa,
			Enabled:      sr.Enabled,
			User:         sr.User,
			Required:     sr.Required,
			Oneshot:      sr.Oneshot,
			HealthCheck:  sr.HealthCheck,
			Restart:      RestartPolicy(sr.Restart),
			RestartDelay: sr.RestartDelay,
			MaxRestarts:  sr.MaxRestarts,
			Env:          sr.Env,
			EnvFile:      sr.EnvFile,
		}
		cfg.Services = append(cfg.Services, svc)
	}
	return cfg, nil
}

type ServiceProcess struct {
	Name      string
	LastError error
	StartTime time.Time
	Config    Service // Store original config for restart
	Process   *exec.Cmd
	PTY       *os.File
	Cancel    context.CancelFunc
	StateMu   sync.RWMutex
	State     ServiceState

	// Health tracking
	HealthyAt    time.Time
	FailureCount int
	HealthCancel context.CancelFunc // Cancel function for health monitor goroutine

	// Restart tracking
	RestartCount int
	LastRestart  time.Time
}

// SetState updates the service state with logging
func (sp *ServiceProcess) SetState(state ServiceState) {
	sp.StateMu.Lock()
	defer sp.StateMu.Unlock()
	oldState := sp.State
	sp.State = state

	// Color-coded state transition message
	oldStateStr := colorize(getStateColor(oldState), oldState.String())
	newStateStr := colorize(getStateColor(state), state.String())
	_info(fmt.Sprintf("Service '%s' state changed from %s to %s",
		colorize(ColorCyan, sp.Name), oldStateStr, newStateStr))
}

func (sp *ServiceProcess) GetState() ServiceState {
	sp.StateMu.RLock()
	defer sp.StateMu.RUnlock()
	return sp.State
}

func (sp *ServiceProcess) SetError(err error) {
	sp.StateMu.Lock()
	defer sp.StateMu.Unlock()
	sp.LastError = err
	if err != nil {
		sp.State = ServiceStateFailed
		// Only log error if not in test mode (when debugMode is explicitly set)
		// In tests, this message is expected but can be noisy
		_error(fmt.Sprintf("Service '%s' failed with error: %v",
			colorize(ColorCyan, sp.Name), err))
	}
}

func (sp *ServiceProcess) GetPID() int {
	if sp.Process != nil && sp.Process.Process != nil {
		return sp.Process.Process.Pid
	}
	return 0
}

// ValidationError represents a configuration validation error
type ValidationError struct {
	Field   string
	Service string
	Message string
}

func (e ValidationError) Error() string {
	if e.Service != "" {
		return fmt.Sprintf("validation error in service '%s', field '%s': %s", e.Service, e.Field, e.Message)
	}
	return fmt.Sprintf("validation error in field '%s': %s", e.Field, e.Message)
}

type ValidationErrors []ValidationError

func (e ValidationErrors) Error() string {
	if len(e) == 0 {
		return "no validation errors"
	}
	msgs := make([]string, 0, len(e))
	for _, err := range e {
		msgs = append(msgs, err.Error())
	}
	return strings.Join(msgs, "; ")
}

// Auto-install function to create symlink in PATH
func autoInstallInPath() {
	// Get the current executable path
	execPath, err := os.Executable()
	if err != nil {
		_info("Warning: Could not determine executable path:", err)
		return
	}

	// Check if we're already in a standard PATH location
	pathDirs := []string{"/usr/local/bin", "/usr/bin", "/bin"}
	execDir := filepath.Dir(execPath)

	for _, pathDir := range pathDirs {
		if execDir == pathDir {
			_info("Already installed in PATH:", execDir)
			return
		}
	}

	// Target installation path
	targetPath := "/usr/local/bin/go-overlay"

	// Check if symlink already exists and points to our executable
	if linkTarget, err := os.Readlink(targetPath); err == nil {
		if linkTarget == execPath {
			return // Already correctly installed
		}
		// Remove existing symlink if it points somewhere else
		_ = os.Remove(targetPath)
	}

	// Create symlink
	if err := os.Symlink(execPath, targetPath); err != nil {
		_warn(fmt.Sprintf("Could not create symlink in PATH: %v", err))
		_warn(fmt.Sprintf("You can manually run: sudo ln -sf %s %s", execPath, targetPath))
		return
	}

	_success("Auto-installed in PATH as 'go-overlay'")
	_info("You can now use: go-overlay list, go-overlay restart <service>, etc.")
}

func main() {
	fmt.Printf("Go Overlay - Version: %s\n", version)

	rootCmd := &cobra.Command{
		Use:   "go-overlay",
		Short: "Go-based service supervisor like s6-overlay",
		RunE: func(_ *cobra.Command, _ []string) error {
			if debugMode {
				_printEnvVariables()
			}

			// Auto-install in PATH for easier CLI usage
			autoInstallInPath()

			// Initialize shutdown context
			shutdownCtx, shutdownCancel = context.WithCancel(context.Background())

			// Setup signal handler
			setupSignalHandler()

			// Start IPC server
			if err := startIPCServer(); err != nil {
				_info("Warning: Could not start IPC server:", err)
			}

			return loadServices("/services.toml")
		},
	}

	// List services command
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List all services and their status",
		RunE: func(_ *cobra.Command, _ []string) error {
			return listServices()
		},
	}

	// Restart service command
	restartCmd := &cobra.Command{
		Use:   "restart [service-name]",
		Short: "Restart a specific service",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return restartService(args[0])
		},
	}

	// Status command
	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show overall system status",
		RunE: func(_ *cobra.Command, _ []string) error {
			return showStatus()
		},
	}

	// Install command - manual installation
	installCmd := &cobra.Command{
		Use:   "install",
		Short: "Install go-overlay in system PATH",
		RunE: func(_ *cobra.Command, _ []string) error {
			autoInstallInPath()
			return nil
		},
	}

	// Add flags
	rootCmd.Flags().BoolVar(&debugMode, "debug", false, "Enable debug mode")

	// Add subcommands
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(restartCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(installCmd)

	if err := rootCmd.Execute(); err != nil {
		_info("Error:", err)
		os.Exit(1)
	}
}

func setupSignalHandler() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	go func() {
		sig := <-sigChan
		_info("Received signal:", sig)
		_info("Initiating graceful shutdown...")
		gracefulShutdown()
		os.Exit(0)
	}()
}

func gracefulShutdown() {
	_info("Starting graceful shutdown process...")

	// Print current service statuses only if we have active services
	if len(activeServices) > 0 {
		printServiceStatuses()
	}

	// Cancel the shutdown context to signal all services to stop
	// Only if it was initialized (daemon mode)
	if shutdownCancel != nil {
		shutdownCancel()
	}

	// Close IPC server
	if ipcServer != nil {
		_ = ipcServer.Close()
	}

	// Remove socket file
	_ = os.Remove(socketPath)

	// If no active services, we can exit early
	if len(activeServices) == 0 {
		_info("No active services to shutdown")
		return
	}

	// Get global shutdown timeout (default 30s if not configured)
	globalTimeout := 30 * time.Second
	servicesMutex.RLock()
	if len(activeServices) > 0 {
		// Use default timeout since we can't easily access config here
		globalTimeout = 30 * time.Second
	}
	servicesMutex.RUnlock()

	shutdownTimer := time.NewTimer(globalTimeout)
	defer shutdownTimer.Stop()

	// Channel to signal when all services have stopped
	done := make(chan struct{})

	go func() {
		shutdownWg.Wait()
		close(done)
	}()

	select {
	case <-done:
		_info("All services stopped gracefully")
	case <-shutdownTimer.C:
		_info("Shutdown timeout reached after", globalTimeout, ", forcing termination...")
		forceKillAllServices()
		// Give a bit more time for force kill to complete
		select {
		case <-done:
			_info("All services stopped after force kill")
		case <-time.After(5 * time.Second):
			_info("Some services may still be running after force kill timeout")
		}
	}

	_info("Graceful shutdown completed")
}

func forceKillAllServices() {
	servicesMutex.RLock()
	defer servicesMutex.RUnlock()

	for name, serviceProc := range activeServices {
		if serviceProc.Process != nil && serviceProc.Process.Process != nil {
			_info("Force killing service:", name)
			if err := serviceProc.Process.Process.Kill(); err != nil {
				_info("Error force killing service", name, ":", err)
			}
		}
	}
}

func addActiveService(name string, serviceProc *ServiceProcess) {
	servicesMutex.Lock()
	defer servicesMutex.Unlock()
	serviceProc.SetState(ServiceStateStarting)
	serviceProc.StartTime = time.Now()
	activeServices[name] = serviceProc
	shutdownWg.Add(1)
}

func removeActiveService(name string) {
	servicesMutex.Lock()
	defer servicesMutex.Unlock()

	if serviceProc, exists := activeServices[name]; exists {
		serviceProc.SetState(ServiceStateStopped)
		if serviceProc.PTY != nil {
			_ = serviceProc.PTY.Close()
		}
		delete(activeServices, name)
		shutdownWg.Done()
	}
}

func loadServices(configFile string) error {
	config, err := loadAndValidateConfig(configFile)
	if err != nil {
		return err
	}

	globalConfig = &config
	return startAllServices(config)
}

func loadAndValidateConfig(configFile string) (Config, error) {
	_info(fmt.Sprintf("Loading services from %s", colorize(ColorCyan, configFile)))

	file, err := os.Open(configFile)
	if err != nil {
		return Config{}, fmt.Errorf("error opening config file %s: %w", configFile, err)
	}
	defer file.Close()

	config, err := parseConfig(file)
	if err != nil {
		return Config{}, fmt.Errorf("error parsing config file %s: %w", configFile, err)
	}

	if err := validateConfig(&config); err != nil {
		return Config{}, fmt.Errorf("configuration validation failed: %w", err)
	}

	_success("Configuration validated successfully")
	_info(fmt.Sprintf("Timeouts configured: PostScript=%ds, ServiceShutdown=%ds, GlobalShutdown=%ds",
		config.Timeouts.PostScript,
		config.Timeouts.ServiceShutdown,
		config.Timeouts.GlobalShutdown))

	return config, nil
}

func startAllServices(config Config) error {
	startedServices := make(map[string]bool)
	failedServices := make(map[string]bool)
	var mu sync.Mutex
	maxLength := getLongestServiceNameLength(config.Services)

	var wg sync.WaitGroup
	for i := range config.Services {
		service := &config.Services[i]
		if service.Enabled != nil && !*service.Enabled {
			_info("Service ", service.Name, " is disabled, skipping")
			continue
		}

		wg.Add(1)
		go func(s *Service, timeouts Timeouts) {
			defer wg.Done()
			processService(s, &mu, startedServices, failedServices, maxLength, timeouts)
		}(service, config.Timeouts)
	}

	wg.Wait()
	printServiceStatuses()

	<-shutdownCtx.Done()
	_info("Shutdown signal received, stopping all services...")
	return nil
}

func processService(s *Service, mu *sync.Mutex, startedServices, failedServices map[string]bool, maxLength int, timeouts Timeouts) {
	if shutdownCtx.Err() != nil {
		_warn(fmt.Sprintf("Shutdown signal received, skipping service: %s", colorize(ColorCyan, s.Name)))
		return
	}

	if !runPreScript(s) {
		if s.Oneshot {
			mu.Lock()
			failedServices[s.Name] = true
			mu.Unlock()
		}
		return
	}

	if !waitForServiceDependencies(s, mu, startedServices, failedServices, timeouts) {
		return
	}

	serviceDone := make(chan error, 1)
	go func() {
		err := startServiceWithPTY(*s, maxLength, timeouts)
		serviceDone <- err
	}()

	if !s.Oneshot {
		mu.Lock()
		startedServices[s.Name] = true
		mu.Unlock()
	}

	postScriptDone := make(chan struct{})
	go runPostScript(s, timeouts.PostScript, postScriptDone)

	if err := <-serviceDone; err != nil {
		if s.Oneshot {
			mu.Lock()
			failedServices[s.Name] = true
			mu.Unlock()
		}
		handleServiceError(s, err)
	} else if s.Oneshot {
		mu.Lock()
		failedServices[s.Name] = false
		startedServices[s.Name] = true
		mu.Unlock()
	}

	<-postScriptDone
}

func runPreScript(s *Service) bool {
	if s.PreScript == "" {
		return true
	}

	_info("| === PRE-SCRIPT START --- [SERVICE: ", s.Name, "] === |")

	if err := os.Chmod(s.PreScript, 0o700); err != nil { // #nosec G302 - execution permission required
		_info("[PRE-SCRIPT ERROR] Error setting execute permission for script ", s.PreScript, ": ", err)
		return false
	}

	if err := runScript(s.PreScript); err != nil {
		_info("[PRE-SCRIPT ERROR] Error executing pre-script for service ", s.Name, ": ", err)
		if s.Required {
			_info("[CRITICAL] Required service ", s.Name, " pre-script failed, initiating shutdown")
			gracefulShutdown()
		}
		return false
	}

	_info("| === PRE-SCRIPT END --- [SERVICE: ", s.Name, "] === |")
	return true
}

func waitForServiceDependencies(s *Service, mu *sync.Mutex, startedServices, failedServices map[string]bool, timeouts Timeouts) bool {
	if len(s.DependsOn) == 0 {
		return true
	}

	_info(fmt.Sprintf("Service '%s' waiting for dependencies: %s",
		colorize(ColorCyan, s.Name),
		colorize(ColorYellow, strings.Join(s.DependsOn, ", "))))

	for _, dep := range s.DependsOn {
		waitTime := 0
		if s.WaitAfter != nil {
			waitTime = s.WaitAfter.GetWaitTime(dep)
		}
		if !waitForDependency(dep, waitTime, mu, startedServices, failedServices, timeouts.DependencyWait) {
			_warn(fmt.Sprintf("Dependency wait canceled for service: %s", colorize(ColorCyan, s.Name)))
			return false
		}
	}
	return true
}

func runPostScript(s *Service, postScriptTimeout int, done chan<- struct{}) {
	defer close(done)

	timeout := time.Duration(postScriptTimeout) * time.Second
	select {
	case <-time.After(timeout):
	case <-shutdownCtx.Done():
		return
	}

	if s.PosScript == "" {
		return
	}

	_info("| === POST-SCRIPT START --- [SERVICE: ", s.Name, "] === |")

	if err := os.Chmod(s.PosScript, 0o700); err != nil { // #nosec G302 - execution permission required
		_info("[POST-SCRIPT ERROR] Error setting execute permission for script ", s.PosScript, ": ", err)
		return
	}

	if err := runScript(s.PosScript); err != nil {
		_info("[POST-SCRIPT ERROR] Error executing post-script for service ", s.Name, ": ", err)
		return
	}

	_info("| === POST-SCRIPT END --- [SERVICE: ", s.Name, "] === |")
}

func handleServiceError(s *Service, err error) {
	_error(fmt.Sprintf("Error starting service '%s': %v", colorize(ColorCyan, s.Name), err))
	if s.Required {
		_error(fmt.Sprintf("[CRITICAL] Required service '%s' failed, initiating shutdown",
			colorize(ColorCyan, s.Name)))
		gracefulShutdown()
	}
}

func isBashAvailable() bool {
	_, err := exec.LookPath("bash")
	return err == nil
}

func runScript(scriptPath string) error {
	shell := "sh"
	if isBashAvailable() {
		shell = "bash"
	}

	cmd := exec.Command(shell, "-c", scriptPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()

	return cmd.Run()
}

func waitForDependency(depName string, waitAfter int, mu *sync.Mutex, startedServices, failedServices map[string]bool, dependencyWait int) bool {
	maxWait := time.Duration(dependencyWait) * time.Second
	start := time.Now()

	for {
		// Check for shutdown signal
		select {
		case <-shutdownCtx.Done():
			return false
		default:
		}

		// Check for timeout
		if time.Since(start) > maxWait {
			_error(fmt.Sprintf("Dependency wait timeout exceeded for '%s'",
				colorize(ColorYellow, depName)))
			return false
		}

		mu.Lock()
		depStarted := startedServices[depName]
		depFailed := failedServices[depName]
		mu.Unlock()

		if depFailed {
			_error(fmt.Sprintf("Dependency '%s' failed before becoming ready",
				colorize(ColorRed, depName)))
			return false
		}

		if depStarted {
			if waitAfter > 0 {
				_info(fmt.Sprintf("Dependency '%s' is up. Waiting %ds before starting dependent service",
					colorize(ColorGreen, depName), waitAfter))
			} else {
				_success(fmt.Sprintf("Dependency '%s' is ready", colorize(ColorGreen, depName)))
			}

			// Wait with cancellation support
			select {
			case <-time.After(time.Duration(waitAfter) * time.Second):
				return true
			case <-shutdownCtx.Done():
				return false
			}
		}

		_info(fmt.Sprintf("Waiting for dependency: %s", colorize(ColorYellow, depName)))

		// Sleep with cancellation support
		select {
		case <-time.After(2 * time.Second):
			continue
		case <-shutdownCtx.Done():
			return false
		}
	}
}

func joinArgs(args []string) string {
	return strings.Join(args, " ")
}

func startServiceWithPTY(service Service, maxLength int, timeouts Timeouts) error {
	if service.LogFile != "" {
		_info(fmt.Sprintf("Service '%s' is configured to use log file: %s",
			colorize(ColorCyan, service.Name),
			colorize(ColorYellow, service.LogFile)))
		go tailLogFile(service.LogFile, service.Name)
		return nil
	}

	_info(fmt.Sprintf("Starting service: %s", colorize(ColorCyan, service.Name)))

	var cmd *exec.Cmd

	// Use exec.Command directly instead of shell when possible
	if len(service.Args) > 0 {
		// If we have args, pass them directly to the command
		cmd = exec.Command(service.Command, service.Args...)
	} else {
		// No args, just the command
		cmd = exec.Command(service.Command)
	}

	// Handle user switching if specified
	if service.User != "" {
		// For user switching, we need to use shell
		fullCommand := service.Command
		if len(service.Args) > 0 {
			fullCommand = fmt.Sprintf("%s %s", service.Command, joinArgs(service.Args))
		}

		shell := "sh"
		if isBashAvailable() {
			shell = "bash"
		}

		cmd = exec.Command("su", "-s", shell, "-c", fullCommand, service.User)
	}

	// Build environment with service-specific env vars
	cmd.Env = buildServiceEnv(service)
	if len(service.Env) > 0 || service.EnvFile != "" {
		_info(fmt.Sprintf("Service '%s' has custom environment variables configured",
			colorize(ColorCyan, service.Name)))
	}

	ptmx, err := pty.Start(cmd)
	if err != nil {
		return fmt.Errorf("error starting PTY for service %s: %w", service.Name, err)
	}

	_success(fmt.Sprintf("Service '%s' started successfully (PID: %d)",
		colorize(ColorCyan, service.Name), cmd.Process.Pid))

	// Create service context for graceful shutdown
	serviceCtx, serviceCancel := context.WithCancel(shutdownCtx)

	// Register the service as active
	serviceProcess := &ServiceProcess{
		Name:    service.Name,
		Process: cmd,
		PTY:     ptmx,
		Cancel:  serviceCancel,
		State:   ServiceStatePending,
		Config:  service,
	}
	addActiveService(service.Name, serviceProcess)

	// Mark service as running once it's started
	serviceProcess.SetState(ServiceStateRunning)

	// Start health monitor if configured
	startHealthMonitor(serviceProcess)

	// Start log processing in background
	go prefixLogs(ptmx, service.Name, maxLength)

	// Handle graceful shutdown
	go func() {
		<-serviceCtx.Done()
		serviceProcess.SetState(ServiceStateStopping)
		_info(fmt.Sprintf("Gracefully stopping service: %s", colorize(ColorCyan, service.Name)))

		// Send SIGTERM to the process
		if cmd.Process != nil {
			if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
				_error(fmt.Sprintf("Error sending SIGTERM to service '%s': %v",
					colorize(ColorCyan, service.Name), err))
				serviceProcess.SetError(err)
			}

			// Wait for graceful shutdown with configurable timeout
			done := make(chan error, 1)
			go func() {
				done <- cmd.Wait()
			}()

			shutdownTimeout := time.Duration(timeouts.ServiceShutdown) * time.Second
			select {
			case <-time.After(shutdownTimeout):
				// Force kill if not stopped gracefully
				_warn(fmt.Sprintf("Force killing service '%s' after %s timeout",
					colorize(ColorCyan, service.Name), shutdownTimeout))
				if err := cmd.Process.Kill(); err != nil {
					_error(fmt.Sprintf("Error force killing service '%s': %v",
						colorize(ColorCyan, service.Name), err))
					serviceProcess.SetError(err)
				}
				<-done // Wait for the process to actually exit
			case err := <-done:
				if err != nil {
					_error(fmt.Sprintf("Service '%s' exited with error: %v",
						colorize(ColorCyan, service.Name), err))
					serviceProcess.SetError(err)
				} else {
					_success(fmt.Sprintf("Service '%s' stopped gracefully",
						colorize(ColorCyan, service.Name)))
				}
			}
		}

		// Clean up
		if serviceProcess.HealthCancel != nil {
			serviceProcess.HealthCancel()
		}
		if ptmx != nil {
			_ = ptmx.Close()
		}
		removeActiveService(service.Name)
	}()

	// Wait for the command to complete or context cancellation
	select {
	case <-serviceCtx.Done():
		return nil
	default:
		err := cmd.Wait()
		// Service exited on its own
		if serviceProcess.HealthCancel != nil {
			serviceProcess.HealthCancel()
		}
		serviceCancel()
		if err != nil {
			serviceProcess.SetError(err)
		}
		removeActiveService(service.Name)

		// Handle restart policy
		handleServiceExit(serviceProcess, err)
		return err
	}
}

func prefixLogs(reader *os.File, serviceName string, maxLength int) {
	formattedName := formatServiceName(serviceName, maxLength)
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()
		if line != "" {
			fmt.Printf("[%s] %s\n", formattedName, line)
		}
	}
	if err := scanner.Err(); err != nil {
		_info("Error reading logs for service ", serviceName, ": ", err)
	}
}

func getLongestServiceNameLength(services []Service) int {
	maxLength := 0
	for i := range services {
		service := &services[i]
		if len(service.Name) > maxLength {
			maxLength = len(service.Name)
		}
	}
	return maxLength
}

func formatServiceName(serviceName string, maxLength int) string {
	return fmt.Sprintf("%-*s", maxLength, serviceName)
}

func tailLogFile(filePath, serviceName string) {
	file, err := os.Open(filePath)
	if err != nil {
		_info("Error opening log file for service ", serviceName, ": ", err)
		return
	}
	defer file.Close()

	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		_info("Error seeking log file for service ", serviceName, ": ", err)
		return
	}

	scanner := bufio.NewScanner(file)
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-shutdownCtx.Done():
			_info("Stopping log tailing for service:", serviceName)
			return
		case <-ticker.C:
			for scanner.Scan() {
				line := scanner.Text()
				_print(fmt.Sprintf("[%s] %s", serviceName, line))
			}
			if err := scanner.Err(); err != nil {
				_info("Error reading log file for service ", serviceName, ": ", err)
				return
			}
		}
	}
}

// Helper function to get color for service state
func getStateColor(state ServiceState) string {
	switch state {
	case ServiceStatePending:
		return ColorYellow
	case ServiceStateStarting:
		return ColorCyan
	case ServiceStateRunning:
		return ColorGreen
	case ServiceStateStopping:
		return ColorMagenta
	case ServiceStateStopped:
		return ColorGray
	case ServiceStateFailed:
		return ColorRed
	default:
		return ColorWhite
	}
}

// Helper function to format colored text
func colorize(color, text string) string {
	return color + text + ColorReset
}

func _info(a ...interface{}) {
	_logWithColor("INFO", ColorBoldBlue, a...)
}

func _warn(a ...interface{}) {
	_logWithColor("WARN", ColorBoldYellow, a...)
}

func _error(a ...interface{}) {
	_logWithColor("ERROR", ColorBoldRed, a...)
}

func _success(a ...interface{}) {
	_logWithColor("SUCCESS", ColorBoldGreen, a...)
}

func _print(a ...interface{}) {
	message := fmt.Sprint(a...)
	fmt.Println(message)
}

func _debug(isDebug bool, a ...interface{}) {
	if isDebug && !debugMode {
		return
	}
	message := fmt.Sprint(a...)
	fmt.Println(message)
}

func _logWithColor(level, color string, a ...interface{}) {
	prefix := fmt.Sprintf("%s[%-7s]%s", color, level, ColorReset)
	message := fmt.Sprint(a...)
	fmt.Printf("%s %s\n", prefix, message)
}

// =============================================================================
// Environment Variables Functions
// =============================================================================

// loadEnvFile loads environment variables from a .env file
func loadEnvFile(filePath string) (map[string]string, error) {
	env := make(map[string]string)

	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Parse KEY=VALUE
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			// Remove quotes if present
			value = strings.Trim(value, `"'`)
			env[key] = value
		}
	}

	return env, scanner.Err()
}

// buildServiceEnv builds the environment slice for a service
func buildServiceEnv(service Service) []string {
	// Start with system environment
	env := os.Environ()
	envMap := make(map[string]string)

	// Convert to map for easier merging
	for _, e := range env {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}

	// Load env_file if specified
	if service.EnvFile != "" {
		if fileEnv, err := loadEnvFile(service.EnvFile); err == nil {
			for k, v := range fileEnv {
				envMap[k] = v
			}
			_info(fmt.Sprintf("Loaded %d env vars from %s for service '%s'",
				len(fileEnv), service.EnvFile, service.Name))
		} else {
			_warn(fmt.Sprintf("Could not load env_file '%s' for service '%s': %v",
				service.EnvFile, service.Name, err))
		}
	}

	// Override with inline env vars
	for k, v := range service.Env {
		envMap[k] = v
	}

	// Convert back to slice
	result := make([]string, 0, len(envMap))
	for k, v := range envMap {
		result = append(result, fmt.Sprintf("%s=%s", k, v))
	}

	return result
}

// =============================================================================
// Health Check Functions
// =============================================================================

// applyHealthCheckDefaults sets default values for health check config
func applyHealthCheckDefaults(hc *HealthCheckConfig) {
	if hc.Interval == 0 {
		hc.Interval = 30
	}
	if hc.Retries == 0 {
		hc.Retries = 3
	}
	if hc.Timeout == 0 {
		hc.Timeout = 5
	}
	if hc.StartDelay == 0 {
		hc.StartDelay = 10
	}
}

// startHealthMonitor starts a goroutine that monitors service health
func startHealthMonitor(serviceProc *ServiceProcess) {
	if serviceProc.Config.HealthCheck == nil {
		return
	}

	config := *serviceProc.Config.HealthCheck
	applyHealthCheckDefaults(&config)

	// Create a cancellable context for the health monitor
	healthCtx, healthCancel := context.WithCancel(shutdownCtx)
	serviceProc.HealthCancel = healthCancel

	go func() {
		// Wait for initial delay
		_info(fmt.Sprintf("Health monitor for '%s' will start in %ds",
			serviceProc.Name, config.StartDelay))

		select {
		case <-time.After(time.Duration(config.StartDelay) * time.Second):
		case <-healthCtx.Done():
			return
		}

		_info(fmt.Sprintf("Health monitor started for '%s' (interval: %ds, retries: %d)",
			serviceProc.Name, config.Interval, config.Retries))

		ticker := time.NewTicker(time.Duration(config.Interval) * time.Second)
		defer ticker.Stop()

		failureCount := 0

		for {
			select {
			case <-healthCtx.Done():
				return
			case <-ticker.C:
				healthy := performHealthCheck(serviceProc, config)
				if healthy {
					if failureCount > 0 {
						_success(fmt.Sprintf("Service '%s' is healthy again after %d failures",
							serviceProc.Name, failureCount))
					}
					failureCount = 0
					serviceProc.StateMu.Lock()
					serviceProc.HealthyAt = time.Now()
					serviceProc.FailureCount = 0
					serviceProc.StateMu.Unlock()
				} else {
					failureCount++
					serviceProc.StateMu.Lock()
					serviceProc.FailureCount = failureCount
					serviceProc.StateMu.Unlock()

					_warn(fmt.Sprintf("Health check failed for '%s' (%d/%d)",
						serviceProc.Name, failureCount, config.Retries))

					if failureCount >= config.Retries {
						handleUnhealthyService(serviceProc)
						failureCount = 0
					}
				}
			}
		}
	}()
}

// performHealthCheck executes the health check and returns true if healthy
func performHealthCheck(serviceProc *ServiceProcess, config HealthCheckConfig) bool {
	if config.Endpoint != "" {
		return checkHTTPHealth(config.Endpoint, config.Timeout)
	}
	if config.Command != "" {
		return checkCommandHealth(config.Command, config.Timeout)
	}
	return true // No health check configured means always healthy
}

// checkHTTPHealth performs an HTTP GET request to check health
func checkHTTPHealth(endpoint string, timeout int) bool {
	client := &http.Client{
		Timeout: time.Duration(timeout) * time.Second,
	}
	resp, err := client.Get(endpoint)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 400
}

// checkCommandHealth executes a command and checks exit code
func checkCommandHealth(command string, timeout int) bool {
	ctx, cancel := context.WithTimeout(context.Background(),
		time.Duration(timeout)*time.Second)
	defer cancel()

	shell := "sh"
	if isBashAvailable() {
		shell = "bash"
	}

	cmd := exec.CommandContext(ctx, shell, "-c", command)
	return cmd.Run() == nil
}

// handleUnhealthyService handles a service that has failed health checks
func handleUnhealthyService(serviceProc *ServiceProcess) {
	_error(fmt.Sprintf("Service '%s' is unhealthy after %d consecutive failures",
		serviceProc.Name, serviceProc.FailureCount))

	// If service has a restart policy, trigger restart
	if serviceProc.Config.Restart == RestartAlways || serviceProc.Config.Restart == RestartOnFailure {
		_info(fmt.Sprintf("Triggering restart for unhealthy service '%s'", serviceProc.Name))

		// Cancel the current process
		if serviceProc.Cancel != nil {
			serviceProc.Cancel()
		}
	} else if serviceProc.Config.Required {
		_error(fmt.Sprintf("[CRITICAL] Required service '%s' is unhealthy, initiating shutdown",
			serviceProc.Name))
		gracefulShutdown()
	}
}

// =============================================================================
// Restart Policy Functions
// =============================================================================

// handleServiceExit handles service exit with restart policy
func handleServiceExit(serviceProc *ServiceProcess, exitErr error) {
	policy := serviceProc.Config.Restart

	// Determine if we should restart
	shouldRestart := false
	switch policy {
	case RestartAlways:
		shouldRestart = true
	case RestartOnFailure:
		shouldRestart = exitErr != nil
	case RestartNever, "":
		shouldRestart = false
	}

	// Check max restarts limit
	if serviceProc.Config.MaxRestarts > 0 &&
		serviceProc.RestartCount >= serviceProc.Config.MaxRestarts {
		_warn(fmt.Sprintf("Service '%s' reached max restarts (%d), not restarting",
			serviceProc.Name, serviceProc.Config.MaxRestarts))
		shouldRestart = false
	}

	if shouldRestart {
		serviceProc.RestartCount++
		delay := serviceProc.Config.RestartDelay
		if delay == 0 {
			delay = 1 // Default 1 second
		}

		_info(fmt.Sprintf("Restarting service '%s' in %ds (attempt %d/%s)",
			serviceProc.Name, delay, serviceProc.RestartCount,
			formatMaxRestarts(serviceProc.Config.MaxRestarts)))

		// Schedule restart
		time.AfterFunc(time.Duration(delay)*time.Second, func() {
			restartServiceInternal(serviceProc)
		})
	} else if serviceProc.Config.Required && exitErr != nil {
		// Required service failed without restart policy
		_error(fmt.Sprintf("[CRITICAL] Required service '%s' exited with error, initiating shutdown",
			serviceProc.Name))
		gracefulShutdown()
	}
}

// formatMaxRestarts formats the max restarts for display
func formatMaxRestarts(max int) string {
	if max == 0 {
		return "∞"
	}
	return fmt.Sprintf("%d", max)
}

// restartServiceInternal performs the actual service restart
func restartServiceInternal(serviceProc *ServiceProcess) {
	if globalConfig == nil {
		_error(fmt.Sprintf("Cannot restart service '%s': no global config", serviceProc.Name))
		return
	}

	// Check shutdown context
	if shutdownCtx.Err() != nil {
		_info(fmt.Sprintf("Skipping restart of '%s' - shutdown in progress", serviceProc.Name))
		return
	}

	serviceProc.LastRestart = time.Now()
	maxLength := getLongestServiceNameLength(globalConfig.Services)

	_info(fmt.Sprintf("Starting restart of service '%s'", serviceProc.Name))

	go func() {
		if err := startServiceWithPTY(serviceProc.Config, maxLength, globalConfig.Timeouts); err != nil {
			_error(fmt.Sprintf("Error restarting service '%s': %v", serviceProc.Name, err))
		}
	}()
}

func _printEnvVariables() {
	_info("Function entry logged.")
	_debug(true, "| ---------------- START - ENVIRONMENT VARS ---------------- |")

	envVars := os.Environ()
	for i, env := range envVars {
		if i == len(envVars)-1 {
			fmt.Printf("%s", env)
		} else {
			fmt.Printf("%s\n", env)
		}
	}

	_debug(true, "| ---------------- CLOSE - ENVIRONMENT VARS ---------------- |")
}

// Validation functions
func validateConfig(config *Config) error {
	var errors ValidationErrors

	// Set default timeouts if not specified
	if config.Timeouts.PostScript == 0 {
		config.Timeouts.PostScript = 7
	}
	if config.Timeouts.ServiceShutdown == 0 {
		config.Timeouts.ServiceShutdown = 10
	}
	if config.Timeouts.GlobalShutdown == 0 {
		config.Timeouts.GlobalShutdown = 30
	}
	if config.Timeouts.DependencyWait == 0 {
		config.Timeouts.DependencyWait = 300 // 5 minutes
	}

	// Validate services
	serviceNames := make(map[string]bool)
	for i := range config.Services {
		service := &config.Services[i]
		// Validate service
		if errs := validateService(*service); len(errs) > 0 {
			errors = append(errors, errs...)
		}

		// Check for duplicate service names
		if serviceNames[service.Name] {
			errors = append(errors, ValidationError{
				Field:   "name",
				Service: service.Name,
				Message: "duplicate service name",
			})
		}
		serviceNames[service.Name] = true

		// Set default enabled if not specified
		if service.Enabled == nil {
			config.Services[i].Enabled = new(bool)
			*config.Services[i].Enabled = true
		}
	}

	// Validate dependencies
	if err := validateDependencies(config.Services); err != nil {
		errors = append(errors, ValidationError{
			Field:   "dependencies",
			Message: err.Error(),
		})
	}

	if len(errors) > 0 {
		return errors
	}

	return nil
}

func validateService(service Service) ValidationErrors {
	var errors ValidationErrors

	errors = append(errors, validateRequiredFields(&service)...)
	errors = append(errors, validateServiceName(&service)...)
	errors = append(errors, validateCommand(&service)...)
	errors = append(errors, validateScripts(&service)...)
	errors = append(errors, validateLogFile(&service)...)
	errors = append(errors, validateWaitAfter(&service)...)
	errors = append(errors, validateUser(&service)...)
	errors = append(errors, validateHealthCheck(&service)...)
	errors = append(errors, validateRestartPolicy(&service)...)
	errors = append(errors, validateEnvFile(&service)...)

	return errors
}

func validateRequiredFields(service *Service) ValidationErrors {
	var errors ValidationErrors

	if service.Name == "" {
		errors = append(errors, ValidationError{
			Field:   "name",
			Service: service.Name,
			Message: "service name is required",
		})
	}

	if service.Command == "" {
		errors = append(errors, ValidationError{
			Field:   "command",
			Service: service.Name,
			Message: "command is required",
		})
	}

	return errors
}

func validateServiceName(service *Service) ValidationErrors {
	var errors ValidationErrors

	if service.Name != "" {
		validName := regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
		if !validName.MatchString(service.Name) {
			errors = append(errors, ValidationError{
				Field:   "name",
				Service: service.Name,
				Message: "service name must contain only alphanumeric characters, dashes, and underscores",
			})
		}
	}

	return errors
}

func validateCommand(service *Service) ValidationErrors {
	var errors ValidationErrors

	if service.Command != "" && !strings.Contains(service.Command, " ") {
		if _, err := exec.LookPath(service.Command); err != nil {
			if !filepath.IsAbs(service.Command) {
				errors = append(errors, ValidationError{
					Field:   "command",
					Service: service.Name,
					Message: fmt.Sprintf("command '%s' not found in PATH", service.Command),
				})
			} else {
				if _, err := os.Stat(service.Command); os.IsNotExist(err) {
					errors = append(errors, ValidationError{
						Field:   "command",
						Service: service.Name,
						Message: fmt.Sprintf("command file '%s' does not exist", service.Command),
					})
				}
			}
		}
	}

	return errors
}

func validateScripts(service *Service) ValidationErrors {
	var errors ValidationErrors

	if service.PreScript != "" {
		if _, err := os.Stat(service.PreScript); os.IsNotExist(err) {
			errors = append(errors, ValidationError{
				Field:   "pre_script",
				Service: service.Name,
				Message: fmt.Sprintf("pre-script file '%s' does not exist", service.PreScript),
			})
		}
	}

	if service.PosScript != "" {
		if _, err := os.Stat(service.PosScript); os.IsNotExist(err) {
			errors = append(errors, ValidationError{
				Field:   "pos_script",
				Service: service.Name,
				Message: fmt.Sprintf("post-script file '%s' does not exist", service.PosScript),
			})
		}
	}

	return errors
}

func validateLogFile(service *Service) ValidationErrors {
	var errors ValidationErrors

	if service.LogFile != "" {
		logDir := filepath.Dir(service.LogFile)
		if _, err := os.Stat(logDir); os.IsNotExist(err) {
			errors = append(errors, ValidationError{
				Field:   "log_file",
				Service: service.Name,
				Message: fmt.Sprintf("log file directory '%s' does not exist", logDir),
			})
		}
	}

	return errors
}

func validateWaitAfter(service *Service) ValidationErrors {
	var errors ValidationErrors

	if service.WaitAfter != nil && service.WaitAfter.IsPerDep {
		for depName, waitTime := range service.WaitAfter.PerDep {
			if waitTime < 0 || waitTime > 300 {
				errors = append(errors, ValidationError{
					Field:   "wait_after",
					Service: service.Name,
					Message: fmt.Sprintf("wait_after for dependency '%s' must be between 0 and 300 seconds", depName),
				})
			}
		}
	} else if service.WaitAfter != nil {
		if service.WaitAfter.Global < 0 || service.WaitAfter.Global > 300 {
			errors = append(errors, ValidationError{
				Field:   "wait_after",
				Service: service.Name,
				Message: "wait_after must be between 0 and 300 seconds",
			})
		}
	}

	return errors
}

func validateUser(service *Service) ValidationErrors {
	var errors ValidationErrors

	if service.User != "" {
		if _, err := exec.Command("id", service.User).Output(); err != nil {
			errors = append(errors, ValidationError{
				Field:   "user",
				Service: service.Name,
				Message: fmt.Sprintf("user '%s' does not exist", service.User),
			})
		}
	}

	return errors
}

func validateHealthCheck(service *Service) ValidationErrors {
	var errors ValidationErrors

	if service.HealthCheck != nil {
		hc := service.HealthCheck

		// Validate endpoint URL format
		if hc.Endpoint != "" {
			if !strings.HasPrefix(hc.Endpoint, "http://") && !strings.HasPrefix(hc.Endpoint, "https://") {
				errors = append(errors, ValidationError{
					Field:   "health_check.endpoint",
					Service: service.Name,
					Message: "endpoint must start with http:// or https://",
				})
			}
		}

		// Validate interval
		if hc.Interval < 0 {
			errors = append(errors, ValidationError{
				Field:   "health_check.interval",
				Service: service.Name,
				Message: "interval must be a positive number",
			})
		}

		// Validate retries
		if hc.Retries < 0 {
			errors = append(errors, ValidationError{
				Field:   "health_check.retries",
				Service: service.Name,
				Message: "retries must be a positive number",
			})
		}

		// Validate timeout
		if hc.Timeout < 0 {
			errors = append(errors, ValidationError{
				Field:   "health_check.timeout",
				Service: service.Name,
				Message: "timeout must be a positive number",
			})
		}

		// Validate start_delay
		if hc.StartDelay < 0 {
			errors = append(errors, ValidationError{
				Field:   "health_check.start_delay",
				Service: service.Name,
				Message: "start_delay must be a positive number",
			})
		}
	}

	return errors
}

func validateRestartPolicy(service *Service) ValidationErrors {
	var errors ValidationErrors

	// Validate restart policy value
	if service.Restart != "" {
		validPolicies := []RestartPolicy{RestartNever, RestartOnFailure, RestartAlways}
		isValid := false
		for _, p := range validPolicies {
			if service.Restart == p {
				isValid = true
				break
			}
		}
		if !isValid {
			errors = append(errors, ValidationError{
				Field:   "restart",
				Service: service.Name,
				Message: fmt.Sprintf("restart must be one of: never, on-failure, always (got: %s)", service.Restart),
			})
		}
	}

	// Validate restart_delay
	if service.RestartDelay < 0 {
		errors = append(errors, ValidationError{
			Field:   "restart_delay",
			Service: service.Name,
			Message: "restart_delay must be a positive number",
		})
	}

	// Validate max_restarts
	if service.MaxRestarts < 0 {
		errors = append(errors, ValidationError{
			Field:   "max_restarts",
			Service: service.Name,
			Message: "max_restarts must be a positive number (0 = unlimited)",
		})
	}

	if service.Oneshot {
		if service.Restart == RestartAlways || service.Restart == RestartOnFailure {
			errors = append(errors, ValidationError{
				Field:   "restart",
				Service: service.Name,
				Message: "oneshot services must use restart='never' (or omit restart)",
			})
		}
	}

	return errors
}

func validateEnvFile(service *Service) ValidationErrors {
	var errors ValidationErrors

	if service.EnvFile != "" {
		if _, err := os.Stat(service.EnvFile); os.IsNotExist(err) {
			errors = append(errors, ValidationError{
				Field:   "env_file",
				Service: service.Name,
				Message: fmt.Sprintf("env_file '%s' does not exist", service.EnvFile),
			})
		}
	}

	return errors
}

func validateDependencies(services []Service) error {
	serviceMap := make(map[string]Service)
	for i := range services {
		service := &services[i]
		serviceMap[service.Name] = *service
	}

	// Check if all dependencies exist
	for i := range services {
		service := &services[i]
		for _, dep := range service.DependsOn {
			if _, exists := serviceMap[dep]; !exists {
				return fmt.Errorf("service '%s' depends on non-existent service '%s'", service.Name, dep)
			}
		}

		// Validate wait_after map references
		if service.WaitAfter != nil && service.WaitAfter.IsPerDep {
			for depName := range service.WaitAfter.PerDep {
				found := false
				for _, dep := range service.DependsOn {
					if dep == depName {
						found = true
						break
					}
				}
				if !found {
					return fmt.Errorf("service '%s' has wait_after for '%s' but doesn't depend on it", service.Name, depName)
				}
			}
		}
	}

	// Check for circular dependencies
	for i := range services {
		service := &services[i]
		if hasCycles(service.Name, serviceMap, make(map[string]bool), make(map[string]bool)) {
			return fmt.Errorf("circular dependency detected involving service '%s'", service.Name)
		}
	}

	return nil
}

func hasCycles(serviceName string, serviceMap map[string]Service, visited, recursionStack map[string]bool) bool {
	visited[serviceName] = true
	recursionStack[serviceName] = true

	service, exists := serviceMap[serviceName]
	if !exists {
		return false
	}

	for _, dep := range service.DependsOn {
		if !visited[dep] {
			if hasCycles(dep, serviceMap, visited, recursionStack) {
				return true
			}
		} else if recursionStack[dep] {
			return true
		}
	}

	recursionStack[serviceName] = false
	return false
}

// Enhanced service status reporting
func printServiceStatuses() {
	servicesMutex.RLock()
	defer servicesMutex.RUnlock()

	fmt.Println(colorize(ColorBoldCyan, "\n=== Service Status Summary ==="))
	for name, serviceProc := range activeServices {
		uptime := time.Since(serviceProc.StartTime).Round(time.Second)
		state := serviceProc.GetState()
		stateColored := colorize(getStateColor(state), state.String())

		status := fmt.Sprintf("  %s │ State: %s │ Uptime: %s",
			colorize(ColorCyan, fmt.Sprintf("%-15s", name)),
			stateColored,
			colorize(ColorWhite, uptime.String()))

		if serviceProc.LastError != nil {
			status += fmt.Sprintf(" │ %s: %s",
				colorize(ColorRed, "Error"),
				serviceProc.LastError)
		}

		fmt.Println(status)
	}
	fmt.Println(colorize(ColorBoldCyan, "=== End Status Summary ===\n"))
}

// IPC functions
func startIPCServer() error {
	// Remove existing socket
	_ = os.Remove(socketPath)

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("failed to create Unix socket: %w", err)
	}

	ipcServer = listener
	_success(fmt.Sprintf("IPC server started at %s", colorize(ColorCyan, socketPath)))

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				select {
				case <-shutdownCtx.Done():
					return // Shutting down
				default:
					_info("Error accepting IPC connection:", err)
					continue
				}
			}

			go handleIPCConnection(conn)
		}
	}()

	return nil
}

func handleIPCConnection(conn net.Conn) {
	defer conn.Close()

	decoder := json.NewDecoder(conn)
	encoder := json.NewEncoder(conn)

	var cmd IPCCommand
	if err := decoder.Decode(&cmd); err != nil {
		_info("Error decoding IPC command:", err)
		return
	}

	var response IPCResponse

	switch cmd.Type {
	case CmdListServices:
		response = handleListServices()
	case CmdRestartService:
		response = handleRestartService(cmd.ServiceName)
	case CmdGetStatus:
		response = handleGetStatus()
	default:
		response = IPCResponse{
			Success: false,
			Message: "Unknown command type",
		}
	}

	if err := encoder.Encode(response); err != nil {
		_info("Error encoding IPC response:", err)
	}
}

func handleListServices() IPCResponse {
	servicesMutex.RLock()
	defer servicesMutex.RUnlock()

	services := make([]ServiceInfo, 0, len(activeServices))
	for name, serviceProc := range activeServices {
		var lastError string
		if serviceProc.LastError != nil {
			lastError = serviceProc.LastError.Error()
		}

		services = append(services, ServiceInfo{
			Name:      name,
			State:     serviceProc.GetState(),
			PID:       serviceProc.GetPID(),
			Uptime:    time.Since(serviceProc.StartTime),
			LastError: lastError,
			Required:  serviceProc.Config.Required,
		})
	}

	return IPCResponse{
		Success:  true,
		Services: services,
	}
}

func handleRestartService(serviceName string) IPCResponse {
	servicesMutex.Lock()
	defer servicesMutex.Unlock()

	serviceProc, exists := activeServices[serviceName]
	if !exists {
		return IPCResponse{
			Success: false,
			Message: fmt.Sprintf("Service '%s' not found", serviceName),
		}
	}

	_info("Restarting service:", serviceName)

	// Stop the current service
	serviceProc.SetState(ServiceStateStopping)
	if serviceProc.Cancel != nil {
		serviceProc.Cancel()
	}

	// Wait a moment for graceful stop
	time.Sleep(2 * time.Second)

	// Force kill if still running
	if serviceProc.Process != nil && serviceProc.Process.Process != nil {
		if err := serviceProc.Process.Process.Kill(); err != nil {
			_info("Error killing service during restart:", err)
		}
	}

	// Clean up
	if serviceProc.PTY != nil {
		_ = serviceProc.PTY.Close()
	}
	delete(activeServices, serviceName)

	// Restart the service
	go func() {
		time.Sleep(1 * time.Second) // Brief pause before restart

		if globalConfig != nil {
			maxLength := getLongestServiceNameLength(globalConfig.Services)
			if err := startServiceWithPTY(serviceProc.Config, maxLength, globalConfig.Timeouts); err != nil {
				_info("Error restarting service", serviceName, ":", err)
			}
		}
	}()

	return IPCResponse{
		Success: true,
		Message: fmt.Sprintf("Service '%s' restart initiated", serviceName),
	}
}

func handleGetStatus() IPCResponse {
	servicesMutex.RLock()
	defer servicesMutex.RUnlock()

	totalServices := len(activeServices)
	runningServices := 0
	failedServices := 0

	for _, serviceProc := range activeServices {
		state := serviceProc.GetState()
		if state == ServiceStateRunning {
			runningServices++
		} else if state == ServiceStateFailed {
			failedServices++
		}
	}

	message := fmt.Sprintf("Total: %d, Running: %d, Failed: %d",
		totalServices, runningServices, failedServices)

	return IPCResponse{
		Success: true,
		Message: message,
	}
}

// Client functions for CLI commands
func sendIPCCommand(cmd IPCCommand) (*IPCResponse, error) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("could not connect to Go Overlay daemon: %w", err)
	}
	defer conn.Close()

	encoder := json.NewEncoder(conn)
	decoder := json.NewDecoder(conn)

	if err := encoder.Encode(cmd); err != nil {
		return nil, fmt.Errorf("error sending command: %w", err)
	}

	var response IPCResponse
	if err := decoder.Decode(&response); err != nil {
		return nil, fmt.Errorf("error receiving response: %w", err)
	}

	return &response, nil
}

func listServices() error {
	response, err := sendIPCCommand(IPCCommand{Type: CmdListServices})
	if err != nil {
		return err
	}

	if !response.Success {
		return fmt.Errorf("%s", response.Message)
	}

	// Header with colors
	fmt.Printf("%s %-15s %s %-10s %s %-8s %s %-12s %s %-8s %s %s%s\n",
		ColorBoldWhite, "NAME",
		ColorBoldWhite, "STATE",
		ColorBoldWhite, "PID",
		ColorBoldWhite, "UPTIME",
		ColorBoldWhite, "REQUIRED",
		ColorBoldWhite, "LAST_ERROR", ColorReset)
	fmt.Println(colorize(ColorGray, strings.Repeat("─", 85)))

	for _, service := range response.Services {
		uptime := service.Uptime.Round(time.Second)
		required := colorize(ColorGray, "No")
		if service.Required {
			required = colorize(ColorYellow, "Yes")
		}

		lastError := service.LastError
		if len(lastError) > 30 {
			lastError = lastError[:27] + "..."
		}

		stateColor := getStateColor(service.State)
		nameColor := ColorCyan
		pidColor := ColorWhite

		if lastError != "" {
			lastError = colorize(ColorRed, lastError)
		} else {
			lastError = colorize(ColorGray, "-")
		}

		fmt.Printf("%s%-15s%s %s%-10s%s %s%-8d%s %s%-12s%s %s%-8s%s %s\n",
			nameColor, service.Name, ColorReset,
			stateColor, service.State, ColorReset,
			pidColor, service.PID, ColorReset,
			ColorWhite, uptime, ColorReset,
			ColorWhite, required, ColorReset,
			lastError)
	}

	return nil
}

func restartService(serviceName string) error {
	response, err := sendIPCCommand(IPCCommand{
		Type:        CmdRestartService,
		ServiceName: serviceName,
	})
	if err != nil {
		return err
	}

	if response.Success {
		fmt.Println(colorize(ColorGreen, "✓ "+response.Message))
	} else {
		return fmt.Errorf("%s", response.Message)
	}

	return nil
}

func showStatus() error {
	response, err := sendIPCCommand(IPCCommand{Type: CmdGetStatus})
	if err != nil {
		return err
	}

	if response.Success {
		fmt.Printf("%s: %s\n",
			colorize(ColorBoldCyan, "System Status"),
			colorize(ColorGreen, response.Message))
	} else {
		return fmt.Errorf("%s", response.Message)
	}

	return nil
}
