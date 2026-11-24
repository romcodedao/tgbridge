package setup

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/romcodedao/tgbridge/internal/config"
	"github.com/romcodedao/tgbridge/internal/install"
)

// Action is one step of the uninstall plan.
type Action struct {
	Desc, Path string
	Do         func(ctx context.Context) error
}

const skipped = "skipped (not a tgbridge folder)"

// safeToRemove holds when p is strictly under home and its path below home names tgbridge.
func safeToRemove(home, p string) bool {
	if !filepath.IsAbs(home) || !filepath.IsAbs(p) {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(home), filepath.Clean(p))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
		return false
	}
	return strings.Contains(rel, "tgbridge")
}

func lexists(p string) bool { _, err := os.Lstat(p); return err == nil }

// UninstallPlan lists what `tgbridge uninstall` removes and what it leaves in place.
// purge adds config, token, state, uploads, the launchd log and a binary in ~/.local/bin.
func UninstallPlan(e Env, cfg *config.Config, purge bool) (do []Action, kept []string) {
	if e.Svc != nil && e.Svc.Installed() {
		do = append(do, Action{"stop and remove " + e.Svc.Path(), e.Svc.Path(), e.Svc.Uninstall})
	}
	do = append(do, Action{"remove tgbridge's hooks and MCP server from " + e.ClaudeSettings, e.ClaudeSettings, func(ctx context.Context) error {
		return install.Uninstall(e.ClaudeSettings, func(args ...string) error { _, err := e.Run(ctx, cfg.ClaudeBin, args...); return err })
	}})
	if bak := e.ClaudeSettings + ".tgbridge.bak"; lexists(bak) {
		kept = append(kept, bak+" (your Claude settings from before tgbridge)")
	}

	conf := filepath.Dir(e.ConfigPath)
	if !safeToRemove(e.Home, conf) { // e.g. TGBRIDGE_CONFIG=~/x.yaml: the dir is home itself
		conf = e.ConfigPath
	}
	items := [][2]string{{"config", conf}, {"token", cfg.TokenFile}, {"state", cfg.StateDir}, {"uploads", cfg.Uploads.Dir}}
	if e.GOOS == "darwin" {
		items = append(items, [2]string{"log", filepath.Join(e.Home, "Library/Logs/tgbridge.log")})
	}
	if !purge {
		for _, it := range append(items, [2]string{"binary", e.Bin}) {
			if it[1] != "" && lexists(it[1]) {
				kept = append(kept, it[1]+" ("+it[0]+")")
			}
		}
		return do, kept
	}

	rm := func(p string) {
		if p == "" {
			return
		}
		if !safeToRemove(e.Home, p) {
			kept = append(kept, p+": "+skipped)
			return
		}
		p = filepath.Clean(p)
		for _, a := range do {
			if p == a.Path || strings.HasPrefix(p, a.Path+"/") {
				return // inside a folder already being deleted
			}
		}
		if !lexists(p) {
			return
		}
		// Check again with symlinks resolved, so a tgbridge-named link can't lead out of home.
		home, err1 := filepath.EvalSymlinks(e.Home)
		dir, err2 := filepath.EvalSymlinks(filepath.Dir(p))
		if err1 != nil || err2 != nil || !safeToRemove(home, filepath.Join(dir, filepath.Base(p))) {
			kept = append(kept, p+": "+skipped)
			return
		}
		do = append(do, Action{"delete " + p, p, func(context.Context) error { return os.RemoveAll(p) }})
	}
	for _, it := range items {
		rm(it[1])
	}

	// Only the binary install.sh or the release tarball put in ~/.local/bin; go install, brew etc. are the user's.
	bin := filepath.Join(e.Home, ".local/bin/tgbridge")
	real, err := filepath.EvalSymlinks(e.Bin)
	if err != nil {
		real = e.Bin
	}
	realBin, _ := filepath.EvalSymlinks(bin)
	if real == bin || real != "" && real == realBin {
		rm(bin)
	} else if e.Bin != "" && lexists(e.Bin) {
		kept = append(kept, e.Bin+": remove it yourself: rm "+e.Bin)
	}
	return do, kept
}
