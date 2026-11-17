// Command redmine-tui is a terminal browser and editor for Redmine issues.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"golang.org/x/term"

	"github.com/villcabo/redmine-tui/internal/config"
	"github.com/villcabo/redmine-tui/internal/i18n"
	"github.com/villcabo/redmine-tui/internal/redmine"
	"github.com/villcabo/redmine-tui/internal/tui"
)

// Set by the release build through -ldflags.
var (
	version = "dev"
	commit  = ""
	date    = ""
)

// deps is everything run touches outside the process, so it can be tested.
type deps struct {
	stdin      io.Reader
	stdout     io.Writer
	stderr     io.Writer
	getenv     func(string) string
	home       string
	readFile   func(string) ([]byte, error)
	readSecret func() (string, error)
	launch     func(cfg config.Config, client *redmine.Client, startID int, noColor bool) error
}

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", i18n.New(i18n.Detect(os.Getenv)).Err(i18n.Wrap(err, "err.home_dir")))
		os.Exit(1)
	}
	os.Exit(run(os.Args[1:], deps{
		stdin:      os.Stdin,
		stdout:     os.Stdout,
		stderr:     os.Stderr,
		getenv:     os.Getenv,
		home:       home,
		readFile:   os.ReadFile,
		readSecret: readSecretFromTerminal,
		launch:     launchTUI,
	}))
}

// run executes the command line and returns the process exit code.
func run(args []string, d deps) int {
	var (
		fl       config.Flags
		showVer  bool
		readOnly bool
	)
	fs := flag.NewFlagSet("redmine-tui", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&fl.URL, "url", "", "")
	fs.StringVar(&fl.KeyFile, "key-file", "", "")
	fs.StringVar(&fl.Lang, "lang", "", "")
	fs.StringVar(&fl.ConfigPath, "config", "", "")
	fs.BoolVar(&readOnly, "read-only", false, "")
	fs.BoolVar(&showVer, "version", false, "")

	guess := config.GuessLanguage(scanLang(args), d.getenv)
	tr := i18n.New(guess)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(d.stdout, tr.T("usage"))
			return 0
		}
		fmt.Fprint(d.stderr, tr.T("usage"))
		fmt.Fprintln(d.stderr, "Error:", tr.Err(i18n.Wrap(err, "err.bad_flag")))
		return 2
	}
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "read-only" {
			fl.ReadOnly = &readOnly
		}
	})

	if showVer {
		fmt.Fprintln(d.stdout, versionString())
		return 0
	}

	rest := fs.Args()
	if len(rest) > 0 && rest[0] == "init" {
		return finish(d, tr, runInit(d, tr, fl, rest[1:]))
	}

	startID, err := parseIssueID(rest)
	if err != nil {
		fmt.Fprint(d.stderr, tr.T("usage"))
		return finish(d, tr, err)
	}

	cfg, err := config.Resolve(config.Sources{Flags: fl, Getenv: d.getenv, Home: d.home, ReadFile: d.readFile})
	if err != nil {
		return finish(d, tr, err)
	}
	tr = i18n.New(cfg.Language)

	header, err := redmine.LoadHeaderFile(cfg.KeyFile)
	if err != nil {
		return finish(d, tr, i18n.Wrap(err, "err.key_file_inaccessible"))
	}
	var opts []redmine.Option
	if cfg.ReadOnly {
		opts = append(opts, redmine.WithReadOnly())
	}
	client := redmine.NewClient(cfg.BaseURL, header, nil, opts...)
	_, noColor := lookupEnv(d.getenv, "NO_COLOR")
	return finish(d, tr, d.launch(cfg, client, startID, noColor))
}

func lookupEnv(getenv func(string) string, key string) (string, bool) {
	v := getenv(key)
	return v, v != ""
}

// finish prints err in the user's language and maps it to an exit code.
func finish(d deps, tr i18n.Translator, err error) int {
	if err == nil {
		return 0
	}
	fmt.Fprintln(d.stderr, "Error:", tr.Err(err))
	return 1
}

func launchTUI(cfg config.Config, client *redmine.Client, startID int, noColor bool) error {
	m := tui.New(client, tui.Options{
		BaseURL:      cfg.BaseURL,
		StartID:      startID,
		NoColor:      noColor,
		ReadOnly:     cfg.ReadOnly,
		Lang:         cfg.Language,
		Editor:       cfg.Editor,
		StatusColors: cfg.StatusColors,
	})
	if _, err := tea.NewProgram(m).Run(); err != nil {
		return i18n.Wrap(err, "err.tui")
	}
	return nil
}

func readSecretFromTerminal() (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", errNoTerminal
	}
	b, err := term.ReadPassword(fd)
	return string(b), err
}

var errNoTerminal = errors.New("no terminal")

// scanLang finds --lang before the flag set is parsed, so -h can answer in that language.
func scanLang(args []string) string {
	for i, a := range args {
		a = strings.TrimLeft(a, "-")
		if v, ok := strings.CutPrefix(a, "lang="); ok {
			return v
		}
		if a == "lang" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func versionString() string {
	v := version
	if v == "dev" {
		if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			v = bi.Main.Version
		}
	}
	out := "redmine-tui " + v
	var extra []string
	if commit != "" {
		extra = append(extra, commit)
	}
	if date != "" {
		extra = append(extra, date)
	}
	if len(extra) > 0 {
		out += " (" + strings.Join(extra, ", ") + ")"
	}
	return out
}

// parseIssueID accepts no argument (0) or one issue number, optionally prefixed with "#".
func parseIssueID(args []string) (int, error) {
	switch len(args) {
	case 0:
		return 0, nil
	case 1:
		id, err := strconv.Atoi(strings.TrimPrefix(args[0], "#"))
		if err != nil || id <= 0 {
			return 0, i18n.Errorf("err.bad_issue", args[0])
		}
		return id, nil
	default:
		return 0, i18n.Errorf("err.too_many_args")
	}
}
