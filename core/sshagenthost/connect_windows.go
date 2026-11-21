package sshagenthost

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

const pipePrefix = `\\.\pipe\MoonPassword-Agent-Control-`

// Connect attaches to the existing host, or starts a private executable copy so
// the updater can replace the installed files without stopping the running host.
func Connect(executable, dataDir string, secret []byte) (*Client, error) {
	var session uint32
	if err := windows.ProcessIdToSessionId(uint32(os.Getpid()), &session); err != nil {
		return nil, err
	}
	hash := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", session, strings.ToLower(filepath.Clean(dataDir)))))
	name := fmt.Sprintf("%s%x", pipePrefix, hash[:16])
	c, absent, err := dial(name, secret)
	// The old process can have closed its handle just before the server
	// finishes consuming the disconnect.
	deadline := time.Now().Add(callTimeout)
	for errors.Is(err, errOwnerBusy) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		c, absent, err = dial(name, secret)
	}
	if err == nil || !absent {
		return c, err
	}

	runtimeDir := filepath.Join(dataDir, "ssh-agent-runtime")
	if err := os.MkdirAll(runtimeDir, 0700); err != nil {
		return nil, err
	}
	// Delete only old copies which Windows no longer has open.
	files, _ := os.ReadDir(runtimeDir)
	for _, f := range files {
		if strings.HasPrefix(f.Name(), "host-") && filepath.Ext(f.Name()) == ".exe" {
			_ = os.Remove(filepath.Join(runtimeDir, f.Name()))
		}
	}
	copyPath, err := copyExecutable(executable, runtimeDir)
	if err != nil {
		return nil, err
	}

	cmd := exec.Command(copyPath, name)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW, HideWindow: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		_ = os.Remove(copyPath)
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		stdin.Close()
		_ = os.Remove(copyPath)
		return nil, err
	}
	_, sendErr := stdin.Write(secret)
	_ = stdin.Close()
	if sendErr != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = os.Remove(copyPath)
		return nil, sendErr
	}
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		_ = os.Remove(copyPath)
		close(exited)
	}()
	deadline = time.Now().Add(callTimeout)
	for time.Now().Before(deadline) {
		if c, absent, err = dial(name, secret); err == nil || !absent {
			return c, err
		}
		select {
		case <-exited:
			return nil, errors.New("the SSH Agent process could not start")
		case <-time.After(50 * time.Millisecond):
		}
	}
	return nil, errors.New("the SSH Agent process did not become ready")
}

func dial(name string, secret []byte) (*Client, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := winio.DialPipeContext(ctx, name)
	if err != nil {
		return nil, errors.Is(err, windows.ERROR_FILE_NOT_FOUND), err
	}
	c, err := Attach(conn, secret)
	return c, false, err
}

func ListenControl(name string) (net.Listener, error) {
	if !strings.HasPrefix(name, pipePrefix) || len(name) != len(pipePrefix)+32 {
		return nil, errors.New("invalid SSH Agent control pipe")
	}
	return winio.ListenPipe(name, &winio.PipeConfig{SecurityDescriptor: "D:P(A;;GA;;;OW)", InputBufferSize: 64 << 10, OutputBufferSize: 64 << 10})
}

func copyExecutable(path, dir string) (string, error) {
	src, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer src.Close()
	dst, err := os.CreateTemp(dir, "host-*.exe")
	if err != nil {
		return "", err
	}
	_, copyErr := io.Copy(dst, src)
	closeErr := dst.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		_ = os.Remove(dst.Name())
		return "", err
	}
	return dst.Name(), nil
}
