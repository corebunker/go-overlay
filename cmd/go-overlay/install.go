package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func autoInstallInPath() {
	execPath, err := os.Executable()
	if err != nil {
		_info("Warning: Could not determine executable path:", err)
		return
	}

	pathDirs := []string{"/usr/local/bin", "/usr/bin", "/bin"}
	execDir := filepath.Dir(execPath)

	for _, pathDir := range pathDirs {
		if execDir == pathDir {
			_info("Already installed in PATH:", execDir)
			return
		}
	}

	targetPath := "/usr/local/bin/go-overlay"

	if linkTarget, err := os.Readlink(targetPath); err == nil {
		if linkTarget == execPath {
			return
		}
		_ = os.Remove(targetPath)
	}

	if err := os.Symlink(execPath, targetPath); err != nil {
		_warn(fmt.Sprintf("Could not create symlink in PATH: %v", err))
		_warn(fmt.Sprintf("You can manually run: sudo ln -sf %s %s", execPath, targetPath))
		return
	}

	_success("Auto-installed in PATH as 'go-overlay'")
	_info("You can now use: go-overlay list, go-overlay restart <service>, etc.")
}
