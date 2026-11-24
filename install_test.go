package tgbridge

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeRelease serves a v0.1.0 archive whose "binary" is a shell script; sum overrides the checksum.
func fakeRelease(t *testing.T, sum string) string {
	t.Helper()
	body := []byte("#!/bin/sh\necho \"init ran\"\n")
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "tgbridge", Mode: 0o755, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	tw.Write(body)
	tw.Close()
	gz.Close()
	name := fmt.Sprintf("tgbridge_0.1.0_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	if sum == "" {
		sum = fmt.Sprintf("%x", sha256.Sum256(buf.Bytes()))
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/download/v0.1.0/"+name, func(w http.ResponseWriter, _ *http.Request) { w.Write(buf.Bytes()) })
	mux.HandleFunc("/download/v0.1.0/checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "%s  other.tar.gz\n%s  %s\n", strings.Repeat("0", 64), sum, name)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

func runInstall(t *testing.T, base string, env []string, wrap ...string) (string, string, error) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("install.sh is not for windows")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	home := t.TempDir()
	dir := filepath.Join(home, "bin")
	args := append(wrap, "sh", "install.sh")
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = append(os.Environ(), "HOME="+home, "TGBRIDGE_BASE_URL="+base,
		"TGBRIDGE_VERSION=v0.1.0", "TGBRIDGE_INSTALL_DIR="+dir)
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.CombinedOutput()
	return dir, string(out), err
}

func TestInstallScript(t *testing.T) {
	dir, out, err := runInstall(t, fakeRelease(t, ""), []string{"TGBRIDGE_NO_INIT=1"})
	if err != nil {
		t.Fatalf("install failed: %v\n%s", err, out)
	}
	fi, err := os.Stat(filepath.Join(dir, "tgbridge"))
	if err != nil || fi.Mode().Perm() != 0o755 {
		t.Fatalf("binary missing or wrong mode: %v %v", fi, err)
	}
}

func TestInstallScriptBadChecksum(t *testing.T) {
	dir, out, err := runInstall(t, fakeRelease(t, strings.Repeat("a", 64)), []string{"TGBRIDGE_NO_INIT=1"})
	if err == nil {
		t.Fatalf("want failure, got success\n%s", out)
	}
	if _, serr := os.Stat(filepath.Join(dir, "tgbridge")); serr == nil {
		t.Fatal("binary installed despite bad checksum")
	}
}

func TestInstallScriptNoTTY(t *testing.T) {
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("no setsid")
	}
	// setsid drops the controlling terminal, so /dev/tty can't be opened
	_, out, err := runInstall(t, fakeRelease(t, ""), nil, "setsid")
	if err != nil {
		t.Fatalf("install failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "run: tgbridge init") {
		t.Fatalf("output lacks the init hint:\n%s", out)
	}
}
