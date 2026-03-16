package main

import (
	"fmt"
	"time"
)

func handleServiceExit(serviceProc *ServiceProcess, exitErr error) {
	policy := serviceProc.Config.Restart

	shouldRestart := false
	switch policy {
	case RestartAlways:
		shouldRestart = true
	case RestartOnFailure:
		shouldRestart = exitErr != nil
	case RestartNever, "":
		shouldRestart = false
	}

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
			delay = 1
		}

		_info(fmt.Sprintf("Restarting service '%s' in %ds (attempt %d/%s)",
			serviceProc.Name, delay, serviceProc.RestartCount,
			formatMaxRestarts(serviceProc.Config.MaxRestarts)))

		time.AfterFunc(time.Duration(delay)*time.Second, func() {
			restartServiceInternal(serviceProc)
		})
	} else if serviceProc.Config.Required && exitErr != nil {
		_error(fmt.Sprintf("[CRITICAL] Required service '%s' exited with error, initiating shutdown",
			serviceProc.Name))
		gracefulShutdown()
	}
}

func formatMaxRestarts(max int) string {
	if max == 0 {
		return "∞"
	}
	return fmt.Sprintf("%d", max)
}

func restartServiceInternal(serviceProc *ServiceProcess) {
	if globalConfig == nil {
		_error(fmt.Sprintf("Cannot restart service '%s': no global config", serviceProc.Name))
		return
	}

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
