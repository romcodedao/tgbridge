package setup

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
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

const skipped = "skipped (outside tgbridge's folders)"

type item struct{ what, path, def string }

// safeToRemove holds when p is strictly under home (not "/") and its path below home names tgbridge.
func safeToRemove(home, p string) bool {
	if !filepath.IsAbs(home) || !filepath.IsAbs(p) || filepath.Clean(home) == "/" {
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

	// Folders are deleted only at tgbridge's default locations: a custom state_dir may be a checkout or the like.
	defConf := filepath.Join(e.Home, ".config/tgbridge")
	defState := filepath.Join(e.Home, ".local/state/tgbridge")
	defUploads := filepath.Join(e.Home, ".cache/tgbridge/uploads")
	conf := item{"config", e.ConfigPath, ""}
	if filepath.Clean(filepath.Dir(e.ConfigPath)) == defConf {
		conf = item{"config", defConf, defConf}
	}
	items := []item{conf, {"token", cfg.TokenFile, ""}, {"state", cfg.StateDir, defState}, {"uploads", cfg.Uploads.Dir, defUploads}}
	if e.GOOS == "darwin" {
		items = append(items, item{"log", filepath.Join(e.Home, "Library/Logs/tgbridge.log"), ""})
	}
	if !purge {
		for _, it := range append(items, item{"binary", e.Bin, ""}) {
			if it.path != "" && lexists(it.path) {
				kept = append(kept, it.path+" ("+it.what+")")
			}
		}
		return do, kept
	}

	// rm plans deleting p; def != "" marks a folder, deleted only when p is that default.
	rm := func(p, def string) {
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
		if def != "" && p != def {
			kept = append(kept, p+": custom folder: remove it yourself: rm -rf "+p)
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
		rm(it.path, it.def)
	}
	// ~/.cache/tgbridge goes too when the uploads folder is all it holds.
	cache := filepath.Dir(defUploads)
	if ents, err := os.ReadDir(cache); err == nil && len(ents) == 1 && ents[0].Name() == "uploads" && planned(do, defUploads) {
		do = append(do, Action{"delete " + cache, cache, func(context.Context) error { return os.Remove(cache) }})
	}

	// Only the binary install.sh or the release tarball put in ~/.local/bin; go install, brew etc. are the user's.
	bin := filepath.Join(e.Home, ".local/bin/tgbridge")
	real, err := filepath.EvalSymlinks(e.Bin)
	if err != nil {
		real = e.Bin
	}
	realBin, _ := filepath.EvalSymlinks(bin)
	if real == bin || real != "" && real == realBin {
		rm(bin, "")
	} else if e.Bin != "" && lexists(e.Bin) {
		kept = append(kept, e.Bin+": remove it yourself: rm "+e.Bin)
	}
	return do, kept
}

func planned(do []Action, p string) bool {
	for _, a := range do {
		if a.Path == p {
			return true
		}
	}
	return false
}

// ConfirmPurge asks before --purge deletes; only "y" or "yes" proceeds, and no answer at all is an error.
func ConfirmPurge(in io.Reader, out io.Writer) (bool, error) {
	fmt.Fprint(out, "Delete these? [y/N] ")
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintln(out)
		return false, errors.New("no answer: use --yes to purge without a prompt")
	}
	a := strings.ToLower(strings.TrimSpace(line))
	return a == "y" || a == "yes", nil
}
