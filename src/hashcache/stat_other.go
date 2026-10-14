//go:build !unix

package hashcache

import "os"

func statIdentity(os.FileInfo) (dev, ino uint64, ctimeNs int64) {
	return 0, 0, 0
}
