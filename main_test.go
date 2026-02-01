package main

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// Test ServiceState String method
func TestServiceStateString(t *testing.T) {
	tests := []struct {
		state    ServiceState
		expected string
	}{
		{ServiceStatePending, "PENDING"},
		{ServiceStateStarting, "STARTING"},
		{ServiceStateRunning, "RUNNING"},
		{ServiceStateStopping, "STOPPING"},
		{ServiceStateStopped, "STOPPED"},
		{ServiceStateFailed, "FAILED"},
		{ServiceState(999), "UNKNOWN"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.state.String(); got != tt.expected {
				t.Errorf("ServiceState.String() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// Test getStateColor function
func TestGetStateColor(t *testing.T) {
	tests := []struct {
		state    ServiceState
		expected string
	}{
		{ServiceStatePending, ColorYellow},
		{ServiceStateStarting, ColorCyan},
		{ServiceStateRunning, ColorGreen},
		{ServiceStateStopping, ColorMagenta},
		{ServiceStateStopped, ColorGray},
		{ServiceStateFailed, ColorRed},
		{ServiceState(999), ColorWhite},
	}

	for _, tt := range tests {
		t.Run(tt.state.String(), func(t *testing.T) {
			if got := getStateColor(tt.state); got != tt.expected {
				t.Errorf("getStateColor() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// Test colorize function
func TestColorize(t *testing.T) {
	tests := []struct {
		name     string
		color    string
		text     string
		expected string
	}{
		{"Red text", ColorRed, "error", ColorRed + "error" + ColorReset},
		{"Green text", ColorGreen, "success", ColorGreen + "success" + ColorReset},
		{"Empty text", ColorBlue, "", ColorBlue + "" + ColorReset},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := colorize(tt.color, tt.text); got != tt.expected {
				t.Errorf("colorize() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// Test DependsOnField UnmarshalTOML
func TestDependsOnFieldUnmarshalTOML(t *testing.T) {
	tests := []struct {
		name      string
		input     interface{}
		expected  []string
		shouldErr bool
	}{
		{
			name:      "Single string",
			input:     "service1",
			expected:  []string{"service1"},
			shouldErr: false,
		},
		{
			name:      "Array of strings",
			input:     []interface{}{"service1", "service2"},
			expected:  []string{"service1", "service2"},
			shouldErr: false,
		},
		{
			name:      "Invalid type",
			input:     123,
			expected:  nil,
			shouldErr: true,
		},
		{
			name:      "Array with non-string",
			input:     []interface{}{"service1", 123},
			expected:  nil,
			shouldErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var d DependsOnField
			err := d.UnmarshalTOML(tt.input)

			if tt.shouldErr {
				if err == nil {
					t.Error("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if len(d) != len(tt.expected) {
				t.Errorf("Length mismatch: got %d, want %d", len(d), len(tt.expected))
				return
			}

			for i, v := range d {
				if v != tt.expected[i] {
					t.Errorf("Value mismatch at index %d: got %v, want %v", i, v, tt.expected[i])
				}
			}
		})
	}
}

// Test WaitAfterField UnmarshalTOML
func TestWaitAfterFieldUnmarshalTOML(t *testing.T) {
	tests := []struct {
		name      string
		input     interface{}
		expected  WaitAfterField
		shouldErr bool
	}{
		{
			name:  "Integer value",
			input: int64(5),
			expected: WaitAfterField{
				Global:   5,
				IsPerDep: false,
			},
			shouldErr: false,
		},
		{
			name: "Map value",
			input: map[string]interface{}{
				"service1": int64(10),
				"service2": int64(20),
			},
			expected: WaitAfterField{
				PerDep: map[string]int{
					"service1": 10,
					"service2": 20,
				},
				IsPerDep: true,
			},
			shouldErr: false,
		},
		{
			name:      "Invalid type",
			input:     "invalid",
			expected:  WaitAfterField{},
			shouldErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var w WaitAfterField
			err := w.UnmarshalTOML(tt.input)

			if tt.shouldErr {
				if err == nil {
					t.Error("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if w.IsPerDep != tt.expected.IsPerDep {
				t.Errorf("IsPerDep mismatch: got %v, want %v", w.IsPerDep, tt.expected.IsPerDep)
			}

			if !w.IsPerDep && w.Global != tt.expected.Global {
				t.Errorf("Global mismatch: got %v, want %v", w.Global, tt.expected.Global)
			}
		})
	}
}

// Test WaitAfterField GetWaitTime
func TestWaitAfterFieldGetWaitTime(t *testing.T) {
	tests := []struct {
		name     string
		field    WaitAfterField
		depName  string
		expected int
	}{
		{
			name: "Global wait time",
			field: WaitAfterField{
				Global:   10,
				IsPerDep: false,
			},
			depName:  "any-service",
			expected: 10,
		},
		{
			name: "Per-dep wait time exists",
			field: WaitAfterField{
				PerDep: map[string]int{
					"service1": 15,
					"service2": 20,
				},
				IsPerDep: true,
			},
			depName:  "service1",
			expected: 15,
		},
		{
			name: "Per-dep wait time not found",
			field: WaitAfterField{
				PerDep: map[string]int{
					"service1": 15,
				},
				IsPerDep: true,
			},
			depName:  "service2",
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.field.GetWaitTime(tt.depName); got != tt.expected {
				t.Errorf("GetWaitTime() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// Test validateService
func TestValidateService(t *testing.T) {
	tests := []struct {
		name      string
		service   Service
		shouldErr bool
		errCount  int
	}{
		{
			name: "Valid service",
			service: Service{
				Name:    "test-service",
				Command: "/bin/echo",
			},
			shouldErr: false,
			errCount:  0,
		},
		{
			name: "Missing name",
			service: Service{
				Command: "/bin/echo",
			},
			shouldErr: true,
			errCount:  1,
		},
		{
			name: "Missing command",
			service: Service{
				Name: "test-service",
			},
			shouldErr: true,
			errCount:  1,
		},
		{
			name: "Invalid name characters",
			service: Service{
				Name:    "test service!",
				Command: "/bin/echo",
			},
			shouldErr: true,
			errCount:  1,
		},
		{
			name: "Command not found",
			service: Service{
				Name:    "test-service",
				Command: "nonexistent-command-xyz",
			},
			shouldErr: true,
			errCount:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := validateService(tt.service)

			if tt.shouldErr && len(errs) == 0 {
				t.Error("Expected errors but got none")
			}

			if !tt.shouldErr && len(errs) > 0 {
				t.Errorf("Expected no errors but got: %v", errs)
			}

			if tt.errCount > 0 && len(errs) != tt.errCount {
				t.Errorf("Expected %d errors but got %d", tt.errCount, len(errs))
			}
		})
	}
}

// Test validateDependencies
func TestValidateDependencies(t *testing.T) {
	tests := []struct {
		name      string
		services  []Service
		shouldErr bool
	}{
		{
			name: "Valid dependencies",
			services: []Service{
				{Name: "service1", Command: "/bin/echo"},
				{Name: "service2", Command: "/bin/echo", DependsOn: []string{"service1"}},
			},
			shouldErr: false,
		},
		{
			name: "Non-existent dependency",
			services: []Service{
				{Name: "service1", Command: "/bin/echo", DependsOn: []string{"nonexistent"}},
			},
			shouldErr: true,
		},
		{
			name: "Circular dependency",
			services: []Service{
				{Name: "service1", Command: "/bin/echo", DependsOn: []string{"service2"}},
				{Name: "service2", Command: "/bin/echo", DependsOn: []string{"service1"}},
			},
			shouldErr: true,
		},
		{
			name: "Valid wait_after map",
			services: []Service{
				{Name: "service1", Command: "/bin/echo"},
				{
					Name:      "service2",
					Command:   "/bin/echo",
					DependsOn: []string{"service1"},
					WaitAfter: &WaitAfterField{
						PerDep:   map[string]int{"service1": 5},
						IsPerDep: true,
					},
				},
			},
			shouldErr: false,
		},
		{
			name: "Invalid wait_after map reference",
			services: []Service{
				{Name: "service1", Command: "/bin/echo"},
				{
					Name:      "service2",
					Command:   "/bin/echo",
					DependsOn: []string{"service1"},
					WaitAfter: &WaitAfterField{
						PerDep:   map[string]int{"nonexistent": 5},
						IsPerDep: true,
					},
				},
			},
			shouldErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateDependencies(tt.services)

			if tt.shouldErr && err == nil {
				t.Error("Expected error but got none")
			}

			if !tt.shouldErr && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}
		})
	}
}

// Test hasCycles
func TestHasCycles(t *testing.T) {
	tests := []struct {
		name        string
		serviceName string
		serviceMap  map[string]Service
		hasCycle    bool
	}{
		{
			name:        "No cycle",
			serviceName: "service1",
			serviceMap: map[string]Service{
				"service1": {Name: "service1", DependsOn: []string{"service2"}},
				"service2": {Name: "service2"},
			},
			hasCycle: false,
		},
		{
			name:        "Direct cycle",
			serviceName: "service1",
			serviceMap: map[string]Service{
				"service1": {Name: "service1", DependsOn: []string{"service2"}},
				"service2": {Name: "service2", DependsOn: []string{"service1"}},
			},
			hasCycle: true,
		},
		{
			name:        "Indirect cycle",
			serviceName: "service1",
			serviceMap: map[string]Service{
				"service1": {Name: "service1", DependsOn: []string{"service2"}},
				"service2": {Name: "service2", DependsOn: []string{"service3"}},
				"service3": {Name: "service3", DependsOn: []string{"service1"}},
			},
			hasCycle: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			visited := make(map[string]bool)
			recursionStack := make(map[string]bool)
			got := hasCycles(tt.serviceName, tt.serviceMap, visited, recursionStack)

			if got != tt.hasCycle {
				t.Errorf("hasCycles() = %v, want %v", got, tt.hasCycle)
			}
		})
	}
}

// Test getLongestServiceNameLength
func TestGetLongestServiceNameLength(t *testing.T) {
	tests := []struct {
		name     string
		services []Service
		expected int
	}{
		{
			name:     "Empty services",
			services: []Service{},
			expected: 0,
		},
		{
			name: "Single service",
			services: []Service{
				{Name: "service1"},
			},
			expected: 8,
		},
		{
			name: "Multiple services",
			services: []Service{
				{Name: "short"},
				{Name: "very-long-service-name"},
				{Name: "medium"},
			},
			expected: 22,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getLongestServiceNameLength(tt.services); got != tt.expected {
				t.Errorf("getLongestServiceNameLength() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// Test formatServiceName
func TestFormatServiceName(t *testing.T) {
	tests := []struct {
		name        string
		serviceName string
		maxLength   int
		expected    string
	}{
		{
			name:        "Exact length",
			serviceName: "service",
			maxLength:   7,
			expected:    "service",
		},
		{
			name:        "Padding needed",
			serviceName: "short",
			maxLength:   10,
			expected:    "short     ",
		},
		{
			name:        "No padding",
			serviceName: "exact",
			maxLength:   5,
			expected:    "exact",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatServiceName(tt.serviceName, tt.maxLength); got != tt.expected {
				t.Errorf("formatServiceName() = %q, want %q", got, tt.expected)
			}
		})
	}
}

// Test joinArgs
func TestJoinArgs(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		expected string
	}{
		{
			name:     "Empty args",
			args:     []string{},
			expected: "",
		},
		{
			name:     "Single arg",
			args:     []string{"arg1"},
			expected: "arg1",
		},
		{
			name:     "Multiple args",
			args:     []string{"arg1", "arg2", "arg3"},
			expected: "arg1 arg2 arg3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := joinArgs(tt.args); got != tt.expected {
				t.Errorf("joinArgs() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// Test ValidationError
func TestValidationError(t *testing.T) {
	tests := []struct {
		name     string
		err      ValidationError
		expected string
	}{
		{
			name: "With service name",
			err: ValidationError{
				Field:   "command",
				Service: "test-service",
				Message: "command is required",
			},
			expected: "validation error in service 'test-service', field 'command': command is required",
		},
		{
			name: "Without service name",
			err: ValidationError{
				Field:   "timeouts",
				Message: "invalid timeout value",
			},
			expected: "validation error in field 'timeouts': invalid timeout value",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.expected {
				t.Errorf("ValidationError.Error() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// Test ServiceProcess state management
func TestServiceProcessSetGetState(t *testing.T) {
	sp := &ServiceProcess{
		Name: "test-service",
	}

	// Test initial state
	sp.SetState(ServiceStatePending)
	if got := sp.GetState(); got != ServiceStatePending {
		t.Errorf("GetState() = %v, want %v", got, ServiceStatePending)
	}

	// Test state transition
	sp.SetState(ServiceStateRunning)
	if got := sp.GetState(); got != ServiceStateRunning {
		t.Errorf("GetState() = %v, want %v", got, ServiceStateRunning)
	}
}

// Test isBashAvailable
func TestIsBashAvailable(_ *testing.T) {
	// This test is environment-dependent
	// Just ensure it doesn't panic
	_ = isBashAvailable()
}

// Test validateConfig with default timeouts
func TestValidateConfigDefaults(t *testing.T) {
	config := &Config{
		Services: []Service{
			{
				Name:    "test",
				Command: "/bin/echo",
			},
		},
	}

	err := validateConfig(config)
	if err != nil {
		t.Errorf("validateConfig() error = %v, want nil", err)
	}

	// Check default timeouts
	if config.Timeouts.PostScript != 7 {
		t.Errorf("Default PostScript timeout = %v, want 7", config.Timeouts.PostScript)
	}
	if config.Timeouts.ServiceShutdown != 10 {
		t.Errorf("Default ServiceShutdown timeout = %v, want 10", config.Timeouts.ServiceShutdown)
	}
	if config.Timeouts.GlobalShutdown != 30 {
		t.Errorf("Default GlobalShutdown timeout = %v, want 30", config.Timeouts.GlobalShutdown)
	}
	if config.Timeouts.DependencyWait != 300 {
		t.Errorf("Default DependencyWait timeout = %v, want 300", config.Timeouts.DependencyWait)
	}
}

// Benchmark tests
func BenchmarkGetStateColor(b *testing.B) {
	for i := 0; i < b.N; i++ {
		getStateColor(ServiceStateRunning)
	}
}

func BenchmarkColorize(b *testing.B) {
	for i := 0; i < b.N; i++ {
		colorize(ColorGreen, "test message")
	}
}

func BenchmarkValidateService(b *testing.B) {
	service := Service{
		Name:    "test-service",
		Command: "/bin/echo",
	}
	for i := 0; i < b.N; i++ {
		validateService(service)
	}
}

// Test ServiceProcess GetPID
func TestServiceProcessGetPID(t *testing.T) {
	sp := &ServiceProcess{
		Name:    "test-service",
		Process: nil,
	}

	// Test with nil process
	if pid := sp.GetPID(); pid != 0 {
		t.Errorf("GetPID() with nil process = %v, want 0", pid)
	}
}

// Test ServiceProcess SetError
func TestServiceProcessSetError(t *testing.T) {
	sp := &ServiceProcess{
		Name: "test-service",
	}

	// Test setting error
	testErr := os.ErrNotExist

	// Note: SetError() will print an error message to stdout
	// This is expected behavior and not a test failure
	sp.SetError(testErr)

	if !errors.Is(sp.LastError, testErr) {
		t.Errorf("LastError = %v, want %v", sp.LastError, testErr)
	}

	if sp.State != ServiceStateFailed {
		t.Errorf("State = %v, want %v", sp.State, ServiceStateFailed)
	}
}

// Test that version is set
func TestVersionSet(t *testing.T) {
	if version == "" {
		t.Error("version should not be empty")
	}
}

// Test parseConfig with various TOML formats
func TestParseConfig(t *testing.T) {
	tests := []struct {
		name      string
		toml      string
		shouldErr bool
		validate  func(*testing.T, Config)
	}{
		{
			name: "Simple config",
			toml: `
[[services]]
name = "test"
command = "/bin/echo"
`,
			shouldErr: false,
			validate: func(t *testing.T, c Config) {
				if len(c.Services) != 1 {
					t.Errorf("Expected 1 service, got %d", len(c.Services))
				}
			},
		},
		{
			name: "Config with depends_on as string",
			toml: `
[[services]]
name = "svc1"
command = "/bin/echo"

[[services]]
name = "svc2"
command = "/bin/echo"
depends_on = "svc1"
`,
			shouldErr: false,
			validate: func(t *testing.T, c Config) {
				if len(c.Services[1].DependsOn) != 1 {
					t.Errorf("Expected 1 dependency, got %d", len(c.Services[1].DependsOn))
				}
			},
		},
		{
			name: "Config with depends_on as array",
			toml: `
[[services]]
name = "svc1"
command = "/bin/echo"
depends_on = ["svc2", "svc3"]
`,
			shouldErr: false,
			validate: func(t *testing.T, c Config) {
				if len(c.Services[0].DependsOn) != 2 {
					t.Errorf("Expected 2 dependencies, got %d", len(c.Services[0].DependsOn))
				}
			},
		},
		{
			name: "Config with wait_after as int",
			toml: `
[[services]]
name = "svc1"
command = "/bin/echo"
wait_after = 5
`,
			shouldErr: false,
			validate: func(t *testing.T, c Config) {
				if c.Services[0].WaitAfter == nil || c.Services[0].WaitAfter.Global != 5 {
					t.Error("Expected wait_after global = 5")
				}
			},
		},
		{
			name: "Config with wait_after as map",
			toml: `
[[services]]
name = "svc1"
command = "/bin/echo"
wait_after = { dep1 = 10, dep2 = 20 }
`,
			shouldErr: false,
			validate: func(t *testing.T, c Config) {
				if c.Services[0].WaitAfter == nil || !c.Services[0].WaitAfter.IsPerDep {
					t.Error("Expected wait_after to be per-dep")
				}
			},
		},
		{
			name: "Config with wait_after as sub-table",
			toml: `
[[services]]
name = "svc1"
command = "/bin/echo"

[services.wait_after]
dep1 = 10
dep2 = 20
`,
			shouldErr: false,
			validate: func(t *testing.T, c Config) {
				if c.Services[0].WaitAfter == nil || !c.Services[0].WaitAfter.IsPerDep {
					t.Error("Expected wait_after to be per-dep from sub-table")
				}
				if c.Services[0].WaitAfter.GetWaitTime("dep1") != 10 {
					t.Errorf("Expected wait time for dep1 = 10, got %d", c.Services[0].WaitAfter.GetWaitTime("dep1"))
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config, err := parseConfig(strings.NewReader(tt.toml))

			if tt.shouldErr && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.shouldErr && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if !tt.shouldErr && tt.validate != nil {
				tt.validate(t, config)
			}
		})
	}
}

// Test socket path constant
func TestSocketPath(t *testing.T) {
	expected := "/tmp/go-overlay.sock"
	if socketPath != expected {
		t.Errorf("socketPath = %v, want %v", socketPath, expected)
	}
}

// Mock test for IPC structures
func TestIPCStructures(t *testing.T) {
	// Test IPCCommand
	cmd := IPCCommand{
		Type:        CmdListServices,
		ServiceName: "test-service",
	}
	if cmd.Type != CmdListServices {
		t.Errorf("IPCCommand.Type = %v, want %v", cmd.Type, CmdListServices)
	}
	if cmd.ServiceName != "test-service" {
		t.Errorf("IPCCommand.ServiceName = %v, want %v", cmd.ServiceName, "test-service")
	}

	// Test ServiceInfo
	info := ServiceInfo{
		Name:      "test",
		State:     ServiceStateRunning,
		PID:       123,
		Uptime:    time.Second * 10,
		LastError: "",
		Required:  true,
	}
	if info.Name != "test" {
		t.Errorf("ServiceInfo.Name = %v, want %v", info.Name, "test")
	}

	// Test IPCResponse
	resp := IPCResponse{
		Success:  true,
		Message:  "OK",
		Services: []ServiceInfo{info},
	}
	if !resp.Success {
		t.Error("IPCResponse.Success should be true")
	}
	if resp.Message != "OK" {
		t.Errorf("IPCResponse.Message = %v, want %v", resp.Message, "OK")
	}
	if len(resp.Services) != 1 {
		t.Errorf("IPCResponse.Services length = %v, want %v", len(resp.Services), 1)
	}
}

// Test ValidationErrors
func TestValidationErrors(t *testing.T) {
	tests := []struct {
		name     string
		errors   ValidationErrors
		expected string
	}{
		{
			name:     "Empty errors",
			errors:   ValidationErrors{},
			expected: "no validation errors",
		},
		{
			name: "Single error",
			errors: ValidationErrors{
				{Field: "name", Service: "test", Message: "required"},
			},
			expected: "validation error in service 'test', field 'name': required",
		},
		{
			name: "Multiple errors",
			errors: ValidationErrors{
				{Field: "name", Message: "required"},
				{Field: "command", Service: "svc", Message: "not found"},
			},
			expected: "validation error in field 'name': required; validation error in service 'svc', field 'command': not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.errors.Error(); got != tt.expected {
				t.Errorf("ValidationErrors.Error() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// Test CommandType constants
func TestCommandTypeConstants(t *testing.T) {
	if CmdListServices != "list_services" {
		t.Errorf("CmdListServices = %v, want list_services", CmdListServices)
	}
	if CmdRestartService != "restart_service" {
		t.Errorf("CmdRestartService = %v, want restart_service", CmdRestartService)
	}
	if CmdGetStatus != "get_status" {
		t.Errorf("CmdGetStatus = %v, want get_status", CmdGetStatus)
	}
}

// =============================================================================
// Tests for new features: Health Check, Restart Policy, Environment Variables
// =============================================================================

// Test loadEnvFile
func TestLoadEnvFile(t *testing.T) {
	// Create a temp env file
	tmpDir := t.TempDir()
	envFile := tmpDir + "/test.env"

	content := `# Comment line
KEY1=value1
KEY2=value2
KEY3="quoted value"
KEY4='single quoted'

# Empty line above
DEBUG=true
`
	err := os.WriteFile(envFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create test env file: %v", err)
	}

	env, err := loadEnvFile(envFile)
	if err != nil {
		t.Errorf("loadEnvFile() error = %v", err)
	}

	tests := []struct {
		key      string
		expected string
	}{
		{"KEY1", "value1"},
		{"KEY2", "value2"},
		{"KEY3", "quoted value"},
		{"KEY4", "single quoted"},
		{"DEBUG", "true"},
	}

	for _, tt := range tests {
		if got := env[tt.key]; got != tt.expected {
			t.Errorf("env[%s] = %v, want %v", tt.key, got, tt.expected)
		}
	}
}

// Test loadEnvFile with non-existent file
func TestLoadEnvFileNotFound(t *testing.T) {
	_, err := loadEnvFile("/non/existent/file.env")
	if err == nil {
		t.Error("Expected error for non-existent file")
	}
}

// Test buildServiceEnv
func TestBuildServiceEnv(t *testing.T) {
	service := Service{
		Name:    "test-service",
		Command: "/bin/echo",
		Env: map[string]string{
			"MY_VAR":   "my_value",
			"MY_VAR_2": "my_value_2",
		},
	}

	env := buildServiceEnv(service)

	// Check that our custom vars are present
	found := false
	for _, e := range env {
		if strings.HasPrefix(e, "MY_VAR=") {
			found = true
			if e != "MY_VAR=my_value" {
				t.Errorf("MY_VAR = %v, want MY_VAR=my_value", e)
			}
			break
		}
	}
	if !found {
		t.Error("MY_VAR not found in environment")
	}
}

// Test validateHealthCheck
func TestValidateHealthCheck(t *testing.T) {
	tests := []struct {
		name      string
		service   Service
		shouldErr bool
	}{
		{
			name: "Valid HTTP health check",
			service: Service{
				Name:    "test",
				Command: "/bin/echo",
				HealthCheck: &HealthCheckConfig{
					Endpoint: "http://localhost:8080/health",
					Interval: 30,
					Retries:  3,
				},
			},
			shouldErr: false,
		},
		{
			name: "Valid HTTPS health check",
			service: Service{
				Name:    "test",
				Command: "/bin/echo",
				HealthCheck: &HealthCheckConfig{
					Endpoint: "https://localhost:8443/health",
				},
			},
			shouldErr: false,
		},
		{
			name: "Invalid endpoint - no protocol",
			service: Service{
				Name:    "test",
				Command: "/bin/echo",
				HealthCheck: &HealthCheckConfig{
					Endpoint: "localhost:8080/health",
				},
			},
			shouldErr: true,
		},
		{
			name: "Valid command health check",
			service: Service{
				Name:    "test",
				Command: "/bin/echo",
				HealthCheck: &HealthCheckConfig{
					Command: "curl -sf http://localhost:8080/health",
				},
			},
			shouldErr: false,
		},
		{
			name: "No health check",
			service: Service{
				Name:    "test",
				Command: "/bin/echo",
			},
			shouldErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := validateHealthCheck(&tt.service)
			if tt.shouldErr && len(errs) == 0 {
				t.Error("Expected errors but got none")
			}
			if !tt.shouldErr && len(errs) > 0 {
				t.Errorf("Expected no errors but got: %v", errs)
			}
		})
	}
}

// Test validateRestartPolicy
func TestValidateRestartPolicy(t *testing.T) {
	tests := []struct {
		name      string
		service   Service
		shouldErr bool
	}{
		{
			name: "Valid never policy",
			service: Service{
				Name:    "test",
				Command: "/bin/echo",
				Restart: RestartNever,
			},
			shouldErr: false,
		},
		{
			name: "Valid on-failure policy",
			service: Service{
				Name:    "test",
				Command: "/bin/echo",
				Restart: RestartOnFailure,
			},
			shouldErr: false,
		},
		{
			name: "Valid always policy",
			service: Service{
				Name:    "test",
				Command: "/bin/echo",
				Restart: RestartAlways,
			},
			shouldErr: false,
		},
		{
			name: "Invalid policy",
			service: Service{
				Name:    "test",
				Command: "/bin/echo",
				Restart: "invalid-policy",
			},
			shouldErr: true,
		},
		{
			name: "Empty policy (valid)",
			service: Service{
				Name:    "test",
				Command: "/bin/echo",
				Restart: "",
			},
			shouldErr: false,
		},
		{
			name: "Valid restart_delay and max_restarts",
			service: Service{
				Name:         "test",
				Command:      "/bin/echo",
				Restart:      RestartOnFailure,
				RestartDelay: 5,
				MaxRestarts:  3,
			},
			shouldErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := validateRestartPolicy(&tt.service)
			if tt.shouldErr && len(errs) == 0 {
				t.Error("Expected errors but got none")
			}
			if !tt.shouldErr && len(errs) > 0 {
				t.Errorf("Expected no errors but got: %v", errs)
			}
		})
	}
}

// Test validateEnvFile
func TestValidateEnvFile(t *testing.T) {
	// Create a temp file for the valid case
	tmpDir := t.TempDir()
	validEnvFile := tmpDir + "/valid.env"
	os.WriteFile(validEnvFile, []byte("KEY=value"), 0644)

	tests := []struct {
		name      string
		service   Service
		shouldErr bool
	}{
		{
			name: "Valid env_file",
			service: Service{
				Name:    "test",
				Command: "/bin/echo",
				EnvFile: validEnvFile,
			},
			shouldErr: false,
		},
		{
			name: "Non-existent env_file",
			service: Service{
				Name:    "test",
				Command: "/bin/echo",
				EnvFile: "/non/existent/file.env",
			},
			shouldErr: true,
		},
		{
			name: "No env_file",
			service: Service{
				Name:    "test",
				Command: "/bin/echo",
			},
			shouldErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := validateEnvFile(&tt.service)
			if tt.shouldErr && len(errs) == 0 {
				t.Error("Expected errors but got none")
			}
			if !tt.shouldErr && len(errs) > 0 {
				t.Errorf("Expected no errors but got: %v", errs)
			}
		})
	}
}

// Test applyHealthCheckDefaults
func TestApplyHealthCheckDefaults(t *testing.T) {
	hc := &HealthCheckConfig{}
	applyHealthCheckDefaults(hc)

	if hc.Interval != 30 {
		t.Errorf("Default Interval = %d, want 30", hc.Interval)
	}
	if hc.Retries != 3 {
		t.Errorf("Default Retries = %d, want 3", hc.Retries)
	}
	if hc.Timeout != 5 {
		t.Errorf("Default Timeout = %d, want 5", hc.Timeout)
	}
	if hc.StartDelay != 10 {
		t.Errorf("Default StartDelay = %d, want 10", hc.StartDelay)
	}

	// Test that existing values are preserved
	hc2 := &HealthCheckConfig{
		Interval:   60,
		Retries:    5,
		Timeout:    10,
		StartDelay: 20,
	}
	applyHealthCheckDefaults(hc2)

	if hc2.Interval != 60 {
		t.Errorf("Interval should be preserved: got %d, want 60", hc2.Interval)
	}
}

// Test formatMaxRestarts
func TestFormatMaxRestarts(t *testing.T) {
	tests := []struct {
		max      int
		expected string
	}{
		{0, "∞"},
		{1, "1"},
		{5, "5"},
		{100, "100"},
	}

	for _, tt := range tests {
		if got := formatMaxRestarts(tt.max); got != tt.expected {
			t.Errorf("formatMaxRestarts(%d) = %v, want %v", tt.max, got, tt.expected)
		}
	}
}

// Test RestartPolicy constants
func TestRestartPolicyConstants(t *testing.T) {
	if RestartNever != "never" {
		t.Errorf("RestartNever = %v, want never", RestartNever)
	}
	if RestartOnFailure != "on-failure" {
		t.Errorf("RestartOnFailure = %v, want on-failure", RestartOnFailure)
	}
	if RestartAlways != "always" {
		t.Errorf("RestartAlways = %v, want always", RestartAlways)
	}
}

// Test parseConfig with new fields
func TestParseConfigNewFields(t *testing.T) {
	tomlContent := `
[[services]]
name = "api"
command = "/bin/echo"
restart = "on-failure"
restart_delay = 5
max_restarts = 3

[services.health_check]
endpoint = "http://localhost:8080/health"
interval = 30
retries = 3

[services.env]
KEY1 = "value1"
KEY2 = "value2"
`
	config, err := parseConfig(strings.NewReader(tomlContent))
	if err != nil {
		t.Fatalf("parseConfig() error = %v", err)
	}

	if len(config.Services) != 1 {
		t.Fatalf("Expected 1 service, got %d", len(config.Services))
	}

	svc := config.Services[0]

	// Check restart policy
	if svc.Restart != RestartOnFailure {
		t.Errorf("Restart = %v, want %v", svc.Restart, RestartOnFailure)
	}
	if svc.RestartDelay != 5 {
		t.Errorf("RestartDelay = %v, want 5", svc.RestartDelay)
	}
	if svc.MaxRestarts != 3 {
		t.Errorf("MaxRestarts = %v, want 3", svc.MaxRestarts)
	}

	// Check health check
	if svc.HealthCheck == nil {
		t.Fatal("HealthCheck should not be nil")
	}
	if svc.HealthCheck.Endpoint != "http://localhost:8080/health" {
		t.Errorf("HealthCheck.Endpoint = %v, want http://localhost:8080/health", svc.HealthCheck.Endpoint)
	}
	if svc.HealthCheck.Interval != 30 {
		t.Errorf("HealthCheck.Interval = %v, want 30", svc.HealthCheck.Interval)
	}

	// Check env vars
	if len(svc.Env) != 2 {
		t.Errorf("Env length = %v, want 2", len(svc.Env))
	}
	if svc.Env["KEY1"] != "value1" {
		t.Errorf("Env[KEY1] = %v, want value1", svc.Env["KEY1"])
	}
}

// Test checkCommandHealth
func TestCheckCommandHealth(t *testing.T) {
	// Test successful command
	if !checkCommandHealth("exit 0", 5) {
		t.Error("checkCommandHealth('exit 0') should return true")
	}

	// Test failing command
	if checkCommandHealth("exit 1", 5) {
		t.Error("checkCommandHealth('exit 1') should return false")
	}
}

// Benchmark for buildServiceEnv
func BenchmarkBuildServiceEnv(b *testing.B) {
	service := Service{
		Name:    "test-service",
		Command: "/bin/echo",
		Env: map[string]string{
			"VAR1": "value1",
			"VAR2": "value2",
			"VAR3": "value3",
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buildServiceEnv(service)
	}
}

// Benchmark for validateHealthCheck
func BenchmarkValidateHealthCheck(b *testing.B) {
	service := Service{
		Name:    "test",
		Command: "/bin/echo",
		HealthCheck: &HealthCheckConfig{
			Endpoint: "http://localhost:8080/health",
			Interval: 30,
			Retries:  3,
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		validateHealthCheck(&service)
	}
}

