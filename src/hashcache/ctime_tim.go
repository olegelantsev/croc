//go:build unix && !(darwin || freebsd || netbsd)

package hashcache

import "syscall"

func ctimeNanos(st *syscall.Stat_t) int64 {
	return st.Ctim.Nano()
}
