// Package uploads stores Telegram attachments where Claude can read them, and expires them.
package uploads

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Save writes dir/<thread>/<name> (owner-only) via fill. name must come from SafeName. A failed fill leaves no file.
func Save(dir string, thread int, name string, fill func(io.Writer) error) (string, error) {
	td := filepath.Join(dir, strconv.Itoa(thread))
	for _, d := range []string{dir, td} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return "", err
		}
		if err := os.Chmod(d, 0o700); err != nil {
			return "", err
		}
	}
	p, err := filepath.Abs(filepath.Join(td, name))
	if err != nil {
		return "", err
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	err = fill(f)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(p)
		return "", err
	}
	return p, nil
}

// SafeName reduces a sender-supplied file name to a harmless base name.
func SafeName(name string) string {
	name = filepath.Base(name)
	name = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-' {
			return r
		}
		return '_'
	}, name)
	if len(name) > 100 {
		name = name[:100] // all ASCII after Map, except '_' runes which are ASCII too
	}
	if name == "" || name == "." || name == ".." {
		return "file"
	}
	return name
}

// Remove deletes every upload of one topic.
func Remove(dir string, thread int) error {
	return os.RemoveAll(filepath.Join(dir, strconv.Itoa(thread)))
}

// Cleanup deletes uploads older than maxAge, then the oldest until the total is at most maxTotal bytes.
func Cleanup(dir string, maxAge time.Duration, maxTotal int64, now time.Time) error {
	type file struct {
		path string
		mod  time.Time
		size int64
	}
	var keep []file
	var total int64
	var first error // a failed delete is reported, but never stops the rest
	remove := func(p string) bool {
		err := os.Remove(p)
		if err != nil && !errors.Is(err, fs.ErrNotExist) && first == nil {
			first = err
		}
		return err == nil || errors.Is(err, fs.ErrNotExist)
	}
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) { // deleted under us (e.g. by Claude)
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if now.Sub(fi.ModTime()) > maxAge {
			remove(p)
			return nil
		}
		keep = append(keep, file{p, fi.ModTime(), fi.Size()})
		total += fi.Size()
		return nil
	})
	if err != nil {
		return err
	}
	sort.Slice(keep, func(i, j int) bool { return keep[i].mod.Before(keep[j].mod) })
	for _, f := range keep {
		if total <= maxTotal {
			break
		}
		if remove(f.path) {
			total -= f.size
		}
	}
	// Remove fails on non-empty dirs, which is what we want.
	es, _ := os.ReadDir(dir)
	for _, e := range es {
		if e.IsDir() {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
	return first
}
