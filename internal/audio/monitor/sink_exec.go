package monitor

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

type defaultSinkFactory struct{}

func (defaultSinkFactory) Open(outputDevice string) (Sink, error) {
	normalized := normalizeSinkOutputDevice(outputDevice)
	if normalized != "" {
		if sink, err := openAplaySink(normalized); err == nil {
			return sink, nil
		}
	}
	if sink, err := openFFplaySink(""); err == nil {
		return sink, nil
	}
	if sink, err := openAplaySink(normalized); err == nil {
		return sink, nil
	}
	return nil, errors.New("no supported monitor output backend found (ffplay/aplay)")
}

func openFFplaySink(outputDevice string) (Sink, error) {
	path, err := exec.LookPath("ffplay")
	if err != nil {
		return nil, err
	}
	args := make([]string, 0, 13)
	args = append(args,
		"-hide_banner",
		"-loglevel", "error",
		"-nodisp",
		"-autoexit",
		"-f", "s16le",
		"-ar", "8000",
		"-ac", "1",
	)
	args = append(args, "-i", "pipe:0")
	return newExecSink(path, args...)
}

func openAplaySink(outputDevice string) (Sink, error) {
	path, err := exec.LookPath("aplay")
	if err != nil {
		return nil, err
	}
	args := make([]string, 0, 10)
	args = append(args,
		"-q",
		"-f", "S16_LE",
		"-r", "8000",
		"-c", "1",
	)
	if outputDevice = normalizeSinkOutputDevice(outputDevice); outputDevice != "" {
		args = append(args, "-D", outputDevice)
	}
	return newExecSink(path, args...)
}

func normalizeSinkOutputDevice(outputDevice string) string {
	outputDevice = strings.TrimSpace(outputDevice)
	if outputDevice == "" || strings.EqualFold(outputDevice, "system-default") {
		return ""
	}
	return outputDevice
}

// ListOutputDevices returns selectable monitor output-device options.
func ListOutputDevices() []string {
	out := []string{"system-default"}
	seen := map[string]struct{}{
		"system-default": {},
	}
	for _, device := range listAplayDevices() {
		device = strings.TrimSpace(device)
		if device == "" {
			continue
		}
		if _, ok := seen[device]; ok {
			continue
		}
		seen[device] = struct{}{}
		out = append(out, device)
	}
	sort.Strings(out[1:])
	return out
}

func listAplayDevices() []string {
	path, err := exec.LookPath("aplay")
	if err != nil {
		return nil
	}
	cmd := exec.Command(path, "-L")
	raw, err := cmd.Output()
	if err != nil {
		return nil
	}
	return parseAplayDeviceList(string(raw))
}

func parseAplayDeviceList(raw string) []string {
	lines := strings.Split(raw, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		// ALSA device IDs are top-level lines; descriptions are indented.
		if line[0] == ' ' || line[0] == '\t' {
			continue
		}
		device := strings.Fields(line)
		if len(device) == 0 {
			continue
		}
		out = append(out, device[0])
	}
	return out
}

type execSink struct {
	stderr    bytes.Buffer
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	waitCh    chan error
	closeOnce sync.Once
}

func newExecSink(path string, args ...string) (*execSink, error) {
	sink := &execSink{
		waitCh: make(chan error, 1),
	}
	cmd := exec.Command(path, args...)
	cmd.Stderr = &sink.stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, err
	}
	sink.cmd = cmd
	sink.stdin = stdin
	go func() {
		sink.waitCh <- cmd.Wait()
	}()
	select {
	case err := <-sink.waitCh:
		_ = stdin.Close()
		return nil, formatProcessExitError(err, sink.stderr.String())
	case <-time.After(150 * time.Millisecond):
	}
	return sink, nil
}

func (s *execSink) WritePCM(samples []int16) error {
	if len(samples) == 0 {
		return nil
	}
	payload := make([]byte, len(samples)*2)
	for i, sample := range samples {
		binary.LittleEndian.PutUint16(payload[i*2:], uint16(sample))
	}
	_, err := s.stdin.Write(payload)
	if err != nil {
		return fmt.Errorf("write monitor pcm: %w", err)
	}
	return nil
}

func (s *execSink) Close() error {
	var outErr error
	s.closeOnce.Do(func() {
		_ = s.stdin.Close()
		select {
		case err := <-s.waitCh:
			if err != nil && !isProcessExitError(err) {
				outErr = err
			}
		case <-time.After(500 * time.Millisecond):
			if s.cmd.Process != nil {
				_ = s.cmd.Process.Kill()
			}
			if err := <-s.waitCh; err != nil && !isProcessExitError(err) {
				outErr = err
			}
		}
	})
	return outErr
}

func isProcessExitError(err error) bool {
	if err == nil {
		return false
	}
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr)
}

func formatProcessExitError(err error, stderr string) error {
	msg := strings.TrimSpace(stderr)
	if msg != "" {
		return fmt.Errorf("%w: %s", err, msg)
	}
	return err
}
