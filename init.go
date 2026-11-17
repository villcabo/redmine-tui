package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/villcabo/redmine-tui/internal/config"
	"github.com/villcabo/redmine-tui/internal/i18n"
	"github.com/villcabo/redmine-tui/internal/redmine"
)

const defaultKeyFileRel = ".config/redmine/api-key-header"

// runInit creates the config file interactively. It never asks for the API key in
// clear text: it explains how to store it, or reads it with hidden input.
func runInit(d deps, tr i18n.Translator, fl config.Flags, extra []string) error {
	if len(extra) > 0 {
		return i18n.Errorf("err.too_many_args")
	}
	path := config.DefaultPath(d.getenv, d.home)
	if fl.ConfigPath != "" {
		path = fl.ConfigPath
	}
	in := bufio.NewReader(d.stdin)
	ask := func(prompt string) (string, error) {
		fmt.Fprint(d.stdout, prompt)
		line, err := in.ReadString('\n')
		if err != nil && !(errors.Is(err, io.EOF) && line != "") {
			return "", err
		}
		return strings.TrimSpace(line), nil
	}
	yes := func(prompt string) (bool, error) {
		a, err := ask(prompt)
		return strings.EqualFold(a, tr.T("confirm.yes_key")), err
	}

	fmt.Fprintln(d.stdout, tr.T("init.title"))
	if _, err := d.readFile(path); err == nil {
		ok, err := yes(tr.T("init.exists", path))
		if err != nil || !ok {
			fmt.Fprintln(d.stdout, tr.T("init.cancelled"))
			return err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return i18n.Wrap(err, "err.config_read", path)
	}

	var base string
	for {
		v, err := ask(tr.T("init.url_prompt"))
		if err != nil {
			return err
		}
		if u, perr := url.Parse(v); perr == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" {
			base = strings.TrimRight(v, "/")
			break
		}
		fmt.Fprintln(d.stdout, tr.T("init.url_bad"))
	}

	lang := tr.Lang()
	for {
		v, err := ask(tr.T("init.lang_prompt", lang))
		if err != nil {
			return err
		}
		if v == "" {
			break
		}
		if l, ok := i18n.Parse(v); ok {
			lang = l
			break
		}
		fmt.Fprintln(d.stdout, tr.T("init.lang_bad"))
	}

	if err := config.Write(path, config.Render(config.File{URL: base, Language: string(lang)})); err != nil {
		return err
	}
	fmt.Fprintln(d.stdout, tr.T("init.written", path))

	return offerKey(d, i18n.New(lang), yes)
}

func offerKey(d deps, tr i18n.Translator, yes func(string) (bool, error)) error {
	keyPath := filepath.Join(d.home, defaultKeyFileRel)
	fmt.Fprintln(d.stdout)
	fmt.Fprint(d.stdout, tr.T("init.key_intro", "~/"+defaultKeyFileRel))

	typeIt, err := yes(tr.T("init.key_ask"))
	if err != nil || !typeIt {
		fmt.Fprintln(d.stdout, tr.T("init.done"))
		return nil
	}
	if _, err := d.readFile(keyPath); err == nil {
		ok, err := yes(tr.T("init.key_exists", keyPath))
		if err != nil || !ok {
			fmt.Fprintln(d.stdout, tr.T("init.cancelled"))
			return err
		}
	}
	fmt.Fprint(d.stdout, tr.T("init.key_prompt"))
	secret, err := d.readSecret()
	fmt.Fprintln(d.stdout)
	if errors.Is(err, errNoTerminal) {
		fmt.Fprintln(d.stdout, tr.T("init.key_no_tty"))
		return nil
	}
	if err != nil {
		return err
	}
	secret = strings.TrimSpace(secret)
	if secret == "" {
		fmt.Fprintln(d.stdout, tr.T("init.key_empty"))
		return nil
	}
	if err := config.WriteSecret(keyPath, redmine.DefaultKeyHeader+": "+secret); err != nil {
		return err
	}
	fmt.Fprintln(d.stdout, tr.T("init.key_saved", keyPath))
	fmt.Fprintln(d.stdout, tr.T("init.done"))
	return nil
}
