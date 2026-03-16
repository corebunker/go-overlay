package main

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"
)

const socketPath = "/tmp/go-overlay.sock"

type CommandType string

const (
	CmdListServices   CommandType = "list_services"
	CmdRestartService CommandType = "restart_service"
	CmdGetStatus      CommandType = "get_status"
)

type IPCCommand struct {
	Type        CommandType `json:"type"`
	ServiceName string      `json:"service_name,omitempty"`
}

type ServiceInfo struct {
	Name      string        `json:"name"`
	LastError string        `json:"last_error,omitempty"`
	Uptime    time.Duration `json:"uptime"`
	State     ServiceState  `json:"state"`
	PID       int           `json:"pid"`
	Required  bool          `json:"required"`
}

type IPCResponse struct {
	Message  string        `json:"message,omitempty"`
	Services []ServiceInfo `json:"services,omitempty"`
	Success  bool          `json:"success"`
}

func startIPCServer() error {
	_ = removeSocketFile()

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
					return
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

	serviceProc.SetState(ServiceStateStopping)
	if serviceProc.Cancel != nil {
		serviceProc.Cancel()
	}

	time.Sleep(2 * time.Second)

	if serviceProc.Process != nil && serviceProc.Process.Process != nil {
		if err := serviceProc.Process.Process.Kill(); err != nil {
			_info("Error killing service during restart:", err)
		}
	}

	if serviceProc.PTY != nil {
		_ = serviceProc.PTY.Close()
	}
	delete(activeServices, serviceName)

	go func() {
		time.Sleep(1 * time.Second)
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

func removeSocketFile() error {
	return removeFile(socketPath)
}
