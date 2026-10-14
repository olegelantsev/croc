// Package hashcache remembers the digest and compression decision computed
// for a sender's source file so that restarting an interrupted send does not
// read unchanged files again.
//
// An entry is reused only when the file's size, modification time, mode and,
// where the platform exposes them, device, inode and change time all match
// the values recorded when the digest was computed. Every failure is
// non-fatal: callers fall back to hashing the file.
package hashcache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	log "github.com/schollz/croc/v11/src/logger"
	"github.com/schollz/croc/v11/src/utils"
)

const (
	directoryName = "hash-cache"
	// racyWindow skips caching files modified this recently: a further write
	// within the same timestamp tick would not change the recorded metadata.
	racyWindow = 2 * time.Second
	maxAge     = 30 * 24 * time.Hour
	maxEntries = 10000
)

// Entry is a cached preparation result for one file and hash algorithm.
type Entry struct {
	Path         string      `json:"path"`
	Algorithm    string      `json:"algorithm"`
	Size         int64       `json:"size"`
	ModTimeNs    int64       `json:"mtime"`
	Mode         os.FileMode `json:"mode"`
	Dev          uint64      `json:"dev,omitempty"`
	Ino          uint64      `json:"ino,omitempty"`
	CtimeNs      int64       `json:"ctime,omitempty"`
	Hash         []byte      `json:"hash"`
	IsCompressed bool        `json:"compressed"`
	HasCompress  bool        `json:"has_compress"`
	LastUsed     time.Time   `json:"last_used"`
}

var (
	now       = time.Now
	pruneOnce sync.Once
)

func directory(create bool) (string, error) {
	base, err := utils.GetConfigDir(false)
	if err != nil || base == "" {
		return "", err
	}
	dir := filepath.Join(base, directoryName)
	if create {
		if err = os.MkdirAll(dir, 0o700); err != nil {
			return "", err
		}
	}
	return dir, nil
}

func entryName(absPath, algorithm string) string {
	sum := sha256.Sum256([]byte(absPath + "\x00" + algorithm))
	return hex.EncodeToString(sum[:]) + ".json"
}

func (e *Entry) matches(absPath, algorithm string, fi os.FileInfo) bool {
	dev, ino, ctime := statIdentity(fi)
	return e.Path == absPath && e.Algorithm == algorithm && len(e.Hash) > 0 &&
		e.Size == fi.Size() && e.ModTimeNs == fi.ModTime().UnixNano() &&
		e.Mode == fi.Mode() && e.Dev == dev && e.Ino == ino && e.CtimeNs == ctime
}

// Lookup returns the entry for path and algorithm when fi still describes the
// file that was hashed.
func Lookup(path, algorithm string, fi os.FileInfo) (*Entry, bool) {
	if fi == nil || !fi.Mode().IsRegular() {
		return nil, false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, false
	}
	dir, err := directory(false)
	if err != nil || dir == "" {
		return nil, false
	}
	pruneOnce.Do(func() { go prune(dir) })
	name := filepath.Join(dir, entryName(absPath, algorithm))
	b, err := os.ReadFile(name)
	if err != nil {
		return nil, false
	}
	var e Entry
	if err = json.Unmarshal(b, &e); err != nil {
		log.Debugf("hash cache: ignoring corrupt entry %s: %v", name, err)
		return nil, false
	}
	if !e.matches(absPath, algorithm, fi) {
		return nil, false
	}
	e.LastUsed = now()
	if err = write(dir, name, &e); err != nil {
		log.Debugf("hash cache: %v", err)
	}
	return &e, true
}

// Store records hash for path. fi must describe the file as it was before and
// after hashing. hasCompress reports whether isCompressed was determined.
func Store(path, algorithm string, fi os.FileInfo, hash []byte, isCompressed, hasCompress bool) {
	if fi == nil || !fi.Mode().IsRegular() || len(hash) == 0 {
		return
	}
	if now().Sub(fi.ModTime()) < racyWindow {
		return
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return
	}
	dir, err := directory(true)
	if err != nil || dir == "" {
		log.Debugf("hash cache: no directory: %v", err)
		return
	}
	dev, ino, ctime := statIdentity(fi)
	e := Entry{
		Path:         absPath,
		Algorithm:    algorithm,
		Size:         fi.Size(),
		ModTimeNs:    fi.ModTime().UnixNano(),
		Mode:         fi.Mode(),
		Dev:          dev,
		Ino:          ino,
		CtimeNs:      ctime,
		Hash:         append([]byte(nil), hash...),
		IsCompressed: isCompressed,
		HasCompress:  hasCompress,
		LastUsed:     now(),
	}
	if err = write(dir, filepath.Join(dir, entryName(absPath, algorithm)), &e); err != nil {
		log.Debugf("hash cache: %v", err)
	}
}

// write replaces name atomically so that concurrent senders never observe a
// partially written entry.
func write(dir, name string, e *Entry) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".entry-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err = tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err = tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err = os.Rename(tmpName, name); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// prune removes entries unused for maxAge and the least recently used entries
// beyond maxEntries. File modification times track LastUsed because every
// lookup hit rewrites its entry.
func prune(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type candidate struct {
		name    string
		modTime time.Time
	}
	var kept []candidate
	cutoff := now().Add(-maxAge)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		name := filepath.Join(dir, entry.Name())
		if info.ModTime().Before(cutoff) {
			os.Remove(name)
			continue
		}
		kept = append(kept, candidate{name: name, modTime: info.ModTime()})
	}
	if len(kept) <= maxEntries {
		return
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].modTime.Before(kept[j].modTime) })
	for _, c := range kept[:len(kept)-maxEntries] {
		os.Remove(c.name)
	}
}
