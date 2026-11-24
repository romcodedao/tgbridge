package setup

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

// fake returns a Runner that records "name args" lines and answers from out (keyed by the same line).
func fake(out map[string]string, errs map[string]error) (Runner, *[]string) {
	var calls []string
	return func(_ context.Context, name string, args ...string) (string, error) {
		k := strings.Join(append([]string{name}, args...), " ")
		calls = append(calls, k)
		return out[k], errs[k]
	}, &calls
}

func found(string) (string, error) { return "/x/claude", nil }

func TestDetectClaude(t *testing.T) {
	ctx := context.Background()
	t.Run("logged in", func(t *testing.T) {
		run, _ := fake(map[string]string{
			"/x/claude --version":   "2.1.0 (Claude Code)\nextra\n",
			"/x/claude auth status": `{"loggedIn":true,"authMethod":"claude.ai"}`,
		}, nil)
		ci, err := DetectClaude(ctx, "claude", found, run)
		if err != nil || ci.Path != "/x/claude" || ci.Version != "2.1.0 (Claude Code)" || !ci.LoggedIn || ci.AuthMethod != "claude.ai" || ci.AuthErr != nil {
			t.Fatalf("%+v %v", ci, err)
		}
	})
	t.Run("logged out", func(t *testing.T) {
		run, _ := fake(map[string]string{"/x/claude auth status": `{"loggedIn":false}`}, nil)
		ci, _ := DetectClaude(ctx, "claude", found, run)
		if ci.LoggedIn || ci.AuthErr != nil {
			t.Fatalf("%+v", ci)
		}
	})
	t.Run("auth command error", func(t *testing.T) {
		run, _ := fake(nil, map[string]error{"/x/claude auth status": errors.New("boom")})
		ci, err := DetectClaude(ctx, "claude", found, run)
		if err != nil || ci.AuthErr == nil {
			t.Fatalf("%+v %v", ci, err)
		}
	})
	t.Run("bad json", func(t *testing.T) {
		run, _ := fake(map[string]string{"/x/claude auth status": "not json"}, nil)
		if ci, _ := DetectClaude(ctx, "claude", found, run); ci.AuthErr == nil {
			t.Fatalf("%+v", ci)
		}
	})
	t.Run("not found", func(t *testing.T) {
		run, calls := fake(nil, nil)
		_, err := DetectClaude(ctx, "claude", func(string) (string, error) { return "", errors.New("nope") }, run)
		if err == nil || len(*calls) != 0 {
			t.Fatalf("%v %v", err, *calls)
		}
	})
}

func TestClaudeChecks(t *testing.T) {
	const fix = "run `claude`, then /login"
	cases := []struct {
		ci   ClaudeInfo
		err  error
		want Status
		fix  string
	}{
		{ClaudeInfo{}, errors.New("x"), Fail, "install Claude Code: https://docs.claude.com/en/docs/claude-code/setup"},
		{ClaudeInfo{Path: "p", AuthErr: errors.New("x")}, nil, Warn, fix},
		{ClaudeInfo{Path: "p"}, nil, Fail, fix},
		{ClaudeInfo{Path: "p", LoggedIn: true}, nil, OK, ""},
	}
	for i, c := range cases {
		cs := ClaudeChecks(c.ci, c.err)
		last := cs[len(cs)-1]
		if last.Status != c.want || last.Fix != c.fix {
			t.Errorf("%d: %+v", i, cs)
		}
	}
}

func TestPrint(t *testing.T) {
	cs := []Check{{OK, "a", "fine", ""}, {Fail, "bb", "bad", "do it"}}
	var b bytes.Buffer
	Print(&b, cs)
	want := "  ok    a   fine\n  FAIL  bb  bad\n        fix: do it\n"
	if b.String() != want {
		t.Fatalf("%q", b.String())
	}
	if !Failed(cs) || Failed(cs[:1]) {
		t.Fatal("Failed")
	}
}
