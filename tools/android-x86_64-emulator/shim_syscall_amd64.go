//go:build linux && amd64

package libc

import "golang.org/x/sys/unix"

// Emulator-only shim (see README.md). Android's seccomp policy for apps only allows the
// syscalls bionic itself uses, and on x86_64 that excludes the legacy path-based calls
// (open, stat, lstat, mkdir, unlink, rename, ...): modernc.org/libc issues them directly and the
// process is killed with SIGSYS. arm64 has no such calls, so real phones are unaffected.
// Each legacy call is rewritten to its *at() equivalent, which bionic does use.

const atFDCWD = ^uintptr(99) // AT_FDCWD (-100)

func shimSyscall(n, a1, a2, a3 uintptr) (uintptr, uintptr, unix.Errno) {
	return shimSyscall6(n, a1, a2, a3, 0, 0, 0)
}

func shimSyscall6(n, a1, a2, a3, a4, a5, a6 uintptr) (uintptr, uintptr, unix.Errno) {
	switch n {
	case unix.SYS_OPEN:
		return unix.Syscall6(unix.SYS_OPENAT, atFDCWD, a1, a2, a3, 0, 0)
	case unix.SYS_STAT:
		return unix.Syscall6(unix.SYS_NEWFSTATAT, atFDCWD, a1, a2, 0, 0, 0)
	case unix.SYS_LSTAT:
		return unix.Syscall6(unix.SYS_NEWFSTATAT, atFDCWD, a1, a2, unix.AT_SYMLINK_NOFOLLOW, 0, 0)
	case unix.SYS_ACCESS:
		return unix.Syscall6(unix.SYS_FACCESSAT, atFDCWD, a1, a2, 0, 0, 0)
	case unix.SYS_MKDIR:
		return unix.Syscall6(unix.SYS_MKDIRAT, atFDCWD, a1, a2, 0, 0, 0)
	case unix.SYS_RMDIR:
		return unix.Syscall6(unix.SYS_UNLINKAT, atFDCWD, a1, unix.AT_REMOVEDIR, 0, 0, 0)
	case unix.SYS_UNLINK:
		return unix.Syscall6(unix.SYS_UNLINKAT, atFDCWD, a1, 0, 0, 0, 0)
	case unix.SYS_RENAME:
		return unix.Syscall6(unix.SYS_RENAMEAT, atFDCWD, a1, atFDCWD, a2, 0, 0)
	case unix.SYS_LINK:
		return unix.Syscall6(unix.SYS_LINKAT, atFDCWD, a1, atFDCWD, a2, 0, 0)
	case unix.SYS_SYMLINK:
		return unix.Syscall6(unix.SYS_SYMLINKAT, a1, atFDCWD, a2, 0, 0, 0)
	case unix.SYS_READLINK:
		return unix.Syscall6(unix.SYS_READLINKAT, atFDCWD, a1, a2, a3, 0, 0)
	case unix.SYS_CHMOD:
		return unix.Syscall6(unix.SYS_FCHMODAT, atFDCWD, a1, a2, 0, 0, 0)
	case unix.SYS_CHOWN:
		return unix.Syscall6(unix.SYS_FCHOWNAT, atFDCWD, a1, a2, a3, 0, 0)
	case unix.SYS_MKNOD:
		return unix.Syscall6(unix.SYS_MKNODAT, atFDCWD, a1, a2, a3, 0, 0)
	case unix.SYS_PIPE:
		return unix.Syscall6(unix.SYS_PIPE2, a1, 0, 0, 0, 0, 0)
	case unix.SYS_DUP2:
		if a1 == a2 { // dup2 returns the descriptor when both are equal; dup3 refuses
			if _, _, e := unix.Syscall(unix.SYS_FCNTL, a1, unix.F_GETFD, 0); e != 0 {
				return ^uintptr(0), 0, e
			}
			return a2, 0, 0
		}
		return unix.Syscall6(unix.SYS_DUP3, a1, a2, 0, 0, 0, 0)
	}
	return unix.Syscall6(n, a1, a2, a3, a4, a5, a6)
}
