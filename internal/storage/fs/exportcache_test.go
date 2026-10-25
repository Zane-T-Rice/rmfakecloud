package fs

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ddvk/rmfakecloud/internal/config"
)

func TestTemplatesStampFollowsEdits(t *testing.T) {
	dir := t.TempDir()
	s := &FileSystemStorage{Cfg: &config.Config{DataDir: dir}}

	if got := s.templatesStamp(); got != "" {
		t.Fatalf("no templates dir, stamp = %q", got)
	}

	tpl := filepath.Join(dir, "templates")
	if err := os.Mkdir(tpl, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(tpl, "A.template")
	if err := os.WriteFile(file, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	first := s.templatesStamp()

	// edited in place, the directory itself doesn't change
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(file, later, later); err != nil {
		t.Fatal(err)
	}
	if second := s.templatesStamp(); second == first {
		t.Fatalf("stamp did not change after an edit: %s", second)
	}
}
