package main

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"time"
)

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

func startHealthMonitor(serviceProc *ServiceProcess) {
	if serviceProc.Config.HealthCheck == nil {
		return
	}

	config := *serviceProc.Config.HealthCheck
	applyHealthCheckDefaults(&config)

	healthCtx, healthCancel := context.WithCancel(shutdownCtx)
	serviceProc.HealthCancel = healthCancel

	go func() {
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

func performHealthCheck(serviceProc *ServiceProcess, config HealthCheckConfig) bool {
	if config.Endpoint != "" {
		return checkHTTPHealth(config.Endpoint, config.Timeout)
	}
	if config.Command != "" {
		return checkCommandHealth(config.Command, config.Timeout)
	}
	return true
}

func checkHTTPHealth(endpoint string, timeout int) bool {
	client := &http.Client{
		Timeout: time.Duration(timeout) * time.Second,
	}
	resp, err := client.Get(endpoint) // #nosec G107
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 400
}

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

func handleUnhealthyService(serviceProc *ServiceProcess) {
	_error(fmt.Sprintf("Service '%s' is unhealthy after %d consecutive failures",
		serviceProc.Name, serviceProc.FailureCount))

	if serviceProc.Config.Restart == RestartAlways || serviceProc.Config.Restart == RestartOnFailure {
		_info(fmt.Sprintf("Triggering restart for unhealthy service '%s'", serviceProc.Name))
		if serviceProc.Cancel != nil {
			serviceProc.Cancel()
		}
	} else if serviceProc.Config.Required {
		_error(fmt.Sprintf("[CRITICAL] Required service '%s' is unhealthy, initiating shutdown",
			serviceProc.Name))
		gracefulShutdown()
	}
}
