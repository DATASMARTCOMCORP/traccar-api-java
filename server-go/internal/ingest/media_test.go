package ingest

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteMedia(t *testing.T) {
	root := t.TempDir()
	name, err := writeMedia(root, "123", "20260101000000.jpg", []byte{1, 2, 3})
	if err != nil || name != "20260101000000.jpg" {
		t.Fatalf("got %q, %v", name, err)
	}
	b, err := os.ReadFile(filepath.Join(root, "123", name))
	if err != nil || string(b) != "\x01\x02\x03" {
		t.Fatalf("file %v %v", b, err)
	}
	for _, id := range []string{"../x", "/etc", "a/../../x"} {
		if _, err := writeMedia(root, id, "f.jpg", nil); err == nil {
			t.Errorf("%q: written outside the media path", id)
		}
	}
}
