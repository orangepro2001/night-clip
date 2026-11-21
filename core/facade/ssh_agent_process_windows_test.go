package facade

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/sys/windows"

	"moonpassword/core/pipe"
)

func TestWindowsAgentRemainsAvailableAcrossUpdate(t *testing.T) {
	exe, err := filepath.Abs(filepath.Join("..", "dist", "windows-amd64", "moonpassword-ssh-agent.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(exe); errors.Is(err, os.ErrNotExist) {
		t.Skip("build the Windows SSH Agent helper first")
	}
	dir := t.TempDir()
	start := func() *node {
		c := New()
		cfg := configFor(t, dir, func(m map[string]any) {
			m["platform"] = "windows"
			m["ssh_agent_host_path"] = exe
		})
		if err := c.Init(cfg); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(c.Shutdown)
		return &node{t: t, c: c, name: "Windows"}
	}
	old := start()
	name := testAgentPipe(t)
	old.call("settings.set", map[string]any{"key": "ssh_agent_pipe", "value": name})
	old.call("settings.set", map[string]any{"key": "ssh_agent", "value": "on"})
	if st := old.call("ssh.status", nil); st["state"] != "listening" {
		t.Fatalf("agent: %v", st)
	}
	vcreate(t, old)
	old.call("ssh.generate", map[string]any{"type": "ed25519", "title": "Update Fixture"})
	waitFor(t, "host has the key", func() bool { return old.call("ssh.status", nil)["keys_loaded"] == float64(1) })

	a := agentClient(t, name)
	keys, err := a.List()
	if err != nil || len(keys) != 1 {
		t.Fatalf("before update: %v %v", keys, err)
	}
	pub, err := ssh.ParsePublicKey(keys[0].Blob)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("MoonPassword synthetic update fixture")
	checkSign := func(client agent.ExtendedAgent) error {
		sig, err := client.Sign(pub, data)
		if err != nil {
			return err
		}
		return pub.Verify(data, sig)
	}
	if err := checkSign(a); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	signErr := make(chan error, 1)
	wg.Go(func() {
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				if err := checkSign(a); err != nil {
					signErr <- err
					return
				}
			}
		}
	})
	old.call("ssh.prepare_update", map[string]any{"keep": true})
	old.c.Shutdown()
	time.Sleep(300 * time.Millisecond) // both UI instances are absent
	fresh := start()
	close(stop)
	wg.Wait()
	select {
	case err := <-signErr:
		t.Fatalf("agent lost a signing request during update: %v", err)
	default:
	}
	if st := fresh.call("ssh.status", nil); st["state"] != "listening" || st["vault_unlocked"] != false ||
		st["keys_loaded"] != float64(1) || st["keys_state"] != "grace" {
		t.Fatalf("restarted UI: %v", st)
	}
	if err := checkSign(agentClient(t, name)); err != nil {
		t.Fatal(err)
	}

	if lock := fresh.call("vault.lock", nil); lock["ssh_keys_cleared"] != float64(1) {
		t.Fatalf("manual lock of restarted UI: %v", lock)
	}
	if keys, err := a.List(); err != nil || len(keys) != 0 {
		t.Fatalf("manual lock: %v %v", keys, err)
	}
	fresh.call("vault.unlock", map[string]any{"password": vpw})
	waitFor(t, "host has the key after unlock", func() bool { return fresh.call("ssh.status", nil)["keys_loaded"] == float64(1) })

	// Post the actual Windows messages to the test helper's hidden window only.
	// This exercises its independent listener without locking or logging off the workstation.
	hwnd := agentSessionWindow(t, name)
	fresh.call("ssh.prepare_update", map[string]any{"keep": true})
	fresh.c.Shutdown()
	sendSessionMessage(t, hwnd, 7) // WTS_SESSION_LOCK
	if keys, err := a.List(); err != nil || len(keys) != 0 {
		t.Fatalf("session lock: %v %v", keys, err)
	}
	if err := checkSign(a); err == nil {
		t.Fatal("host signed after session lock")
	}

	sendSessionMessage(t, hwnd, 8) // WTS_SESSION_UNLOCK
	reopened := start()
	reopened.call("vault.unlock", map[string]any{"password": vpw})
	waitFor(t, "host has the key before logoff", func() bool { return reopened.call("ssh.status", nil)["keys_loaded"] == float64(1) })
	reopened.call("ssh.prepare_update", map[string]any{"keep": true})
	reopened.c.Shutdown()
	sendSessionMessage(t, hwnd, 6) // WTS_SESSION_LOGOFF
	if err := checkSign(a); err == nil {
		t.Fatal("host signed after logoff")
	}
	waitFor(t, "logoff closes the helper pipe", func() bool {
		conn, err := pipe.Dial(name)
		if err == nil {
			conn.Close()
		}
		return err != nil
	})

	// A cancelled update must close the pipe and drop the keys on ordinary quit.
	last := start()
	last.call("vault.unlock", map[string]any{"password": vpw})
	waitFor(t, "reloaded key before ordinary exit", func() bool { return last.call("ssh.status", nil)["keys_loaded"] == float64(1) })
	beforeQuit := agentClient(t, name)
	last.call("ssh.prepare_update", map[string]any{"keep": true})
	last.call("ssh.prepare_update", map[string]any{"keep": false})
	last.c.Shutdown()
	waitFor(t, "ordinary exit disconnects the agent", func() bool { return checkSign(beforeQuit) != nil })
}

func agentSessionWindow(t *testing.T, name string) uintptr {
	t.Helper()
	conn, err := os.OpenFile(name, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var pid uint32
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	r, _, err := kernel32.NewProc("GetNamedPipeServerProcessId").Call(conn.Fd(), uintptr(unsafe.Pointer(&pid)))
	runtime.KeepAlive(conn)
	if r == 0 || pid == uint32(os.Getpid()) {
		t.Fatalf("agent is not independent: %v", err)
	}
	user32 := windows.NewLazySystemDLL("user32.dll")
	windowPID := user32.NewProc("GetWindowThreadProcessId")
	var found uintptr
	cb := windows.NewCallback(func(hwnd, _ uintptr) uintptr {
		var owner uint32
		windowPID.Call(hwnd, uintptr(unsafe.Pointer(&owner)))
		if owner != pid {
			return 1
		}
		found = hwnd
		return 0
	})
	user32.NewProc("EnumWindows").Call(cb, 0)
	if found == 0 {
		t.Fatal("the independent Agent has no session window")
	}
	return found
}

func sendSessionMessage(t *testing.T, hwnd, event uintptr) {
	t.Helper()
	post := windows.NewLazySystemDLL("user32.dll").NewProc("PostMessageW")
	if r, _, err := post.Call(hwnd, 0x2b1, event, 0); r == 0 {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
}
