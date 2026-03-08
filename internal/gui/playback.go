//go:build !headless

package gui

import (
	"errors"
	"os/exec"
	"runtime"
	"strings"
)

func startPlayback(path string) (*exec.Cmd, bool, error) {
	if strings.TrimSpace(path) == "" {
		return nil, false, errors.New("recording path is empty")
	}
	if ffplay, err := exec.LookPath("ffplay"); err == nil {
		cmd := exec.Command(ffplay, "-nodisp", "-autoexit", path)
		if err := cmd.Start(); err != nil {
			return nil, false, err
		}
		go func() { _ = cmd.Wait() }()
		return cmd, true, nil
	}

	if err := openFile(path); err != nil {
		return nil, false, err
	}
	return nil, false, nil
}

func openFile(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
