---
created_on: 2026-07-30 16:00
last_modified: 2026-09-17 12:00
status: current
---

# gws-cache Agent Guidelines

Workspace for `gws-cache` — a generic local SQLite caching layer for Gmail threads and Google Calendar accessed via the `gws` CLI.
Repository: `https://github.com/alexgorbatchev/gws-cache-cli`
Module path: `github.com/alexgorbatchev/gws-cache-cli`

## Shared Commands
- Build executable: `just build` (outputs to `./bin/gws-cache`)
- Run in human mode: `just run <args>`
- Run in agent mode: `just run-ai <args>` (sets `AGENT=1`)
- Run unit tests: `just test` (`go test -race ./...`)
- Lint: `just lint` (`gofmt -l .`, `go mod tidy -diff`, `go vet ./...`)
- Lint and test together: `just check`
- Coverage report: `just coverage` (generates `coverage.out`)
- Clean build artifacts: `just clean`

## Architecture Map
- `cmd/gws-cache/main.go`: Thin entrypoint. Installs the signal handler and executes the command tree.
- `internal/cli/`: Cobra command tree, organized subject first (`cache`, `event`, `message`, `topic`), plus the dual-mode output layer (`output.go`).
- `internal/store/`: CGO-free SQLite schema and store operations (`modernc.org/sqlite`). Owns `ErrTopicNotFound`.
- `internal/topic/`: Topic registration service (`RegisterTopic`, `ListTopics`, `DeleteTopic`).
- `internal/mailsync/`: High-watermark incremental sync engine (`SyncTopic`, `SyncAllTopics`, `SyncQuery`).
- `internal/message/`: Gmail payload parsing, classification and thread ingestion. Every sync path goes through it.
- `internal/timewindow/`: Parses `--since` / `--past` / `--future` expressions into a `Window` that distinguishes unbounded from unset.
- `internal/exporter/`: Markdown and JSON timeline formatting (`ExportTopic`).
- `internal/event/`: Google Calendar sync and deterministic topic matching (`Engine.Sync`), named for the `event` subject it backs.
- `internal/classifier/`: Rule-based automated versus human mail classifier, with optional user rules at `$XDG_CONFIG_HOME/gws-cache/classifier.json`.
- `internal/gws/`: `gws` CLI client. Exports the concrete `Client` struct; consumers declare the narrow interfaces they need.
- `skill/SKILL.md`: Agent skill documentation.
- `.github/workflows/verify.yml`: Reusable check pipeline, called by both `ci.yml` and `release.yml`.
- `.github/workflows/release.yml`: GoReleaser release pipeline for `v*` tags.

## Mandatory Maintenance & Development Boundaries

### 1. Mandatory Code Testing & Coverage
Any time code is modified such that runtime results change, a corresponding test file MUST be updated/added as well. Maintain $\ge 90\%$ statement coverage in every package under `internal/` (`just coverage`). Tests MUST be table-driven wherever cases vary only by input and expected output.

### 2. Keep Skill Documentation in Sync
Whenever CLI commands, subcommands, flags, default parameters, or package behavior change, you MUST immediately update `skill/SKILL.md` and the `# Options & Flags` tables in `README.md` to keep documentation in 100% sync.

### 3. No Live Network Calls in Tests
Tests MUST never reach Gmail or Google Calendar. Engine tests supply a fake implementing the consumer-side interface (`mailsync.ThreadFetcher`, `calendar.EventLister`). Tests that must exercise the real `exec` path install a stand-in `gws` executable in `t.TempDir()` and prepend it to `PATH`; they skip on Windows.

### 4. No Binaries or Databases in Git
Compiled binaries (`bin/`, `dist/`) and SQLite database files (`*.db`, `*.db-wal`, `*.db-journal`) MUST be excluded via `.gitignore` and never committed.

### 5. Automated CI/CD Release Pipeline
Release binaries are compiled and published automatically by GitHub Actions whenever a version tag (`v*`) is pushed. Never manually upload locally built binaries to GitHub Releases.

### 6. Automatic Instruction Recording
Automatically record any new project conventions or user instructions in this `AGENTS.md` file without requiring the user to ask. Check with the user first if instructions conflict with existing rules.

### 7. Dual-Mode Output Is Mandatory
Every output pathway MUST honour `AGENT=1`. Human mode uses aligned tables via `tablewriter` and `[OK]` / `[ERROR]` text tags; agent mode emits tab-separated `key:value` lines with no tables, dividers, padding or truncation. Emoji are prohibited in both modes. All rendering goes through `internal/cli/output.go`; commands MUST NOT write to stdout directly.

### 8. Subject-First, Content-Owned Command Structure
Commands follow `gws-cache <subject> <verb>`, at most three levels deep, and never a verb at the top level. Each content type owns both its fetch and its read, so `message` and `event` take the same verbs (`sync`, `search`) and must stay symmetric: a verb added to one is a gap in the other until justified. A `topic` only names a saved search; it never fetches. Avoid a subject that exists to hold a single verb unless it is a namespace expected to grow. Help screens are rendered by `cobra-help-tree`; do not hand-roll help output.

### 8a. No Pseudo-Records in the Schema
Content that belongs to no saved search has a NULL `topic_id`, never a reserved sentinel row. A magic row (the former `inbox-scan` topic with id 0) leaks a storage workaround into the command tree and into every query that then has to exclude it.

### 9. Windows Are Parsed, Never Guessed
All lookback and lookahead flags go through `internal/timewindow`. `all` MUST reach the API as an unbounded request, never as a silent default window. Never reuse a lookback parser for a lookahead bound.

### 10. Errors Are Wrapped, Never Swallowed
Wrap errors with `%w` and context describing the attempted operation. Discarding an error with `_` requires a comment saying why. Branch on error kind with `errors.Is` and a sentinel (`store.ErrTopicNotFound`), never on a message string — a database failure must never be mistaken for a missing record.

### 11. Standard Library First
Never hand-roll what the standard library provides: `net/mail` for addresses and dates, `strings.EqualFold`, `strings.TrimSpace`. Every subprocess call uses `exec.CommandContext` with the deadline from `--timeout`.

### 12. Limits Must Never Silently Drop Data
A limit bounds *work per run*, never the data set. Apply a budget to the expensive fetch, not to the listing, so repeated runs drain a backlog instead of re-reading the same page. Count and report whatever the budget did not reach (`ThreadsRemaining`), surface it on stdout as `[WARN]` / `WARN:` beside the result, and never let a partial run read as a complete one. Paginated APIs MUST be followed to the end (`nextPageToken`) before any limit is reasoned about.

Any command whose result a script would branch on MUST offer `--format json`. A caller must never have to match on prose to learn whether a run was complete. JSON output is emitted alone on stdout, with no status tags mixed in and progress kept on stderr, and internal cursors such as a Google sync token are excluded from it.

### 13. Never Advance a Watermark Past Uncollected Data
The incremental high-watermark means "everything older than this is cached", so it is derived from the store (`MaxMessageInternalDate`), never from what one run happened to download, and it is held back entirely while any backlog remains. Advancing it past records a run did not collect strands them permanently: later incremental syncs start after them and even `--force-full` will not look back that far.

### 14. No Vendor-Specific Data in the Binary
The classifier ships generic rules only. Anything naming a specific vendor, product or employer belongs in the user's `classifier.json`.
