package uploads

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func put(t *testing.T, dir string, thread int, name string, size int, age time.Duration) string {
	t.Helper()
	p, err := Save(dir, thread, name, func(w io.Writer) error { _, err := w.Write(make([]byte, size)); return err })
	if err != nil {
		t.Fatal(err)
	}
	mt := time.Now().Add(-age)
	if err := os.Chtimes(p, mt, mt); err != nil {
		t.Fatal(err)
	}
	return p
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func TestSaveModesAndPath(t *testing.T) {
	dir := t.TempDir()
	p, err := Save(dir, 7, "a.txt", func(w io.Writer) error { _, err := w.Write([]byte("hi")); return err })
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(p) != filepath.Join(dir, "7") {
		t.Fatalf("path %s", p)
	}
	fi, _ := os.Stat(p)
	di, _ := os.Stat(filepath.Dir(p))
	if fi.Mode().Perm() != 0o600 || di.Mode().Perm() != 0o700 {
		t.Fatalf("modes %v %v", fi.Mode().Perm(), di.Mode().Perm())
	}
	if b, _ := os.ReadFile(p); string(b) != "hi" {
		t.Fatalf("content %q", b)
	}
}

func TestSaveRemovesFileOnError(t *testing.T) {
	dir := t.TempDir()
	_, err := Save(dir, 7, "a.txt", func(w io.Writer) error { w.Write([]byte("part")); return errors.New("boom") })
	if err == nil {
		t.Fatal("want error")
	}
	if es, _ := os.ReadDir(filepath.Join(dir, "7")); len(es) != 0 {
		t.Fatalf("left %v", es)
	}
}

func TestSafeName(t *testing.T) {
	for in, want := range map[string]string{"../../x": "x", "/etc/passwd": "passwd", "": "file", "..": "file", "a b?.png": "a_b_.png"} {
		if got := SafeName(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
	if got := SafeName(strings.Repeat("a", 300)); len(got) > 100 {
		t.Fatalf("len %d", len(got))
	}
}

func TestCleanupByAge(t *testing.T) {
	dir := t.TempDir()
	old := put(t, dir, 1, "old", 1, 8*24*time.Hour)
	new := put(t, dir, 1, "new", 1, 24*time.Hour)
	if err := Cleanup(dir, 7*24*time.Hour, 1<<20, time.Now()); err != nil {
		t.Fatal(err)
	}
	if exists(old) || !exists(new) {
		t.Fatal("age cleanup wrong")
	}
}

func TestCleanupBySize(t *testing.T) {
	dir := t.TempDir()
	// walk order is c, b, a: the oldest comes last, so only sorting by age deletes the right one
	c := put(t, dir, 1, "c", 10, time.Hour)
	b := put(t, dir, 2, "b", 10, 2*time.Hour)
	a := put(t, dir, 3, "a", 10, 3*time.Hour)
	if err := Cleanup(dir, 24*time.Hour, 20, time.Now()); err != nil {
		t.Fatal(err)
	}
	if exists(a) || !exists(b) || !exists(c) {
		t.Fatal("size cleanup wrong")
	}
}

func TestCleanupRemovesEmptyTopicDir(t *testing.T) {
	dir := t.TempDir()
	put(t, dir, 1, "old", 1, 8*24*time.Hour)
	if err := Cleanup(dir, 24*time.Hour, 1<<20, time.Now()); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(dir, "1")) {
		t.Fatal("empty topic dir left")
	}
}

func TestRemoveTopic(t *testing.T) {
	dir := t.TempDir()
	put(t, dir, 1, "a", 1, 0)
	b := put(t, dir, 2, "b", 1, 0)
	if err := Remove(dir, 1); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(dir, "1")) || !exists(b) {
		t.Fatal("remove wrong")
	}
}

func TestCleanupMissingDir(t *testing.T) {
	if err := Cleanup(filepath.Join(t.TempDir(), "nope"), time.Hour, 1, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestCleanupContinuesPastFailedDelete(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the read-only dir")
	}
	dir := t.TempDir()
	stuck := put(t, dir, 1, "stuck", 1, 8*24*time.Hour)
	old := put(t, dir, 2, "old", 1, 8*24*time.Hour)
	os.Chmod(filepath.Dir(stuck), 0o500) // can't delete inside
	t.Cleanup(func() { os.Chmod(filepath.Dir(stuck), 0o700) })
	if err := Cleanup(dir, 24*time.Hour, 1<<20, time.Now()); err == nil {
		t.Fatal("want the failed delete reported")
	}
	if exists(old) {
		t.Fatal("cleanup stopped at the first failed delete")
	}
}
