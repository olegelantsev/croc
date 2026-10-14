//go:build unix

package hashcache

import (
	"os"
	"syscall"
)

func statIdentity(fi os.FileInfo) (dev, ino uint64, ctimeNs int64) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, 0
	}
	return uint64(st.Dev), uint64(st.Ino), ctimeNanos(st)
}
