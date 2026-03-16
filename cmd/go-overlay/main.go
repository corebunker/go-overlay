package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

var (
	debugMode bool
	version   = "v0.1.3"
)

var (
	shutdownCtx    context.Context
	shutdownCancel context.CancelFunc
	activeServices = make(map[string]*ServiceProcess)
	servicesMutex  sync.RWMutex
	shutdownWg     sync.WaitGroup
	ipcServer      net.Listener
	globalConfig   *Config
)

func main() {
	fmt.Printf("Go Overlay - Version: %s\n", version)

	rootCmd := &cobra.Command{
		Use:   "go-overlay",
		Short: "Go-based service supervisor like s6-overlay",
		RunE: func(_ *cobra.Command, _ []string) error {
			if debugMode {
				_printEnvVariables()
			}
			autoInstallInPath()
			shutdownCtx, shutdownCancel = context.WithCancel(context.Background())
			setupSignalHandler()
			if err := startIPCServer(); err != nil {
				_info("Warning: Could not start IPC server:", err)
			}
			return loadServices("/services.toml")
		},
	}

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List all services and their status",
		RunE: func(_ *cobra.Command, _ []string) error {
			return listServices()
		},
	}

	restartCmd := &cobra.Command{
		Use:   "restart [service-name]",
		Short: "Restart a specific service",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return restartService(args[0])
		},
	}

	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show overall system status",
		RunE: func(_ *cobra.Command, _ []string) error {
			return showStatus()
		},
	}

	installCmd := &cobra.Command{
		Use:   "install",
		Short: "Install go-overlay in system PATH",
		RunE: func(_ *cobra.Command, _ []string) error {
			autoInstallInPath()
			return nil
		},
	}

	rootCmd.Flags().BoolVar(&debugMode, "debug", false, "Enable debug mode")
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

	if len(activeServices) > 0 {
		printServiceStatuses()
	}

	if shutdownCancel != nil {
		shutdownCancel()
	}

	if ipcServer != nil {
		_ = ipcServer.Close()
	}

	_ = removeFile(socketPath)

	if len(activeServices) == 0 {
		_info("No active services to shutdown")
		return
	}

	globalTimeout := 30 * time.Second
	servicesMutex.RLock()
	if len(activeServices) > 0 {
		globalTimeout = 30 * time.Second
	}
	servicesMutex.RUnlock()

	shutdownTimer := time.NewTimer(globalTimeout)
	defer shutdownTimer.Stop()

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

func removeFile(path string) error {
	return os.Remove(path)
}
