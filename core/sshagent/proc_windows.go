//go:build windows

package sshagent

import (
	"strings"
	"syscall"
	"unsafe"
)

var (
	kernel32                      = syscall.NewLazyDLL("kernel32.dll")
	procQueryFullProcessImageName = kernel32.NewProc("QueryFullProcessImageNameW")
)

const (
	th32csSnapProcess              = 0x00000002
	processQueryLimitedInformation = 0x1000
	maxChain                       = 5
)

// chainStop are shell and system processes where the interesting part of
// the chain ends; they are not reported.
var chainStop = map[string]bool{
	"explorer.exe": true, "services.exe": true, "svchost.exe": true, "wininit.exe": true,
	"winlogon.exe": true, "userinit.exe": true, "sihost.exe": true, "smss.exe": true,
}

// ClientChain returns the process with the given id and up to four of its
// parents, e.g. ssh.exe <- git.exe <- pwsh.exe. A parent created after its
// child is a reused process id and ends the chain.
func ClientChain(pid uint32) []Proc {
	if pid == 0 {
		return []Proc{}
	}
	snap, err := syscall.CreateToolhelp32Snapshot(th32csSnapProcess, 0)
	if err != nil {
		return []Proc{{PID: pid}}
	}
	defer syscall.CloseHandle(snap)
	type info struct {
		parent uint32
		name   string
	}
	procs := map[uint32]info{}
	var e syscall.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = syscall.Process32First(snap, &e); err == nil; err = syscall.Process32Next(snap, &e) {
		procs[e.ProcessID] = info{parent: e.ParentProcessID, name: syscall.UTF16ToString(e.ExeFile[:])}
	}

	out := []Proc{}
	cur := pid
	var childCreated int64
	for range maxChain {
		p, ok := procs[cur]
		if !ok || chainStop[strings.ToLower(p.name)] {
			break
		}
		created, path := processDetails(cur)
		if childCreated != 0 && created != 0 && created > childCreated {
			break
		}
		out = append(out, Proc{PID: cur, Name: p.name, Path: path})
		if p.parent == 0 || p.parent == cur {
			break
		}
		childCreated = created
		cur = p.parent
	}
	return out
}

// processDetails returns the creation time (100 ns ticks) and image path,
// or zero values when the process cannot be opened.
func processDetails(pid uint32) (int64, string) {
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, pid)
	if err != nil {
		return 0, ""
	}
	defer syscall.CloseHandle(h)
	var created, exited, kernel, user syscall.Filetime
	var t int64
	if syscall.GetProcessTimes(h, &created, &exited, &kernel, &user) == nil {
		t = created.Nanoseconds() / 100
	}
	buf := make([]uint16, 1024)
	n := uint32(len(buf))
	if r, _, _ := procQueryFullProcessImageName.Call(uintptr(h), 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n))); r != 0 {
		return t, syscall.UTF16ToString(buf[:n])
	}
	return t, ""
}
