// Package config resolves the runtime configuration from flags, the environment,
// the optional config file and the defaults, in that order of priority.
package config

import (
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/villcabo/redmine-tui/internal/i18n"
)

const (
	defaultKeyFile = ".config/redmine/api-key-header"
	defaultEditor  = "vi"
)

// StatusColorNames are the colors a status can be given in [status_colors].
var StatusColorNames = []string{"green", "blue", "amber", "magenta", "red", "gray"}

// Config is the fully resolved configuration.
type Config struct {
	BaseURL      string
	KeyFile      string
	Language     i18n.Lang
	ReadOnly     bool
	Editor       string
	StatusColors map[string]string // lower-cased status name -> color name
}

// Flags are the values given on the command line. Empty strings and a nil ReadOnly mean "not given".
type Flags struct {
	URL        string
	KeyFile    string
	Lang       string
	ConfigPath string
	ReadOnly   *bool
}

// Sources is everything Resolve reads, injected so it can be tested.
type Sources struct {
	Flags    Flags
	Getenv   func(string) string
	Home     string
	ReadFile func(string) ([]byte, error)
}

// DefaultPath is where the config file lives when no --config is given.
func DefaultPath(getenv func(string) string, home string) string {
	if x := strings.TrimSpace(getenv("XDG_CONFIG_HOME")); x != "" {
		return filepath.Join(x, "redmine-tui", "config.toml")
	}
	return filepath.Join(home, ".config", "redmine-tui", "config.toml")
}

// GuessLanguage picks a language without failing, for rendering errors that happen
// before the configuration is fully resolved: flag, then REDMINE_TUI_LANG, then the locale.
func GuessLanguage(flagLang string, getenv func(string) string) i18n.Lang {
	for _, v := range []string{flagLang, getenv("REDMINE_TUI_LANG")} {
		if l, ok := i18n.Parse(v); ok {
			return l
		}
	}
	return i18n.Detect(getenv)
}

// Resolve merges every source. A missing default config file is not an error; a
// missing file named with --config is.
func Resolve(src Sources) (Config, error) {
	path := DefaultPath(src.Getenv, src.Home)
	explicit := strings.TrimSpace(src.Flags.ConfigPath) != ""
	if explicit {
		path = expandHome(strings.TrimSpace(src.Flags.ConfigPath), src.Home)
	}
	var file File
	raw, err := src.ReadFile(path)
	switch {
	case err == nil:
		if file, err = Parse(string(raw)); err != nil {
			return Config{}, i18n.Wrap(err, "err.config_file", path)
		}
	case errors.Is(err, fs.ErrNotExist) && !explicit:
	default:
		return Config{}, i18n.Wrap(err, "err.config_read", path)
	}

	base := first(src.Flags.URL, src.Getenv("REDMINE_URL"), file.URL)
	if base == "" {
		return Config{}, i18n.Errorf("err.url_missing", path)
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return Config{}, i18n.Errorf("err.url_invalid")
	}

	keyFile := first(src.Flags.KeyFile, src.Getenv("REDMINE_HEADER_FILE"), src.Getenv("REDMINE_API_KEY_FILE"), file.KeyFile)
	if keyFile == "" {
		keyFile = filepath.Join(src.Home, defaultKeyFile)
	}

	lang, err := resolveLanguage(src, file)
	if err != nil {
		return Config{}, err
	}

	readOnly := file.ReadOnly != nil && *file.ReadOnly
	if v := strings.TrimSpace(src.Getenv("REDMINE_READ_ONLY")); v != "" {
		if readOnly, err = parseBool(v); err != nil {
			return Config{}, i18n.Errorf("err.read_only_env", v)
		}
	}
	if src.Flags.ReadOnly != nil {
		readOnly = *src.Flags.ReadOnly
	}

	colors := make(map[string]string, len(file.StatusColors))
	for name, color := range file.StatusColors {
		color = strings.ToLower(strings.TrimSpace(color))
		if !validColor(color) {
			return Config{}, i18n.Errorf("err.config_color", name, color, strings.Join(StatusColorNames, ", "))
		}
		colors[strings.ToLower(strings.TrimSpace(name))] = color
	}

	// A dedicated redmine-tui setting beats the generic EDITOR variable.
	editor := first(file.Editor, src.Getenv("EDITOR"), src.Getenv("VISUAL"), defaultEditor)

	return Config{
		BaseURL:      strings.TrimRight(base, "/"),
		KeyFile:      expandHome(keyFile, src.Home),
		Language:     lang,
		ReadOnly:     readOnly,
		Editor:       editor,
		StatusColors: colors,
	}, nil
}

func resolveLanguage(src Sources, file File) (i18n.Lang, error) {
	for _, c := range []struct{ source, value string }{
		{"--lang", src.Flags.Lang},
		{"REDMINE_TUI_LANG", src.Getenv("REDMINE_TUI_LANG")},
		{"language", file.Language},
	} {
		v := strings.TrimSpace(c.value)
		if v == "" {
			continue
		}
		l, ok := i18n.Parse(v)
		if !ok {
			return "", i18n.Errorf("err.lang_invalid", c.source, v)
		}
		return l, nil
	}
	return i18n.Detect(src.Getenv), nil
}

func validColor(c string) bool {
	for _, n := range StatusColorNames {
		if n == c {
			return true
		}
	}
	return false
}

// first returns the first value that is not blank, trimmed.
func first(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

func expandHome(p, home string) string {
	switch {
	case p == "~":
		return home
	case strings.HasPrefix(p, "~/"):
		return filepath.Join(home, p[2:])
	}
	return p
}

// Write stores text at path with mode 0600, creating the directory with mode 0700.
func Write(path, text string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return i18n.Wrap(err, "err.config_write", path)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		return i18n.Wrap(err, "err.config_write", path)
	}
	return nil
}

// WriteSecret stores line at path so that only the owner can read it: the directory is
// created with mode 0700 and the file is forced to 0600 even if it already existed.
func WriteSecret(path, line string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return i18n.Wrap(err, "err.config_write", path)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return i18n.Wrap(err, "err.config_write", path)
	}
	if err := f.Chmod(0o600); err != nil {
		return errors.Join(i18n.Wrap(err, "err.config_write", path), f.Close())
	}
	if _, err := f.WriteString(line + "\n"); err != nil {
		return errors.Join(i18n.Wrap(err, "err.config_write", path), f.Close())
	}
	if err := f.Close(); err != nil {
		return i18n.Wrap(err, "err.config_write", path)
	}
	return nil
}
