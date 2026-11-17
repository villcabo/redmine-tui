package config

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/villcabo/redmine-tui/internal/i18n"
)

// File is the content of config.toml. It supports the small TOML subset the tool needs:
// "key = value" lines with string, boolean values, comments, and one [status_colors] table.
type File struct {
	URL          string
	KeyFile      string
	Language     string
	Editor       string
	ReadOnly     *bool
	StatusColors map[string]string
}

// Parse reads the config file format. Unknown keys are an error, so a typo is not silently ignored.
func Parse(text string) (File, error) {
	var f File
	section := ""
	for i, line := range strings.Split(text, "\n") {
		n := i + 1
		line = strings.TrimSpace(stripComment(line))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			if line != "[status_colors]" {
				return File{}, i18n.Errorf("err.config_section", n, line)
			}
			section = "status_colors"
			if f.StatusColors == nil {
				f.StatusColors = map[string]string{}
			}
			continue
		}
		key, rawValue, ok := cutKey(line)
		if !ok {
			return File{}, i18n.Errorf("err.config_syntax", n)
		}
		if section == "status_colors" {
			v, err := parseString(rawValue)
			if err != nil {
				return File{}, i18n.Errorf("err.config_value", n, key)
			}
			f.StatusColors[key] = v
			continue
		}
		if err := f.set(n, key, rawValue); err != nil {
			return File{}, err
		}
	}
	return f, nil
}

func (f *File) set(n int, key, raw string) error {
	if key == "read_only" {
		b, err := parseBool(strings.TrimSpace(raw))
		if err != nil {
			return i18n.Errorf("err.config_value", n, key)
		}
		f.ReadOnly = &b
		return nil
	}
	v, err := parseString(raw)
	if err != nil {
		return i18n.Errorf("err.config_value", n, key)
	}
	switch key {
	case "url":
		f.URL = v
	case "key_file":
		f.KeyFile = v
	case "language":
		f.Language = v
	case "editor":
		f.Editor = v
	default:
		return i18n.Errorf("err.config_key", n, key)
	}
	return nil
}

// Render writes f back as config.toml text, omitting empty values.
func Render(f File) string {
	var b strings.Builder
	add := func(k, v string) {
		if v != "" {
			fmt.Fprintf(&b, "%s = %s\n", k, strconv.Quote(v))
		}
	}
	add("url", f.URL)
	add("language", f.Language)
	add("key_file", f.KeyFile)
	add("editor", f.Editor)
	if f.ReadOnly != nil {
		fmt.Fprintf(&b, "read_only = %t\n", *f.ReadOnly)
	}
	return b.String()
}

// cutKey splits `key = value`, where key may be a bare word or a quoted string.
func cutKey(line string) (key, value string, ok bool) {
	if strings.HasPrefix(line, `"`) {
		end := closingQuote(line)
		if end < 0 {
			return "", "", false
		}
		k, err := strconv.Unquote(line[:end+1])
		rest := strings.TrimSpace(line[end+1:])
		if err != nil || !strings.HasPrefix(rest, "=") {
			return "", "", false
		}
		return k, strings.TrimSpace(rest[1:]), true
	}
	k, v, found := strings.Cut(line, "=")
	k = strings.TrimSpace(k)
	if !found || k == "" {
		return "", "", false
	}
	return k, strings.TrimSpace(v), true
}

func closingQuote(s string) int {
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '"':
			return i
		}
	}
	return -1
}

func parseString(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, `'`) && strings.HasSuffix(raw, `'`) && len(raw) >= 2 {
		return raw[1 : len(raw)-1], nil
	}
	return strconv.Unquote(raw)
}

func parseBool(s string) (bool, error) {
	switch strings.ToLower(s) {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	}
	return false, fmt.Errorf("not a boolean: %q", s)
}

// stripComment removes a trailing "# ..." that is not inside a quoted string.
func stripComment(line string) string {
	var quote byte
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case quote != 0:
			if c == '\\' && quote == '"' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '#':
			return line[:i]
		}
	}
	return line
}
