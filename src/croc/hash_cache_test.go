package croc

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	// Keep the persistent sender hash cache out of the user's configuration.
	dir, err := os.MkdirTemp("", "croc-test-config-")
	if err != nil {
		panic(err)
	}
	os.Setenv("CROC_CONFIG_DIR", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func agedProgressiveTestFile(t *testing.T, directory, name string, data []byte) FileInfo {
	t.Helper()
	progressiveTestFile(t, directory, name, data)
	filename := filepath.Join(directory, name)
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(filename, old, old); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(filename)
	if err != nil {
		t.Fatal(err)
	}
	return FileInfo{
		Name:         name,
		FolderRemote: "./",
		FolderSource: directory,
		Size:         info.Size(),
		ModTime:      info.ModTime(),
		Mode:         info.Mode(),
	}
}

func countingHashClient(options Options, calls *int) *Client {
	client := progressiveTestClient(options.HashAlgorithm)
	client.Options = options
	hash := client.stop.hash
	client.stop.hash = func(fname string, algorithm string, showProgress ...bool) ([]byte, error) {
		*calls++
		return hash(fname, algorithm, showProgress...)
	}
	return client
}

func TestSendReusesCachedHashForUnchangedFile(t *testing.T) {
	t.Setenv("CROC_CONFIG_DIR", t.TempDir())
	directory := t.TempDir()
	data := bytes.Repeat([]byte("cached payload "), 4096)
	file := agedProgressiveTestFile(t, directory, "payload", data)
	options := Options{IsSender: true, HashAlgorithm: defaultHashAlgorithm}

	var calls int
	first := countingHashClient(options, &calls)
	if err := first.sendCollectFiles([]FileInfo{file}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("first send hashed %d times", calls)
	}

	calls = 0
	second := countingHashClient(options, &calls)
	if err := second.sendCollectFiles([]FileInfo{file}); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("unchanged file rehashed %d times", calls)
	}
	if !bytes.Equal(first.FilesToTransfer[0].Hash, second.FilesToTransfer[0].Hash) ||
		first.FilesToTransfer[0].IsCompressed != second.FilesToTransfer[0].IsCompressed {
		t.Fatal("cached preparation differs from computed preparation")
	}
	if second.hashCacheHits.Load() != 1 {
		t.Fatalf("cache hits = %d", second.hashCacheHits.Load())
	}

	calls = 0
	disabled := countingHashClient(Options{IsSender: true, HashAlgorithm: defaultHashAlgorithm, NoHashCache: true}, &calls)
	if err := disabled.sendCollectFiles([]FileInfo{file}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("NoHashCache hashed %d times", calls)
	}

	data[0] ^= 0xff
	changed := agedProgressiveTestFile(t, directory, "payload", data)
	calls = 0
	third := countingHashClient(options, &calls)
	if err := third.sendCollectFiles([]FileInfo{changed}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("modified file hashed %d times", calls)
	}
	if bytes.Equal(first.FilesToTransfer[0].Hash, third.FilesToTransfer[0].Hash) {
		t.Fatal("modified file reused stale hash")
	}
}
