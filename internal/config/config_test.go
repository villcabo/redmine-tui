package config

import (
	"errors"
	"io/fs"
	"os"
	"testing"

	"github.com/villcabo/redmine-tui/internal/i18n"
)

const home = "/home/u"

func sources(env map[string]string, files map[string]string, fl Flags) Sources {
	return Sources{
		Flags:  fl,
		Getenv: func(k string) string { return env[k] },
		Home:   home,
		ReadFile: func(p string) ([]byte, error) {
			if c, ok := files[p]; ok {
				return []byte(c), nil
			}
			return nil, fs.ErrNotExist
		},
	}
}

func boolp(b bool) *bool { return &b }

func TestResolve(t *testing.T) {
	cfgPath := home + "/.config/redmine-tui/config.toml"
	tests := []struct {
		name    string
		env     map[string]string
		files   map[string]string
		flags   Flags
		want    Config
		wantErr string
	}{
		{
			name: "url from env, defaults elsewhere",
			env:  map[string]string{"REDMINE_URL": "https://redmine.example.com/"},
			want: Config{BaseURL: "https://redmine.example.com", KeyFile: home + "/.config/redmine/api-key-header", Language: i18n.EN, Editor: "vi"},
		},
		{
			name:    "url missing everywhere",
			wantErr: "err.url_missing",
		},
		{
			name:  "url from config file",
			files: map[string]string{cfgPath: "url = \"https://file.example.com\"\nlanguage = \"es\"\nread_only = true\neditor = \"nano -w\"\nkey_file = \"~/k\"\n"},
			want:  Config{BaseURL: "https://file.example.com", KeyFile: home + "/k", Language: i18n.ES, ReadOnly: true, Editor: "nano -w"},
		},
		{
			name:  "env beats config",
			env:   map[string]string{"REDMINE_URL": "https://env.example.com", "REDMINE_TUI_LANG": "en", "REDMINE_READ_ONLY": "0", "REDMINE_HEADER_FILE": "/etc/env-key"},
			files: map[string]string{cfgPath: "url = \"https://file.example.com\"\nlanguage = \"es\"\nread_only = true\nkey_file = \"/etc/file-key\"\n"},
			want:  Config{BaseURL: "https://env.example.com", KeyFile: "/etc/env-key", Language: i18n.EN, Editor: "vi"},
		},
		{
			name:  "flags beat env",
			env:   map[string]string{"REDMINE_URL": "https://env.example.com", "REDMINE_TUI_LANG": "en", "REDMINE_READ_ONLY": "0"},
			flags: Flags{URL: "https://flag.example.com", Lang: "es", ReadOnly: boolp(true), KeyFile: "/etc/flag-key"},
			want:  Config{BaseURL: "https://flag.example.com", KeyFile: "/etc/flag-key", Language: i18n.ES, ReadOnly: true, Editor: "vi"},
		},
		{
			name:  "read-only flag can turn it off",
			env:   map[string]string{"REDMINE_URL": "https://x.example.com", "REDMINE_READ_ONLY": "1"},
			flags: Flags{ReadOnly: boolp(false)},
			want:  Config{BaseURL: "https://x.example.com", KeyFile: home + "/.config/redmine/api-key-header", Language: i18n.EN, Editor: "vi"},
		},
		{
			name: "api key file alias",
			env:  map[string]string{"REDMINE_URL": "https://x.example.com", "REDMINE_API_KEY_FILE": "~/alias"},
			want: Config{BaseURL: "https://x.example.com", KeyFile: home + "/alias", Language: i18n.EN, Editor: "vi"},
		},
		{
			name: "header file wins over the alias",
			env:  map[string]string{"REDMINE_URL": "https://x.example.com", "REDMINE_API_KEY_FILE": "/alias", "REDMINE_HEADER_FILE": "/header"},
			want: Config{BaseURL: "https://x.example.com", KeyFile: "/header", Language: i18n.EN, Editor: "vi"},
		},
		{
			name: "locale picks Spanish",
			env:  map[string]string{"REDMINE_URL": "https://x.example.com", "LANG": "es_BO.UTF-8"},
			want: Config{BaseURL: "https://x.example.com", KeyFile: home + "/.config/redmine/api-key-header", Language: i18n.ES, Editor: "vi"},
		},
		{
			name: "LC_ALL beats LANG",
			env:  map[string]string{"REDMINE_URL": "https://x.example.com", "LANG": "es_BO.UTF-8", "LC_ALL": "en_US.UTF-8"},
			want: Config{BaseURL: "https://x.example.com", KeyFile: home + "/.config/redmine/api-key-header", Language: i18n.EN, Editor: "vi"},
		},
		{
			name:  "config language beats the locale",
			env:   map[string]string{"LANG": "es_BO.UTF-8"},
			files: map[string]string{cfgPath: "url = \"https://x.example.com\"\nlanguage = \"en\"\n"},
			want:  Config{BaseURL: "https://x.example.com", KeyFile: home + "/.config/redmine/api-key-header", Language: i18n.EN, Editor: "vi"},
		},
		{
			name: "editor chain",
			env:  map[string]string{"REDMINE_URL": "https://x.example.com", "EDITOR": "nvim", "VISUAL": "code"},
			want: Config{BaseURL: "https://x.example.com", KeyFile: home + "/.config/redmine/api-key-header", Language: i18n.EN, Editor: "nvim"},
		},
		{
			name: "visual when no editor",
			env:  map[string]string{"REDMINE_URL": "https://x.example.com", "VISUAL": "code -w"},
			want: Config{BaseURL: "https://x.example.com", KeyFile: home + "/.config/redmine/api-key-header", Language: i18n.EN, Editor: "code -w"},
		},
		{
			name: "XDG config home",
			env:  map[string]string{"XDG_CONFIG_HOME": "/xdg"},
			files: map[string]string{
				"/xdg/redmine-tui/config.toml": "url = \"https://xdg.example.com\"\n",
			},
			want: Config{BaseURL: "https://xdg.example.com", KeyFile: home + "/.config/redmine/api-key-header", Language: i18n.EN, Editor: "vi"},
		},
		{
			name:    "bad scheme",
			env:     map[string]string{"REDMINE_URL": "ftp://x"},
			wantErr: "err.url_invalid",
		},
		{
			name:    "no host",
			env:     map[string]string{"REDMINE_URL": "https://"},
			wantErr: "err.url_invalid",
		},
		{
			name:    "bad language in flag",
			env:     map[string]string{"REDMINE_URL": "https://x.example.com"},
			flags:   Flags{Lang: "fr"},
			wantErr: "err.lang_invalid",
		},
		{
			name:    "bad read-only env",
			env:     map[string]string{"REDMINE_URL": "https://x.example.com", "REDMINE_READ_ONLY": "maybe"},
			wantErr: "err.read_only_env",
		},
		{
			name:    "broken config file",
			files:   map[string]string{cfgPath: "url = \n= oops"},
			wantErr: "err.config_file",
		},
		{
			name:    "explicit config path that does not exist",
			env:     map[string]string{"REDMINE_URL": "https://x.example.com"},
			flags:   Flags{ConfigPath: "/nowhere.toml"},
			wantErr: "err.config_read",
		},
		{
			name:    "unknown status color",
			files:   map[string]string{cfgPath: "url = \"https://x.example.com\"\n[status_colors]\n\"Doing\" = \"purple\"\n"},
			wantErr: "err.config_color",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Resolve(sources(tt.env, tt.files, tt.flags))
			if tt.wantErr != "" {
				var ie *i18n.Error
				if !errors.As(err, &ie) || ie.Key != tt.wantErr {
					t.Fatalf("Resolve() error = %v, want key %s", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if got.BaseURL != tt.want.BaseURL || got.KeyFile != tt.want.KeyFile || got.Language != tt.want.Language ||
				got.ReadOnly != tt.want.ReadOnly || got.Editor != tt.want.Editor {
				t.Errorf("Resolve() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestResolveStatusColors(t *testing.T) {
	files := map[string]string{home + "/.config/redmine-tui/config.toml": `url = "https://x.example.com"
[status_colors]
"In Progress" = "magenta"   # a comment
Blocked = 'red'
`}
	got, err := Resolve(sources(nil, files, Flags{}))
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got.StatusColors["in progress"] != "magenta" || got.StatusColors["blocked"] != "red" {
		t.Errorf("StatusColors = %v", got.StatusColors)
	}
}

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		check   func(t *testing.T, f File)
		wantErr string
	}{
		{name: "empty", in: "", check: func(t *testing.T, f File) {
			if f.URL != "" || f.ReadOnly != nil {
				t.Errorf("unexpected %+v", f)
			}
		}},
		{name: "comments and blank lines", in: "# header\n\nurl = \"https://a.example.com\" # trailing\n", check: func(t *testing.T, f File) {
			if f.URL != "https://a.example.com" {
				t.Errorf("URL = %q", f.URL)
			}
		}},
		{name: "hash inside a string is kept", in: "editor = \"vim # not a comment\"\n", check: func(t *testing.T, f File) {
			if f.Editor != "vim # not a comment" {
				t.Errorf("Editor = %q", f.Editor)
			}
		}},
		{name: "booleans", in: "read_only = true\n", check: func(t *testing.T, f File) {
			if f.ReadOnly == nil || !*f.ReadOnly {
				t.Errorf("ReadOnly = %v", f.ReadOnly)
			}
		}},
		{name: "escaped quote", in: `editor = "a \"b\""` + "\n", check: func(t *testing.T, f File) {
			if f.Editor != `a "b"` {
				t.Errorf("Editor = %q", f.Editor)
			}
		}},
		{name: "unknown key", in: "colour = \"x\"\n", wantErr: "err.config_key"},
		{name: "unknown section", in: "[server]\n", wantErr: "err.config_section"},
		{name: "no equals", in: "just words\n", wantErr: "err.config_syntax"},
		{name: "unquoted string", in: "url = https://x\n", wantErr: "err.config_value"},
		{name: "bad boolean", in: "read_only = \"yes\"\n", wantErr: "err.config_value"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := Parse(tt.in)
			if tt.wantErr != "" {
				var ie *i18n.Error
				if !errors.As(err, &ie) || ie.Key != tt.wantErr {
					t.Fatalf("Parse() error = %v, want key %s", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			tt.check(t, f)
		})
	}
}

func TestRenderRoundTrip(t *testing.T) {
	ro := true
	in := File{URL: "https://r.example.com", Language: "es", KeyFile: "~/k", Editor: "nano", ReadOnly: &ro}
	out, err := Parse(Render(in))
	if err != nil {
		t.Fatalf("Parse(Render()) error = %v", err)
	}
	if out.URL != in.URL || out.Language != in.Language || out.KeyFile != in.KeyFile || out.Editor != in.Editor || out.ReadOnly == nil || !*out.ReadOnly {
		t.Errorf("round trip = %+v, want %+v", out, in)
	}
}

func TestWriteSecretMode(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/sub/key"
	if err := WriteSecret(path, "X-Redmine-API-Key: k"); err != nil {
		t.Fatalf("WriteSecret() error = %v", err)
	}
	// An existing file with loose permissions must end up at 0600.
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if err := WriteSecret(path, "X-Redmine-API-Key: k2"); err != nil {
		t.Fatalf("WriteSecret() second call error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o, want 600", info.Mode().Perm())
	}
	di, err := os.Stat(dir + "/sub")
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if di.Mode().Perm() != 0o700 {
		t.Errorf("dir mode = %o, want 700", di.Mode().Perm())
	}
}
