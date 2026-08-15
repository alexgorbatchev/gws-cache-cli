# gws-cache Agent Guidelines

Workspace for the `gws-cache` Go CLI utility — a generic local SQLite caching layer for Gmail threads and Google Calendar accessed via `gws`.

## Shared Commands
- Build binary: `just build` (outputs to `bin/gws-cache`)
- Run unit tests: `just test`
- Run coverage report: `just coverage` (generates `coverage.out`)
- Clean build artifacts: `just clean`

## Architecture Map
- `cmd/gws-cache/main.go`: Cobra CLI entrypoint and commands.
- `pkg/store/`: CGO-free SQLite database schema (`modernc.org/sqlite`) and store operations.
- `pkg/topic/`: Topic tracking service (`RegisterTopic`, `ListTopics`, `DeleteTopic`).
- `pkg/classifier/`: Rule-based automated vs. human email classifier.
- `pkg/gmail/`: `gws` CLI client interface.
- `pkg/sync/`: High-watermark incremental sync engine (`ParseSince`, `SyncTopic`, `SyncAllTopics`).
- `pkg/scan/`: Inbox scanning and cached email search (`ScanInbox`, `SearchCached`).
- `pkg/exporter/`: Markdown and JSON timeline formatting (`ExportTopic`).
- `pkg/calendar/`: Google Calendar event sync and listing (`SyncCalendar`).
- `skill/SKILL.md`: Agent skill documentation.

## Mandatory Maintenance Boundaries
1. **KEEP SKILL UP TO DATE (REQUIRED)**: Whenever you modify CLI commands, subcommands, flags, default parameters, or behavior in `gws-cache/`, you MUST immediately update `gws-cache/skill/SKILL.md` to keep documentation in 100% sync.
2. **NO LIVE GMAIL REQUESTS IN TESTS**: All Go unit tests in `gws-cache/` MUST mock `gmail.Client` or `exec.Command`. Tests must never make live network calls to Gmail.
3. **HIGH FUNCTION COVERAGE**: Keep statement and function coverage at $\ge 90\%$ across domain packages.
4. **NO BINARIES OR DATABASES IN GIT**: Compiled binaries (`bin/`) and SQLite databases (`*.db`) MUST be excluded via `.gitignore` and never committed to the repository.
