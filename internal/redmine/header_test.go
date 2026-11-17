package redmine

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fakeKey = "fake-key-0123456789abcdef"

func writeHeader(t *testing.T, content string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "header")
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod fixture: %v", err)
	}
	return path
}

func TestLoadHeaderFile(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		mode      os.FileMode
		wantName  string
		wantErr   bool
		wantInsec bool
	}{
		{name: "valid with newline", content: "X-Redmine-API-Key: " + fakeKey + "\n", mode: 0o600, wantName: "X-Redmine-API-Key"},
		{name: "valid without newline", content: "X-Redmine-API-Key:" + fakeKey, mode: 0o600, wantName: "X-Redmine-API-Key"},
		{name: "read only owner", content: "X-Redmine-API-Key: " + fakeKey, mode: 0o400, wantName: "X-Redmine-API-Key"},
		{name: "group readable", content: "X-Redmine-API-Key: " + fakeKey, mode: 0o640, wantErr: true, wantInsec: true},
		{name: "world readable", content: "X-Redmine-API-Key: " + fakeKey, mode: 0o644, wantErr: true, wantInsec: true},
		{name: "two lines", content: "X-Redmine-API-Key: " + fakeKey + "\nOther: x\n", mode: 0o600, wantErr: true},
		{name: "empty file", content: "", mode: 0o600, wantErr: true},
		{name: "only the key", content: fakeKey + "\n", mode: 0o600, wantName: "X-Redmine-API-Key"},
		{name: "only the key, no newline", content: fakeKey, mode: 0o600, wantName: "X-Redmine-API-Key"},
		{name: "key with spaces is not a key", content: "not a key", mode: 0o600, wantErr: true},
		{name: "custom header name", content: "X-Other: " + fakeKey, mode: 0o600, wantName: "X-Other"},
		{name: "empty value", content: "X-Redmine-API-Key:   ", mode: 0o600, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, err := LoadHeaderFile(writeHeader(t, tt.content, tt.mode))
			if (err != nil) != tt.wantErr {
				t.Fatalf("LoadHeaderFile() error = %v, wantErr %v", err, tt.wantErr)
			}
			if errors.Is(err, ErrInsecurePermissions) != tt.wantInsec {
				t.Errorf("ErrInsecurePermissions = %v, want %v", !tt.wantInsec, tt.wantInsec)
			}
			if err != nil {
				if strings.Contains(err.Error(), fakeKey) {
					t.Errorf("error leaks the key: %v", err)
				}
				return
			}
			if h.Name() != tt.wantName {
				t.Errorf("Name() = %q, want %q", h.Name(), tt.wantName)
			}
		})
	}
}

func TestLoadHeaderFileValueIsTheKey(t *testing.T) {
	for _, content := range []string{fakeKey, "X-Redmine-API-Key: " + fakeKey} {
		t.Run(strings.Fields(content)[len(strings.Fields(content))-1][:4], func(t *testing.T) {
			h, err := LoadHeaderFile(writeHeader(t, content, 0o600))
			if err != nil {
				t.Fatalf("LoadHeaderFile() error = %v", err)
			}
			if h.value != fakeKey {
				t.Errorf("the loaded value is not the key")
			}
		})
	}
}

func TestLoadHeaderFileMissing(t *testing.T) {
	if _, err := LoadHeaderFile(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("expected an error for a missing file")
	}
}

func TestHeaderNeverPrintsValue(t *testing.T) {
	h, err := NewHeader("X-Redmine-API-Key", fakeKey)
	if err != nil {
		t.Fatalf("NewHeader: %v", err)
	}
	for name, got := range map[string]string{
		"String":   h.String(),
		"GoString": h.GoString(),
	} {
		t.Run(name, func(t *testing.T) {
			if strings.Contains(got, fakeKey) {
				t.Errorf("%s leaks the key: %s", name, got)
			}
		})
	}
}
