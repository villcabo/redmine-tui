package redmine

import (
	"io"
	"os"
	"strings"

	"github.com/villcabo/redmine-tui/internal/i18n"
)

// maxHeaderFileSize bounds how much of the key file is read; a real one is a single short line.
const maxHeaderFileSize = 8 << 10

// DefaultKeyHeader is the header assumed when the key file holds only the key.
const DefaultKeyHeader = "X-Redmine-API-Key"

// ErrInsecurePermissions is returned when the key file is accessible by group or others.
var ErrInsecurePermissions = i18n.Errorf("err.insecure_permissions")

// Header is an HTTP header that carries the API key. Its value is never
// exposed through String, GoString or error messages.
type Header struct {
	name  string
	value string
}

// String returns the header with its value masked, so it is safe to log or print.
func (h Header) String() string { return h.name + ": ***" }

// GoString masks the value for %#v as well.
func (h Header) GoString() string { return h.String() }

// Name returns the header field name.
func (h Header) Name() string { return h.name }

// IsZero reports whether no header was loaded.
func (h Header) IsZero() bool { return h.name == "" }

// redact removes the header value from s.
func (h Header) redact(s string) string {
	if h.value == "" {
		return s
	}
	return strings.ReplaceAll(s, h.value, "***")
}

// NewHeader builds a Header from a name and a value.
func NewHeader(name, value string) (Header, error) {
	name = strings.TrimSpace(name)
	value = strings.TrimSpace(value)
	if name == "" || value == "" {
		return Header{}, i18n.Errorf("err.header_empty")
	}
	if strings.ContainsAny(name+value, "\r\n") {
		return Header{}, i18n.Errorf("err.header_newline")
	}
	return Header{name: name, value: value}, nil
}

// LoadHeaderFile reads the API key file. The file holds either a single
// "Name: value" line or only the key, which is sent as X-Redmine-API-Key.
// It refuses files readable or writable by group or others.
// Error messages never include the file contents.
func LoadHeaderFile(path string) (Header, error) {
	f, err := os.Open(path)
	if err != nil {
		return Header{}, i18n.Wrap(err, "err.key_open", path)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return Header{}, i18n.Wrap(err, "err.key_stat", path)
	}
	if !info.Mode().IsRegular() {
		return Header{}, i18n.Errorf("err.key_not_regular", path)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return Header{}, i18n.Wrap(ErrInsecurePermissions, "err.key_perms", path, info.Mode().Perm(), path)
	}

	raw, err := io.ReadAll(io.LimitReader(f, maxHeaderFileSize))
	if err != nil {
		return Header{}, i18n.Wrap(err, "err.key_read", path)
	}

	var lines []string
	for _, l := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) != 1 {
		return Header{}, i18n.Errorf("err.key_lines", path)
	}

	name, value, hasName := strings.Cut(lines[0], ":")
	if !hasName {
		name, value = DefaultKeyHeader, lines[0]
		if strings.ContainsAny(strings.TrimSpace(value), " \t") {
			return Header{}, i18n.Errorf("err.key_format", path)
		}
	}
	h, err := NewHeader(name, value)
	if err != nil {
		return Header{}, i18n.Wrap(err, "err.key_invalid", path)
	}
	return h, nil
}
