package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

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

func validateConfig(config *Config) error {
	var errors ValidationErrors

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
		config.Timeouts.DependencyWait = 300
	}

	serviceNames := make(map[string]bool)
	for i := range config.Services {
		service := &config.Services[i]
		if serviceNames[service.Name] {
			errors = append(errors, ValidationError{
				Field:   "name",
				Service: service.Name,
				Message: "duplicate service name",
			})
		}
		serviceNames[service.Name] = true

		if service.Enabled == nil {
			config.Services[i].Enabled = new(bool)
			*config.Services[i].Enabled = true
		}
	}

	applyServiceEnvOverrides(config)

	for i := range config.Services {
		service := &config.Services[i]
		if errs := validateService(*service); len(errs) > 0 {
			errors = append(errors, errs...)
		}
	}

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

	if !isServiceEnabled(service) {
		return errors
	}

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

		if hc.Endpoint != "" {
			if !strings.HasPrefix(hc.Endpoint, "http://") && !strings.HasPrefix(hc.Endpoint, "https://") {
				errors = append(errors, ValidationError{
					Field:   "health_check.endpoint",
					Service: service.Name,
					Message: "endpoint must start with http:// or https://",
				})
			}
		}

		if hc.Interval < 0 {
			errors = append(errors, ValidationError{
				Field:   "health_check.interval",
				Service: service.Name,
				Message: "interval must be a positive number",
			})
		}

		if hc.Retries < 0 {
			errors = append(errors, ValidationError{
				Field:   "health_check.retries",
				Service: service.Name,
				Message: "retries must be a positive number",
			})
		}

		if hc.Timeout < 0 {
			errors = append(errors, ValidationError{
				Field:   "health_check.timeout",
				Service: service.Name,
				Message: "timeout must be a positive number",
			})
		}

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

	if service.RestartDelay < 0 {
		errors = append(errors, ValidationError{
			Field:   "restart_delay",
			Service: service.Name,
			Message: "restart_delay must be a positive number",
		})
	}

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
	enabledMap := make(map[string]bool)
	for i := range services {
		service := &services[i]
		serviceMap[service.Name] = *service
		enabledMap[service.Name] = isServiceEnabled(*service)
	}

	for i := range services {
		service := &services[i]
		if !isServiceEnabled(*service) {
			continue
		}

		for _, dep := range service.DependsOn {
			if _, exists := serviceMap[dep]; !exists {
				return fmt.Errorf("service '%s' depends on non-existent service '%s'", service.Name, dep)
			}
			if !enabledMap[dep] {
				return fmt.Errorf("service '%s' depends on disabled service '%s'", service.Name, dep)
			}
		}

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

	enabledServiceMap := make(map[string]Service)
	for i := range services {
		service := &services[i]
		if isServiceEnabled(*service) {
			enabledServiceMap[service.Name] = *service
		}
	}

	for name := range enabledServiceMap {
		if hasCycles(name, enabledServiceMap, make(map[string]bool), make(map[string]bool)) {
			return fmt.Errorf("circular dependency detected involving service '%s'", name)
		}
	}

	return nil
}

func isServiceEnabled(service Service) bool {
	if service.Enabled == nil {
		return true
	}
	return *service.Enabled
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
