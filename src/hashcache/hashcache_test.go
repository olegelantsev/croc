package hashcache

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testFile(t *testing.T, data string) (string, os.FileInfo) {
	t.Helper()
	t.Setenv("CROC_CONFIG_DIR", t.TempDir())
	name := filepath.Join(t.TempDir(), "payload")
	if err := os.WriteFile(name, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return name, settle(t, name)
}

// settle moves the file's modification time outside the racy window.
func settle(t *testing.T, name string) os.FileInfo {
	t.Helper()
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(name, old, old); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(name)
	if err != nil {
		t.Fatal(err)
	}
	return fi
}

func TestStoreThenLookup(t *testing.T) {
	name, fi := testFile(t, "hello")
	Store(name, "xxhash", fi, []byte{1, 2, 3}, true, true)
	e, ok := Lookup(name, "xxhash", fi)
	if !ok {
		t.Fatal("expected cache hit")
	}
	if string(e.Hash) != "\x01\x02\x03" || !e.IsCompressed || !e.HasCompress {
		t.Fatalf("unexpected entry %+v", e)
	}
	if _, ok := Lookup(name, "md5", fi); ok {
		t.Fatal("hit for a different algorithm")
	}
}

func TestLookupMissesChangedFile(t *testing.T) {
	name, fi := testFile(t, "hello")
	Store(name, "xxhash", fi, []byte{1}, false, false)

	if err := os.WriteFile(name, []byte("HELLO"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Same size and restored mtime: only the change time (or, on platforms
	// without it, nothing) reveals the rewrite.
	if err := os.Chtimes(name, fi.ModTime(), fi.ModTime()); err != nil {
		t.Fatal(err)
	}
	changed, err := os.Lstat(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ctime := statIdentity(changed); ctime != 0 {
		if _, ok := Lookup(name, "xxhash", changed); ok {
			t.Fatal("hit after rewrite with restored mtime")
		}
	}

	if err := os.WriteFile(name, []byte("longer"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := Lookup(name, "xxhash", settle(t, name)); ok {
		t.Fatal("hit after size and mtime change")
	}

	if err := os.Chmod(name, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := Lookup(name, "xxhash", settle(t, name)); ok {
		t.Fatal("hit after mode change")
	}
}

func TestStoreSkipsRacyModTime(t *testing.T) {
	name, _ := testFile(t, "hello")
	if err := os.WriteFile(name, []byte("fresh"), 0o600); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(name)
	if err != nil {
		t.Fatal(err)
	}
	Store(name, "xxhash", fi, []byte{1}, false, false)
	if _, ok := Lookup(name, "xxhash", fi); ok {
		t.Fatal("stored a file modified within the racy window")
	}
}

func TestCorruptEntryIgnored(t *testing.T) {
	name, fi := testFile(t, "hello")
	Store(name, "xxhash", fi, []byte{1}, false, false)
	dir, err := directory(false)
	if err != nil {
		t.Fatal(err)
	}
	abs, _ := filepath.Abs(name)
	if err := os.WriteFile(filepath.Join(dir, entryName(abs, "xxhash")), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := Lookup(name, "xxhash", fi); ok {
		t.Fatal("hit on corrupt entry")
	}
}

func TestPruneRemovesStaleEntries(t *testing.T) {
	name, fi := testFile(t, "hello")
	Store(name, "xxhash", fi, []byte{1}, false, false)
	Store(name, "md5", fi, []byte{2}, false, false)
	dir, err := directory(false)
	if err != nil {
		t.Fatal(err)
	}
	abs, _ := filepath.Abs(name)
	stale := filepath.Join(dir, entryName(abs, "md5"))
	old := time.Now().Add(-maxAge - time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	prune(dir)
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale entry kept: %v", err)
	}
	if _, ok := Lookup(name, "xxhash", fi); !ok {
		t.Fatal("fresh entry pruned")
	}
}
