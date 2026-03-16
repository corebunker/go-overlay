//go:build integration
// +build integration

package main

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func createTempConfig(t *testing.T, content string) string {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "services.toml")

	err := os.WriteFile(configPath, []byte(content), 0o644)
	if err != nil {
		t.Fatalf("Failed to create temp config: %v", err)
	}

	return configPath
}

func TestIntegrationLoadValidConfig(t *testing.T) {
	configContent := `
[timeouts]
post_script_timeout = 5
service_shutdown_timeout = 8
global_shutdown_timeout = 25
dependency_wait_timeout = 200

[[services]]
name = "test-service-1"
command = "/bin/sleep"
args = ["10"]
enabled = true

[[services]]
name = "test-service-2"
command = "/bin/echo"
args = ["hello", "world"]
enabled = true
depends_on = "test-service-1"
wait_after = 2
`

	configPath := createTempConfig(t, configContent)

	file, err := os.Open(configPath)
	if err != nil {
		t.Fatalf("Failed to open config: %v", err)
	}
	defer file.Close()

	config, err := parseConfig(file)
	if err != nil {
		t.Fatalf("Failed to decode config: %v", err)
	}

	for _, s := range config.Services {
		t.Logf("svc %q deps=%v", s.Name, s.DependsOn)
	}

	if err := validateConfig(&config); err != nil {
		t.Errorf("validateConfig() failed: %v", err)
	}

	if config.Timeouts.PostScript != 5 {
		t.Errorf("PostScript timeout = %v, want 5", config.Timeouts.PostScript)
	}

	if len(config.Services) != 2 {
		t.Errorf("Number of services = %v, want 2", len(config.Services))
	}

	if len(config.Services[1].DependsOn) != 1 {
		t.Errorf("Service 2 dependencies = %v, want 1", len(config.Services[1].DependsOn))
	}
}

func TestIntegrationComplexDependencies(t *testing.T) {
	configContent := `
[[services]]
name = "database"
command = "/bin/sleep"
args = ["30"]
enabled = true

[[services]]
name = "cache"
command = "/bin/sleep"
args = ["30"]
enabled = true

[[services]]
name = "api"
command = "/bin/sleep"
args = ["30"]
enabled = true
depends_on = ["database", "cache"]
wait_after = { database = 5, cache = 3 }

[[services]]
name = "frontend"
command = "/bin/sleep"
args = ["30"]
enabled = true
depends_on = "api"
wait_after = 2
`

	configPath := createTempConfig(t, configContent)

	file, err := os.Open(configPath)
	if err != nil {
		t.Fatalf("Failed to open config: %v", err)
	}
	defer file.Close()

	config, err := parseConfig(file)
	if err != nil {
		t.Fatalf("Failed to decode config: %v", err)
	}

	var apiService Service
	for _, s := range config.Services {
		if s.Name == "api" {
			apiService = s
			break
		}
	}
	if len(apiService.DependsOn) != 2 {
		t.Errorf("API service dependencies = %v, want 2", len(apiService.DependsOn))
	}

	if !apiService.WaitAfter.IsPerDep {
		t.Error("API service should have per-dependency wait times")
	}

	if apiService.WaitAfter.GetWaitTime("database") != 5 {
		t.Errorf("Wait time for database = %v, want 5", apiService.WaitAfter.GetWaitTime("database"))
	}

	if err := validateConfig(&config); err != nil {
		t.Errorf("validateConfig() failed: %v", err)
	}
}

func TestIntegrationInvalidConfigurations(t *testing.T) {
	tests := []struct {
		name          string
		configContent string
		shouldFail    bool
	}{
		{
			name: "Circular dependency",
			configContent: `
[[services]]
name = "service1"
command = "/bin/echo"
depends_on = "service2"

[[services]]
name = "service2"
command = "/bin/echo"
depends_on = "service1"
`,
			shouldFail: true,
		},
		{
			name: "Non-existent dependency",
			configContent: `
[[services]]
name = "service1"
command = "/bin/echo"
depends_on = "nonexistent"
`,
			shouldFail: true,
		},
		{
			name: "Duplicate service names",
			configContent: `
[[services]]
name = "duplicate"
command = "/bin/echo"

[[services]]
name = "duplicate"
command = "/bin/sleep"
`,
			shouldFail: true,
		},
		{
			name: "Invalid service name",
			configContent: `
[[services]]
name = "invalid name!"
command = "/bin/echo"
`,
			shouldFail: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath := createTempConfig(t, tt.configContent)

			file, err := os.Open(configPath)
			if err != nil {
				t.Fatalf("Failed to open config: %v", err)
			}
			defer file.Close()

			config, err := parseConfig(file)
			if err != nil {
				if !tt.shouldFail {
					t.Fatalf("Failed to decode config: %v", err)
				}
				return
			}

			err = validateConfig(&config)
			if tt.shouldFail && err == nil {
				t.Error("Expected validation to fail but it succeeded")
			}
			if !tt.shouldFail && err != nil {
				t.Errorf("Expected validation to succeed but got error: %v", err)
			}
		})
	}
}

func TestIntegrationServiceLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	shutdownCtx, shutdownCancel = context.WithCancel(context.Background())
	defer shutdownCancel()

	service := Service{
		Name:    "test-service",
		Command: "/bin/echo",
		Args:    []string{"test"},
	}

	serviceProc := &ServiceProcess{
		Name:   service.Name,
		State:  ServiceStatePending,
		Config: service,
	}

	serviceProc.SetState(ServiceStateStarting)
	if serviceProc.GetState() != ServiceStateStarting {
		t.Errorf("State = %v, want %v", serviceProc.GetState(), ServiceStateStarting)
	}

	serviceProc.SetState(ServiceStateRunning)
	if serviceProc.GetState() != ServiceStateRunning {
		t.Errorf("State = %v, want %v", serviceProc.GetState(), ServiceStateRunning)
	}

	serviceProc.SetState(ServiceStateStopping)
	if serviceProc.GetState() != ServiceStateStopping {
		t.Errorf("State = %v, want %v", serviceProc.GetState(), ServiceStateStopping)
	}

	serviceProc.SetState(ServiceStateStopped)
	if serviceProc.GetState() != ServiceStateStopped {
		t.Errorf("State = %v, want %v", serviceProc.GetState(), ServiceStateStopped)
	}
}

func TestIntegrationPreScript(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "pre-script.sh")
	markerPath := filepath.Join(tmpDir, "marker.txt")

	scriptContent := "#!/bin/sh\necho 'pre-script executed' > " + markerPath + "\n"
	err := os.WriteFile(scriptPath, []byte(scriptContent), 0o755)
	if err != nil {
		t.Fatalf("Failed to create pre-script: %v", err)
	}

	err = runScript(scriptPath)
	if err != nil {
		t.Errorf("runScript() failed: %v", err)
	}

	if _, err := os.Stat(markerPath); os.IsNotExist(err) {
		t.Error("Pre-script did not create marker file")
	}

	content, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("Failed to read marker file: %v", err)
	}

	expected := "pre-script executed\n"
	if string(content) != expected {
		t.Errorf("Marker file content = %q, want %q", string(content), expected)
	}
}

func TestIntegrationTimeouts(t *testing.T) {
	config := &Config{
		Services: []Service{
			{Name: "test", Command: "/bin/echo"},
		},
		Timeouts: Timeouts{
			PostScript:      3,
			ServiceShutdown: 5,
			GlobalShutdown:  15,
			DependencyWait:  100,
		},
	}

	err := validateConfig(config)
	if err != nil {
		t.Errorf("validateConfig() failed: %v", err)
	}

	if config.Timeouts.PostScript != 3 {
		t.Errorf("PostScript timeout = %v, want 3", config.Timeouts.PostScript)
	}
	if config.Timeouts.ServiceShutdown != 5 {
		t.Errorf("ServiceShutdown timeout = %v, want 5", config.Timeouts.ServiceShutdown)
	}
}

func TestIntegrationWaitAfterDependencies(t *testing.T) {
	shutdownCtx, shutdownCancel = context.WithCancel(context.Background())
	defer shutdownCancel()

	startedServices := make(map[string]bool)
	failedServices := make(map[string]bool)
	var mu sync.Mutex

	mu.Lock()
	startedServices["dep-service"] = true
	mu.Unlock()

	done := make(chan bool)
	go func() {
		result := waitForDependency("dep-service", 1, &mu, startedServices, failedServices, 10)
		done <- result
	}()

	select {
	case result := <-done:
		if !result {
			t.Error("waitForDependency returned false, expected true")
		}
	case <-time.After(5 * time.Second):
		t.Error("waitForDependency timed out")
	}
}

func TestIntegrationDisabledServices(t *testing.T) {
	enabledTrue := true
	enabledFalse := false

	config := &Config{
		Services: []Service{
			{
				Name:    "enabled-service",
				Command: "/bin/echo",
				Enabled: &enabledTrue,
			},
			{
				Name:    "disabled-service",
				Command: "/bin/echo",
				Enabled: &enabledFalse,
			},
		},
	}

	err := validateConfig(config)
	if err != nil {
		t.Errorf("validateConfig() failed: %v", err)
	}

	enabledCount := 0
	for _, service := range config.Services {
		if service.Enabled != nil && *service.Enabled {
			enabledCount++
		}
	}

	if enabledCount != 1 {
		t.Errorf("Enabled services count = %v, want 1", enabledCount)
	}
}

func TestIntegrationLogFileService(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "service.log")

	err := os.MkdirAll(tmpDir, 0o755)
	if err != nil {
		t.Fatalf("Failed to create log directory: %v", err)
	}

	service := Service{
		Name:    "log-service",
		Command: "/bin/echo",
		LogFile: logFile,
	}

	errs := validateService(service)
	if len(errs) > 0 {
		t.Errorf("validateService() failed: %v", errs)
	}
}

func TestIntegrationUserValidation(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("Skipping user validation test (requires root)")
	}

	tests := []struct {
		name      string
		user      string
		shouldErr bool
	}{
		{
			name:      "Valid user - root",
			user:      "root",
			shouldErr: false,
		},
		{
			name:      "Invalid user",
			user:      "nonexistentuser12345",
			shouldErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := Service{
				Name:    "test-service",
				Command: "/bin/echo",
				User:    tt.user,
			}

			errs := validateService(service)
			hasError := len(errs) > 0

			if tt.shouldErr && !hasError {
				t.Error("Expected validation error but got none")
			}
			if !tt.shouldErr && hasError {
				t.Errorf("Expected no validation error but got: %v", errs)
			}
		})
	}
}

func TestIntegrationRequiredServices(t *testing.T) {
	config := &Config{
		Services: []Service{
			{
				Name:     "critical-service",
				Command:  "/bin/echo",
				Required: true,
			},
			{
				Name:     "optional-service",
				Command:  "/bin/echo",
				Required: false,
			},
		},
	}

	err := validateConfig(config)
	if err != nil {
		t.Errorf("validateConfig() failed: %v", err)
	}

	if !config.Services[0].Required {
		t.Error("First service should be required")
	}
	if config.Services[1].Required {
		t.Error("Second service should not be required")
	}
}

func BenchmarkIntegrationLoadConfig(b *testing.B) {
	configContent := `
[[services]]
name = "service1"
command = "/bin/echo"

[[services]]
name = "service2"
command = "/bin/echo"
depends_on = "service1"
`

	tmpDir := b.TempDir()
	configPath := filepath.Join(tmpDir, "services.toml")
	os.WriteFile(configPath, []byte(configContent), 0o644)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		file, _ := os.Open(configPath)
		var cfg Config
		cfg, _ = parseConfig(file)
		validateConfig(&cfg)
		file.Close()
	}
}
