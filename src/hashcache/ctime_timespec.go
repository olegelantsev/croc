//go:build darwin || freebsd || netbsd

package hashcache

import "syscall"

func ctimeNanos(st *syscall.Stat_t) int64 {
	return st.Ctimespec.Nano()
}
