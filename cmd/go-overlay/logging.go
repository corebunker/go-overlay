package main

import "fmt"

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

	ColorBoldRed     = "\033[1;31m"
	ColorBoldGreen   = "\033[1;32m"
	ColorBoldYellow  = "\033[1;33m"
	ColorBoldBlue    = "\033[1;34m"
	ColorBoldMagenta = "\033[1;35m"
	ColorBoldCyan    = "\033[1;36m"
	ColorBoldWhite   = "\033[1;37m"
)

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
