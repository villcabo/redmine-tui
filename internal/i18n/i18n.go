// Package i18n holds the English and Spanish message catalogs and the helpers that
// pick a language and format dates for it.
package i18n

import (
	"fmt"
	"strings"
	"time"
)

// Lang is a supported interface language.
type Lang string

// Supported languages.
const (
	EN Lang = "en"
	ES Lang = "es"
)

// Parse converts a user supplied code such as "es" or "EN" into a Lang.
func Parse(s string) (Lang, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "en":
		return EN, true
	case "es":
		return ES, true
	default:
		return "", false
	}
}

// FromLocale maps a POSIX locale such as "es_BO.UTF-8" to a Lang. Anything that
// does not start with "es" is English.
func FromLocale(locale string) Lang {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(locale)), "es") {
		return ES
	}
	return EN
}

// Detect reads LC_ALL, LC_MESSAGES and LANG, in that order of priority.
func Detect(getenv func(string) string) Lang {
	for _, name := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := strings.TrimSpace(getenv(name)); v != "" {
			return FromLocale(v)
		}
	}
	return EN
}

// Translator renders catalog messages in one language.
type Translator struct{ lang Lang }

// New returns a Translator for lang; an unknown language falls back to English.
func New(lang Lang) Translator {
	if lang != ES {
		lang = EN
	}
	return Translator{lang: lang}
}

// Lang returns the language of the translator.
func (t Translator) Lang() Lang { return t.lang }

// T formats the message for key with args. A key missing from the catalog is
// returned as is, which makes the gap visible instead of silent.
func (t Translator) T(key string, args ...any) string {
	cat := catalogEN
	if t.lang == ES {
		cat = catalogES
	}
	msg, ok := cat[key]
	if !ok {
		return key
	}
	if len(args) == 0 {
		return msg
	}
	return fmt.Sprintf(msg, args...)
}

// DateTime formats a timestamp in the local zone using the language's usual layout.
func (t Translator) DateTime(ts time.Time) string {
	if ts.IsZero() {
		return "-"
	}
	return ts.Local().Format(t.T("layout.datetime"))
}

// Date formats an ISO date (YYYY-MM-DD), returning unparseable input unchanged.
func (t Translator) Date(iso *string) string {
	if iso == nil || strings.TrimSpace(*iso) == "" {
		return "-"
	}
	d, err := time.Parse("2006-01-02", *iso)
	if err != nil {
		return *iso
	}
	return d.Format(t.T("layout.date"))
}

// Error is an error whose text lives in the catalogs, so it can be shown in the
// user's language. Error() renders English; use Translator.Err for another language.
type Error struct {
	Key   string
	Args  []any
	Cause error
}

// Error implements error with the English text.
func (e *Error) Error() string { return New(EN).Err(e) }

// Unwrap exposes the wrapped cause to errors.Is and errors.As.
func (e *Error) Unwrap() error { return e.Cause }

// Errorf returns an Error for key without a cause.
func Errorf(key string, args ...any) error { return &Error{Key: key, Args: args} }

// Wrap returns an Error for key that wraps cause.
func Wrap(cause error, key string, args ...any) error {
	return &Error{Key: key, Args: args, Cause: cause}
}

// Err renders err and its chain of catalog errors in the translator's language.
// Errors that are not catalog errors are printed as they are.
func (t Translator) Err(err error) string {
	if err == nil {
		return ""
	}
	// Only the outermost error is inspected: a foreign wrapper around a catalog
	// error has its own text, and unwrapping it would silently drop that text.
	ie, ok := err.(*Error) //nolint:errorlint
	if !ok {
		return err.Error()
	}
	head := t.T(ie.Key, ie.Args...)
	if ie.Cause == nil {
		return head
	}
	return head + ": " + t.Err(ie.Cause)
}
