package node

import (
	"os"
	"path/filepath"
	"testing"
)

// A leftover .tmp with a loose mode must not hand that mode to a key written through it.
func TestWriteAtomicIsPrivate(t *testing.T) {
	p := filepath.Join(t.TempDir(), "node.key")
	if err := os.WriteFile(p+".tmp", []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(p, []byte("secret")); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, want 0600", fi.Mode().Perm())
	}
}
