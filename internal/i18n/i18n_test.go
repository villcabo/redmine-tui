package i18n

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestCatalogsHaveTheSameKeys(t *testing.T) {
	var onlyEN, onlyES []string
	for k := range catalogEN {
		if _, ok := catalogES[k]; !ok {
			onlyEN = append(onlyEN, k)
		}
	}
	for k := range catalogES {
		if _, ok := catalogEN[k]; !ok {
			onlyES = append(onlyES, k)
		}
	}
	sort.Strings(onlyEN)
	sort.Strings(onlyES)
	if len(onlyEN) > 0 || len(onlyES) > 0 {
		t.Errorf("catalog mismatch: only in en %v, only in es %v", onlyEN, onlyES)
	}
}

var verbPattern = regexp.MustCompile(`%(\[\d+\])?[-+# 0]*\d*[a-zA-Z%]`)

// verbs returns the sorted printf verbs of a message, ignoring literal %%.
func verbs(s string) []string {
	var out []string
	for _, v := range verbPattern.FindAllString(s, -1) {
		if v == "%%" {
			continue
		}
		out = append(out, v[len(v)-1:])
	}
	sort.Strings(out)
	return out
}

func TestCatalogsUseTheSameVerbs(t *testing.T) {
	for k, en := range catalogEN {
		es := catalogES[k]
		if got, want := strings.Join(verbs(es), ""), strings.Join(verbs(en), ""); got != want {
			t.Errorf("%s: es uses verbs %q, en uses %q", k, got, want)
		}
	}
}

func TestEveryKeyUsedInCodeExists(t *testing.T) {
	root := filepath.Join("..", "..")
	call := regexp.MustCompile(`(?:\.t\(|\.T\(|Errorf\(|i18n\.Wrap\([^"]*,\s*)"([a-z0-9_.]+)"`)
	var missing []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range call.FindAllStringSubmatch(string(src), -1) {
			if strings.HasSuffix(m[1], ".") && hasPrefixKey(m[1]) {
				continue // a key built at run time, such as "rel." + type
			}
			if _, ok := catalogEN[m[1]]; !ok {
				missing = append(missing, path+": "+m[1])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(missing) > 0 {
		t.Errorf("keys used in code but missing from the catalogs:\n%s", strings.Join(missing, "\n"))
	}
}

func TestParseAndDetect(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want Lang
	}{
		{name: "empty is English", want: EN},
		{name: "LANG es", env: map[string]string{"LANG": "es_BO.UTF-8"}, want: ES},
		{name: "LC_MESSAGES beats LANG", env: map[string]string{"LANG": "es_ES", "LC_MESSAGES": "en_US"}, want: EN},
		{name: "LC_ALL beats all", env: map[string]string{"LC_ALL": "es_AR", "LC_MESSAGES": "en_US", "LANG": "en_US"}, want: ES},
		{name: "other language is English", env: map[string]string{"LANG": "fr_FR.UTF-8"}, want: EN},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Detect(func(k string) string { return tt.env[k] }); got != tt.want {
				t.Errorf("Detect() = %q, want %q", got, tt.want)
			}
		})
	}
	if l, ok := Parse(" ES "); !ok || l != ES {
		t.Errorf("Parse(\" ES \") = %q, %v", l, ok)
	}
	if _, ok := Parse("fr"); ok {
		t.Error("Parse(fr) should fail")
	}
}

func TestTranslatorAndErrors(t *testing.T) {
	es, en := New(ES), New(EN)
	if got := es.T("time.yesterday"); got != "ayer" {
		t.Errorf("es yesterday = %q", got)
	}
	if got := en.T("time.yesterday"); got != "yesterday" {
		t.Errorf("en yesterday = %q", got)
	}
	if got := en.T("no.such.key"); got != "no.such.key" {
		t.Errorf("a missing key must show itself, got %q", got)
	}

	cause := Errorf("err.read_only")
	err := Wrap(cause, "err.update_issue", 7)
	if got, want := en.Err(err), "update issue #7: read-only mode: writes are disabled"; got != want {
		t.Errorf("en.Err() = %q, want %q", got, want)
	}
	if got, want := es.Err(err), "actualizar el issue #7: modo solo lectura: las escrituras están desactivadas"; got != want {
		t.Errorf("es.Err() = %q, want %q", got, want)
	}
	if !errors.Is(err, cause) {
		t.Error("errors.Is must see the wrapped catalog error")
	}
	if got := en.Err(errors.New("plain")); got != "plain" {
		t.Errorf("a foreign error is printed as is, got %q", got)
	}
}

func TestDateFormats(t *testing.T) {
	ts := time.Date(2026, 11, 5, 14, 30, 0, 0, time.Local)
	iso := "2026-11-05"
	tests := []struct {
		name string
		tr   Translator
		want string
		got  func(Translator) string
	}{
		{name: "en datetime", tr: New(EN), want: "11/05/2026 2:30 PM", got: func(t Translator) string { return t.DateTime(ts) }},
		{name: "es datetime", tr: New(ES), want: "05/11/2026 14:30", got: func(t Translator) string { return t.DateTime(ts) }},
		{name: "en date", tr: New(EN), want: "11/05/2026", got: func(t Translator) string { return t.Date(&iso) }},
		{name: "es date", tr: New(ES), want: "05/11/2026", got: func(t Translator) string { return t.Date(&iso) }},
		{name: "zero time", tr: New(EN), want: "-", got: func(t Translator) string { return t.DateTime(time.Time{}) }},
		{name: "nil date", tr: New(ES), want: "-", got: func(t Translator) string { return t.Date(nil) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.got(tt.tr); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func hasPrefixKey(prefix string) bool {
	for k := range catalogEN {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	return false
}
