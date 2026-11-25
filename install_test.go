package tgbridge

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/pem"
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

// fakeRelease serves a v0.1.0 archive whose "binary" is a shell script over TLS; sum overrides the checksum.
// It returns the env that points install.sh at it and makes curl trust its certificate.
func fakeRelease(t *testing.T, sum string) []string {
	srv := httptest.NewTLSServer(releaseMux(t, sum))
	t.Cleanup(srv.Close)
	return append(trustTLS(t, srv), "TGBRIDGE_BASE_URL="+srv.URL)
}

// releaseMux serves the v0.1.0 archive and its checksums.txt.
func releaseMux(t *testing.T, sum string) http.Handler {
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
		// a longer name with the archive's prefix comes first: only the exact name may match
		fmt.Fprintf(w, "%s  %s.sbom.json\n%s  other.tar.gz\n%s  %s\n", strings.Repeat("0", 64), name, strings.Repeat("0", 64), sum, name)
	})
	return mux
}

// trustTLS writes srv's certificate to a file and returns the env that makes curl trust it.
func trustTLS(t *testing.T, srv *httptest.Server) []string {
	t.Helper()
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	return []string{"CURL_CA_BUNDLE=" + ca, "SSL_CERT_FILE=" + ca}
}

// runInstall runs script with env (after the defaults) in a temp home, behind the wrap command if any.
func runInstall(t *testing.T, script string, env []string, wrap ...string) (string, string, error) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("install.sh is not for windows")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	home := t.TempDir()
	dir := filepath.Join(home, "bin")
	args := append(wrap, "sh", script)
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = append(os.Environ(), "HOME="+home, "TGBRIDGE_VERSION=v0.1.0", "TGBRIDGE_INSTALL_DIR="+dir)
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.CombinedOutput()
	return dir, string(out), err
}

func installed(dir string) bool { _, err := os.Stat(filepath.Join(dir, "tgbridge")); return err == nil }

func TestInstallScript(t *testing.T) {
	dir, out, err := runInstall(t, "install.sh", append(fakeRelease(t, ""), "TGBRIDGE_NO_INIT=1"))
	if err != nil {
		t.Fatalf("install failed: %v\n%s", err, out)
	}
	fi, err := os.Stat(filepath.Join(dir, "tgbridge"))
	if err != nil || fi.Mode().Perm() != 0o755 {
		t.Fatalf("binary missing or wrong mode: %v %v", fi, err)
	}
}

func TestInstallScriptBadChecksum(t *testing.T) {
	dir, out, err := runInstall(t, "install.sh", append(fakeRelease(t, strings.Repeat("a", 64)), "TGBRIDGE_NO_INIT=1"))
	if err == nil {
		t.Fatalf("want failure, got success\n%s", out)
	}
	if installed(dir) {
		t.Fatal("binary installed despite bad checksum")
	}
}

func TestInstallScriptNoTTY(t *testing.T) {
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("no setsid")
	}
	// setsid drops the controlling terminal, so /dev/tty can't be opened
	// the install dir is not on PATH here, so the hint must give the full path
	dir, out, err := runInstall(t, "install.sh", fakeRelease(t, ""), "setsid")
	if err != nil {
		t.Fatalf("install failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, `run: "`+filepath.Join(dir, "tgbridge")+`" init`) {
		t.Fatalf("output lacks the init hint:\n%s", out)
	}
}

// A download cut at a line boundary must run nothing: main is only called on the last line.
func TestInstallScriptTruncated(t *testing.T) {
	b, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.SplitAfter(string(b), "\n")
	cut := filepath.Join(t.TempDir(), "install.sh")
	if err := os.WriteFile(cut, []byte(strings.Join(lines[:len(lines)*6/10], "")), 0o644); err != nil {
		t.Fatal(err)
	}
	dir, out, err := runInstall(t, cut, append(fakeRelease(t, ""), "TGBRIDGE_NO_INIT=1"))
	if err == nil || installed(dir) || strings.Contains(out, "Downloading") {
		t.Fatalf("a truncated script must fail and do nothing: %v\n%s", err, out)
	}
}

func TestInstallScriptRefusesHTTP(t *testing.T) {
	plain := httptest.NewServer(releaseMux(t, "")) // a complete, valid release, but over http
	t.Cleanup(plain.Close)
	dir, out, err := runInstall(t, "install.sh", []string{"TGBRIDGE_BASE_URL=" + plain.URL, "TGBRIDGE_NO_INIT=1"})
	if err == nil || installed(dir) || !strings.Contains(out, "TGBRIDGE_BASE_URL must start with https://") {
		t.Fatalf("an http base URL must be refused: %v\n%s", err, out)
	}

	// an https server that redirects to the http release: curl must not follow it
	redir := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL+r.URL.Path, http.StatusFound)
	}))
	t.Cleanup(redir.Close)
	dir, out, err = runInstall(t, "install.sh", append(trustTLS(t, redir), "TGBRIDGE_BASE_URL="+redir.URL, "TGBRIDGE_NO_INIT=1"))
	if err == nil || installed(dir) || !strings.Contains(out, "download failed") {
		t.Fatalf("an https->http redirect must fail: %v\n%s", err, out)
	}
}

func TestInstallScriptBadVersion(t *testing.T) {
	for _, v := range []string{"v0.1.0/../../x", "0.1.0", "v1", "v0.1.0?x=1", "v0.1.0 x"} {
		dir, out, err := runInstall(t, "install.sh", append(fakeRelease(t, ""), "TGBRIDGE_VERSION="+v, "TGBRIDGE_NO_INIT=1"))
		if err == nil || installed(dir) || !strings.Contains(out, "unexpected version") {
			t.Errorf("%q must be refused: %v\n%s", v, err, out)
		}
	}
}
