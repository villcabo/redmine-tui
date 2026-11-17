package main

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/villcabo/redmine-tui/internal/config"
	"github.com/villcabo/redmine-tui/internal/redmine"
)

type harness struct {
	out, err bytes.Buffer
	d        deps
	launched *launchCall
}

type launchCall struct {
	cfg     config.Config
	startID int
}

func newHarness(t *testing.T, env map[string]string, files map[string]string, stdin string) *harness {
	t.Helper()
	h := &harness{}
	h.d = deps{
		stdin:  strings.NewReader(stdin),
		stdout: &h.out,
		stderr: &h.err,
		getenv: func(k string) string { return env[k] },
		home:   t.TempDir(),
		readFile: func(p string) ([]byte, error) {
			if c, ok := files[p]; ok {
				return []byte(c), nil
			}
			return nil, fs.ErrNotExist
		},
		readSecret: func() (string, error) { return "", errNoTerminal },
		launch: func(cfg config.Config, _ *redmine.Client, startID int, _ bool) error {
			h.launched = &launchCall{cfg: cfg, startID: startID}
			return nil
		},
	}
	return h
}

func TestHelpIsTranslated(t *testing.T) {
	tests := []struct {
		name string
		args []string
		env  map[string]string
		want string
	}{
		{name: "english by default", args: []string{"-h"}, want: "browse and update Redmine issues"},
		{name: "spanish by flag", args: []string{"--lang", "es", "-h"}, want: "navega y actualiza issues de Redmine"},
		{name: "spanish by flag=value", args: []string{"--lang=es", "--help"}, want: "navega y actualiza issues de Redmine"},
		{name: "spanish by env", args: []string{"-h"}, env: map[string]string{"REDMINE_TUI_LANG": "es"}, want: "navega y actualiza issues de Redmine"},
		{name: "spanish by locale", args: []string{"-h"}, env: map[string]string{"LANG": "es_BO.UTF-8"}, want: "navega y actualiza issues de Redmine"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, tt.env, nil, "")
			if code := run(tt.args, h.d); code != 0 {
				t.Fatalf("exit code %d, stderr %q", code, h.err.String())
			}
			if !strings.Contains(h.out.String(), tt.want) {
				t.Errorf("help lacks %q:\n%s", tt.want, h.out.String())
			}
			for _, flag := range []string{"--read-only", "--lang", "--version", "REDMINE_URL", "REDMINE_READ_ONLY", "REDMINE_TUI_LANG"} {
				if !strings.Contains(h.out.String(), flag) {
					t.Errorf("help does not mention %s", flag)
				}
			}
		})
	}
}

func TestVersion(t *testing.T) {
	old := version
	version = "1.2.3"
	t.Cleanup(func() { version = old })
	h := newHarness(t, nil, nil, "")
	if code := run([]string{"--version"}, h.d); code != 0 {
		t.Fatalf("exit code %d", code)
	}
	if got := h.out.String(); !strings.HasPrefix(got, "redmine-tui 1.2.3") {
		t.Errorf("version output = %q", got)
	}
}

func TestMissingURLExplainsHowToConfigure(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want []string
	}{
		{name: "english", want: []string{"the Redmine URL is not set", "REDMINE_URL", "--url", "config.toml"}},
		{name: "spanish", env: map[string]string{"REDMINE_TUI_LANG": "es"}, want: []string{"falta la URL de Redmine", "REDMINE_URL", "--url", "config.toml"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, tt.env, nil, "")
			if code := run(nil, h.d); code != 1 {
				t.Fatalf("exit code %d, want 1", code)
			}
			for _, w := range tt.want {
				if !strings.Contains(h.err.String(), w) {
					t.Errorf("error lacks %q: %s", w, h.err.String())
				}
			}
			if h.launched != nil {
				t.Error("the TUI must not start without a URL")
			}
		})
	}
}

// keyFile writes a key file with the given mode and returns its path.
func keyFile(t *testing.T, content string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(p, []byte(content), mode); err != nil {
		t.Fatalf("write key: %v", err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatalf("chmod key: %v", err)
	}
	return p
}

func TestRunLaunchesWithResolvedConfig(t *testing.T) {
	key := keyFile(t, "fake-key-0123456789abcdef\n", 0o600)
	env := map[string]string{"REDMINE_URL": "https://redmine.example.com", "REDMINE_HEADER_FILE": key}
	h := newHarness(t, env, nil, "")
	if code := run([]string{"--read-only", "--lang", "es", "#42"}, h.d); code != 0 {
		t.Fatalf("exit code %d, stderr %q", code, h.err.String())
	}
	if h.launched == nil {
		t.Fatal("the TUI was not launched")
	}
	c := h.launched.cfg
	if h.launched.startID != 42 || !c.ReadOnly || c.Language != "es" || c.BaseURL != "https://redmine.example.com" {
		t.Errorf("launched with %+v, id %d", c, h.launched.startID)
	}
}

func TestRunRefusesAKeyFileOthersCanRead(t *testing.T) {
	key := keyFile(t, "fake-key-0123456789abcdef\n", 0o644)
	env := map[string]string{"REDMINE_URL": "https://redmine.example.com", "REDMINE_HEADER_FILE": key}
	h := newHarness(t, env, nil, "")
	if code := run(nil, h.d); code != 1 {
		t.Fatalf("exit code %d, want 1", code)
	}
	if !strings.Contains(h.err.String(), "chmod 600") {
		t.Errorf("the error should say how to fix it: %s", h.err.String())
	}
	if strings.Contains(h.err.String(), "fake-key") {
		t.Error("the error leaks the key")
	}
	if h.launched != nil {
		t.Error("the TUI must not start with an insecure key file")
	}
}

func TestRunBadArguments(t *testing.T) {
	tests := []struct {
		name string
		args []string
		code int
	}{
		{name: "unknown flag", args: []string{"--nope"}, code: 2},
		{name: "not a number", args: []string{"abc"}, code: 1},
		{name: "two ids", args: []string{"1", "2"}, code: 1},
		{name: "zero", args: []string{"0"}, code: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, map[string]string{"REDMINE_URL": "https://x.example.com"}, nil, "")
			if code := run(tt.args, h.d); code != tt.code {
				t.Errorf("exit code %d, want %d (stderr %q)", code, tt.code, h.err.String())
			}
			if h.launched != nil {
				t.Error("the TUI must not start")
			}
		})
	}
}

func TestInit(t *testing.T) {
	tests := []struct {
		name       string
		stdin      string
		wantConfig []string
		wantOut    []string
	}{
		{name: "url and language", stdin: "https://redmine.example.com/\nes\nn\n",
			wantConfig: []string{`url = "https://redmine.example.com"`, `language = "es"`},
			wantOut:    []string{"redmine-tui setup", "Config written to", "umask 077", "read -rs"}},
		{name: "invalid url is asked again, language defaults", stdin: "nope\nhttp://localhost:3000\n\nn\n",
			wantConfig: []string{`url = "http://localhost:3000"`, `language = "en"`},
			wantOut:    []string{"not a valid http(s) URL"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, map[string]string{"XDG_CONFIG_HOME": t.TempDir()}, nil, tt.stdin)
			if code := run([]string{"init"}, h.d); code != 0 {
				t.Fatalf("exit code %d, stderr %q", code, h.err.String())
			}
			path := config.DefaultPath(h.d.getenv, h.d.home)
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("config not written: %v", err)
			}
			for _, w := range tt.wantConfig {
				if !strings.Contains(string(raw), w) {
					t.Errorf("config lacks %q:\n%s", w, raw)
				}
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatalf("stat: %v", err)
			}
			if info.Mode().Perm() != 0o600 {
				t.Errorf("config mode = %o, want 600", info.Mode().Perm())
			}
			for _, w := range tt.wantOut {
				if !strings.Contains(h.out.String(), w) {
					t.Errorf("output lacks %q:\n%s", w, h.out.String())
				}
			}
		})
	}
}

func TestInitDoesNotOverwriteWithoutConfirmation(t *testing.T) {
	xdg := t.TempDir()
	env := map[string]string{"XDG_CONFIG_HOME": xdg}
	path := filepath.Join(xdg, "redmine-tui", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("url = \"https://keep.example.com\"\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	h := newHarness(t, env, nil, "\n") // default answer: no
	h.d.readFile = os.ReadFile
	if code := run([]string{"init"}, h.d); code != 0 {
		t.Fatalf("exit code %d", code)
	}
	raw, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(raw), "keep.example.com") {
		t.Errorf("the existing config was changed: %s %v", raw, err)
	}
}

func TestInitKeyWithHiddenInput(t *testing.T) {
	xdg := t.TempDir()
	h := newHarness(t, map[string]string{"XDG_CONFIG_HOME": xdg}, nil, "https://redmine.example.com\nen\ny\n")
	h.d.readSecret = func() (string, error) { return "  fake-key-0123456789abcdef \n", nil }
	if code := run([]string{"init"}, h.d); code != 0 {
		t.Fatalf("exit code %d, stderr %q", code, h.err.String())
	}
	keyPath := filepath.Join(h.d.home, ".config/redmine/api-key-header")
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("key not written: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("key mode = %o, want 600", info.Mode().Perm())
	}
	if _, err := redmine.LoadHeaderFile(keyPath); err != nil {
		t.Errorf("the saved key file must load back: %v", err)
	}
	if strings.Contains(h.out.String(), "fake-key") || strings.Contains(h.err.String(), "fake-key") {
		t.Error("the key must never be echoed")
	}
}

func TestInitKeyWithoutTerminalFallsBackToInstructions(t *testing.T) {
	h := newHarness(t, map[string]string{"XDG_CONFIG_HOME": t.TempDir()}, nil, "https://redmine.example.com\nen\ny\n")
	if code := run([]string{"init"}, h.d); code != 0 {
		t.Fatalf("exit code %d, stderr %q", code, h.err.String())
	}
	if !strings.Contains(h.out.String(), "Hidden input needs a terminal") {
		t.Errorf("output: %s", h.out.String())
	}
	if _, err := os.Stat(filepath.Join(h.d.home, ".config/redmine/api-key-header")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("no key file may be written without a key, stat err = %v", err)
	}
}

func TestScanLang(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "separate", args: []string{"--lang", "es"}, want: "es"},
		{name: "equals", args: []string{"-lang=en"}, want: "en"},
		{name: "absent", args: []string{"-h"}, want: ""},
		{name: "dangling", args: []string{"--lang"}, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := scanLang(tt.args); got != tt.want {
				t.Errorf("scanLang() = %q, want %q", got, tt.want)
			}
		})
	}
}
