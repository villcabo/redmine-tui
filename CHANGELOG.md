# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- Children in the list: `+n` marks an issue with n children, and `→` / `←` / `space`
  expand and collapse its whole subtree in place, with status, priority and assignee.
- The Children tab of the detail draws the full tree (grandchildren included) with the
  same columns as the list, and a summary of direct, total and closed children.
- The Relations tab shows the subject, status, priority and assignee of the other issue.
- The preview shows percent done, a colored due date (overdue in red, due within two days
  in amber) and how many children are closed.
- Write actions from the list: `S`, `c`, `n`, `D` and `A` act on the selected row
  without opening it (the issue is fetched first for its workflow statuses).
- `O` changes the order on the server: updated, due date (issues with one), priority,
  created.
- The detail opens at once with the list row or a cached copy, marked "updating…".
- Version, category and estimated/spent hours in the detail when the instance reports
  them; the version also in the preview.
- The list header says when it was loaded, and a quick filter with no match says so
  and how to clear it.

### Changed

- Reloading the list, or saving a change, keeps the cursor on the same issue instead of
  jumping to the first row.
- "Assigned to me" leaves out the Assignee column; a long "Parent - Child" project name
  shows its last part.
- `o` opens the browser with `open` on macOS and `rundll32` on Windows, not only
  `xdg-open`, and confirms it did.

## [1.0.0] - 2026-11-17

First public release.

### Added

- Write actions, each behind a confirmation that shows the exact change and
  cancels by default: change status (only the statuses the workflow allows),
  add a note (in `$EDITOR` or an in-app text area), set percent done, assign,
  quick close, and create issues (including required custom fields).
- Read-only mode (`--read-only`, `REDMINE_READ_ONLY=1`, `read_only = true`).
- Views: assigned to me, created by me, watching, all issues, assigned to or
  created by any user, and by project.
- Filter panel (project, status, tracker, assignee, author) sent to Redmine as
  query parameters, with an active-filter summary under the title.
- On-demand pagination of long lists ("100 of 340").
- Table list with aligned columns, status summary, preview panel (140+
  columns) and a status bar with the mode, user and host.
- Go to an issue by number, full-text search, and a copy menu (link, number,
  title) that uses OSC 52 so it also works over SSH.
- English and Spanish interface (`--lang`, `REDMINE_TUI_LANG`, config, locale).
- Optional config file `config.toml` and the `redmine-tui init` command.
- API key file may hold only the key, or a `X-Redmine-API-Key: <key>` line.
- `--version`.
- Markdown in descriptions and notes follows Redmine's CommonMark formatter:
  single line breaks inside a paragraph are kept, headings show without the
  literal `#` marks, and a link shows only its text (clickable through OSC 8 in
  terminals that support it) while bare URLs are still printed in full.

### Changed

- `REDMINE_URL` is now required (flag, environment or config file); there is no
  default host.
- Status colors no longer depend on fixed status ids: closed is green, the
  default status is blue, any other open status is amber, and names can be
  mapped to colors in `[status_colors]`.
- In the list, `g` opens the "go to issue" dialog; the start of the list is `home`.

### Fixed

- The cursor no longer hides the first letter of an input's placeholder (for
  example "required" in the Subject field of a new issue).

### Security

- The API key is still masked when formatted, redacted from errors and never
  placed in a URL; the key file must have mode 600.
- Redirects are never followed.
- Only `PUT /issues/:id.json` and `POST /issues.json` can be sent; there is no
  `DELETE` and no request to users, projects or custom fields other than reads.
- A write that times out or fails on the network is never retried.

[1.0.0]: https://github.com/villcabo/redmine-tui/releases/tag/v1.0.0
