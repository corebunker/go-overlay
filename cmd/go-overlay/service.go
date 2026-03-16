package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
)

type ServiceState int

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

type ServiceProcess struct {
	Name         string
	LastError    error
	StartTime    time.Time
	Config       Service
	Process      *exec.Cmd
	PTY          *os.File
	Cancel       context.CancelFunc
	StateMu      sync.RWMutex
	State        ServiceState
	HealthyAt    time.Time
	FailureCount int
	HealthCancel context.CancelFunc
	RestartCount int
	LastRestart  time.Time
}

func (sp *ServiceProcess) SetState(state ServiceState) {
	sp.StateMu.Lock()
	defer sp.StateMu.Unlock()
	oldState := sp.State
	sp.State = state
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

	if err := os.Chmod(s.PreScript, 0o700); err != nil { // #nosec G302
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

	if err := os.Chmod(s.PosScript, 0o700); err != nil { // #nosec G302
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
		select {
		case <-shutdownCtx.Done():
			return false
		default:
		}

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

			select {
			case <-time.After(time.Duration(waitAfter) * time.Second):
				return true
			case <-shutdownCtx.Done():
				return false
			}
		}

		_info(fmt.Sprintf("Waiting for dependency: %s", colorize(ColorYellow, depName)))

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
	if len(service.Args) > 0 {
		cmd = exec.Command(service.Command, service.Args...)
	} else {
		cmd = exec.Command(service.Command)
	}

	if service.User != "" {
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

	serviceCtx, serviceCancel := context.WithCancel(shutdownCtx)

	serviceProcess := &ServiceProcess{
		Name:    service.Name,
		Process: cmd,
		PTY:     ptmx,
		Cancel:  serviceCancel,
		State:   ServiceStatePending,
		Config:  service,
	}
	addActiveService(service.Name, serviceProcess)
	serviceProcess.SetState(ServiceStateRunning)
	startHealthMonitor(serviceProcess)

	go prefixLogs(ptmx, service.Name, maxLength)

	go func() {
		<-serviceCtx.Done()
		serviceProcess.SetState(ServiceStateStopping)
		_info(fmt.Sprintf("Gracefully stopping service: %s", colorize(ColorCyan, service.Name)))

		if cmd.Process != nil {
			if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
				_error(fmt.Sprintf("Error sending SIGTERM to service '%s': %v",
					colorize(ColorCyan, service.Name), err))
				serviceProcess.SetError(err)
			}

			done := make(chan error, 1)
			go func() {
				done <- cmd.Wait()
			}()

			shutdownTimeout := time.Duration(timeouts.ServiceShutdown) * time.Second
			select {
			case <-time.After(shutdownTimeout):
				_warn(fmt.Sprintf("Force killing service '%s' after %s timeout",
					colorize(ColorCyan, service.Name), shutdownTimeout))
				if err := cmd.Process.Kill(); err != nil {
					_error(fmt.Sprintf("Error force killing service '%s': %v",
						colorize(ColorCyan, service.Name), err))
					serviceProcess.SetError(err)
				}
				<-done
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

		if serviceProcess.HealthCancel != nil {
			serviceProcess.HealthCancel()
		}
		if ptmx != nil {
			_ = ptmx.Close()
		}
		removeActiveService(service.Name)
	}()

	select {
	case <-serviceCtx.Done():
		return nil
	default:
		err := cmd.Wait()
		if serviceProcess.HealthCancel != nil {
			serviceProcess.HealthCancel()
		}
		serviceCancel()
		if err != nil {
			serviceProcess.SetError(err)
		}
		removeActiveService(service.Name)
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
