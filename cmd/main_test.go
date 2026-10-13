package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSyncCertificatesToDisk(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "tls.crt")
	keyPath := filepath.Join(dir, "tls.key")

	if err := syncCertificatesToDisk(dir, "tls.crt", "tls.key", []byte("cert-1"), []byte("key-1")); err != nil {
		t.Fatalf("initial sync failed: %v", err)
	}
	assertFileContent(t, certPath, "cert-1")
	assertFileContent(t, keyPath, "key-1")

	// An unchanged secret must not rewrite the files, otherwise the cert watcher reloads needlessly.
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(certPath, past, past); err != nil {
		t.Fatal(err)
	}
	if err := syncCertificatesToDisk(dir, "tls.crt", "tls.key", []byte("cert-1"), []byte("key-1")); err != nil {
		t.Fatalf("unchanged sync failed: %v", err)
	}
	if info, err := os.Stat(certPath); err != nil || !info.ModTime().Equal(past) {
		t.Errorf("expected unchanged certificate not to be rewritten")
	}

	// A certificate rotated by another replica must be picked up.
	if err := syncCertificatesToDisk(dir, "tls.crt", "tls.key", []byte("cert-2"), []byte("key-2")); err != nil {
		t.Fatalf("rotated sync failed: %v", err)
	}
	assertFileContent(t, certPath, "cert-2")
	assertFileContent(t, keyPath, "key-2")
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if string(got) != want {
		t.Errorf("%s = %q, want %q", path, got, want)
	}
}
