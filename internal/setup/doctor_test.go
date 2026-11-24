package setup

import (
	"bytes"
	"context"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
)

func doctorText(cs []Check) string {
	var b bytes.Buffer
	Print(&b, cs)
	return b.String()
}

func TestDoctorFails(t *testing.T) {
	r := newRig(t)
	cs := Doctor(context.Background(), r.env)
	s := doctorText(cs)
	if !Failed(cs) {
		t.Fatalf("missing config must fail:\n%s", s)
	}
	for _, w := range []string{"no config", "run tgbridge init", "tgbridge service install", "v0.1.0"} {
		if !strings.Contains(s, w) {
			t.Errorf("missing %q in\n%s", w, s)
		}
	}

	r.svc.installed = true // installed but not running: show where the logs are
	if s := doctorText(Doctor(context.Background(), r.env)); !strings.Contains(s, "journalctl --user -u tgbridge -n 50") {
		t.Errorf("want the log hint:\n%s", s)
	}
	r.env.Svc = nil
	if s := doctorText(Doctor(context.Background(), r.env)); !strings.Contains(s, "WSL2") {
		t.Errorf("want the WSL2 note:\n%s", s)
	}
}

func TestDoctorThreadedModeOff(t *testing.T) {
	r := newRig(t)
	r.bot.topics = false
	must(t, os.MkdirAll(filepath.Dir(r.env.ConfigPath), 0o700))
	must(t, os.WriteFile(r.env.ConfigPath, []byte("owner_id: 7\n"), 0o600))
	must(t, os.WriteFile(r.tokenPath(), []byte(goodToken), 0o600))
	if s := doctorText(Doctor(context.Background(), r.env)); !strings.Contains(s, "Threaded Mode") {
		t.Errorf("want the Threaded Mode fix:\n%s", s)
	}
}

func TestDoctorAllOK(t *testing.T) {
	r := newRig(t)
	must(t, os.MkdirAll(filepath.Dir(r.env.ConfigPath), 0o700))
	must(t, os.WriteFile(r.env.ConfigPath, []byte("owner_id: 7\n"), 0o600))
	must(t, os.WriteFile(r.tokenPath(), []byte(goodToken), 0o600))
	must(t, os.MkdirAll(filepath.Dir(r.env.ClaudeSettings), 0o700))
	must(t, os.WriteFile(r.env.ClaudeSettings, []byte(`{"hooks":{"Stop":[{"hooks":[{"command":"/b/tgbridge hook Stop"}]}]}}`), 0o600))
	r.svc.installed, r.svc.active = true, true
	cs := Doctor(context.Background(), r.env)
	for _, c := range cs {
		if c.Status != OK && !(c.Status == Info && c.Name == "version") {
			t.Errorf("want all ok:\n%s", doctorText(cs))
			break
		}
	}
	if s := doctorText(cs); !strings.Contains(s, "@testbot") {
		t.Errorf("want the bot name:\n%s", s)
	}
}

func TestDoctorLingerWithoutUSER(t *testing.T) {
	r := newRig(t)
	t.Setenv("USER", "")
	u, err := user.Current()
	must(t, err)
	r.env.Run, _ = fake(map[string]string{"loginctl show-user " + u.Username + " -p Linger --value": "yes\n"}, nil)
	if s := doctorText(Doctor(context.Background(), r.env)); !strings.Contains(s, "ok    linger") {
		t.Errorf("want linger read for the current user:\n%s", s)
	}
}
