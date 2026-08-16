---
created_on: 2026-07-30 16:00
last_modified: 2026-08-15 01:00
status: current
---

# gws-cache Agent Guidelines

Workspace for `gws-cache` — a generic local SQLite caching layer for Gmail threads and Google Calendar accessed via the `gws` CLI.
Repository: `https://github.com/alexgorbatchev/gws-cache-cli`

## Shared Commands
- Build executable: `just build` (outputs to `./bin/gws-cache`)
- Run unit tests: `just test` (`go test -v ./...`)
- Run coverage report: `just coverage` (generates `coverage.out`)
- Clean build artifacts: `just clean`

## Architecture Map
- `cmd/gws-cache/main.go`: Cobra CLI entrypoint (`topic`, `sync`, `search`, `scan`, `export`, `calendar`, `status`).
- `pkg/store/`: CGO-free SQLite database schema (`modernc.org/sqlite`) and store operations.
- `pkg/topic/`: Topic tracking service (`RegisterTopic`, `ListTopics`, `DeleteTopic`).
- `pkg/sync/`: High-watermark incremental sync engine (`ParseSince`, `SyncTopic`, `SyncAllTopics`).
- `pkg/scan/`: Inbox query scanning and cached email search (`ScanInbox`, `SearchCached`).
- `pkg/exporter/`: Markdown and JSON timeline formatting (`ExportTopic`).
- `pkg/calendar/`: Google Calendar event sync and listing (`SyncCalendar`).
- `pkg/classifier/`: Rule-based automated vs. human email classifier.
- `pkg/gmail/`: `gws` CLI client interface (`ListThreads`, `GetThread`, `ListCalendarEvents`).
- `skill/SKILL.md`: Agent skill documentation.
- `.github/workflows/release.yml`: GitHub Actions automated release pipeline for `v*` tags.

## Mandatory Maintenance & Development Boundaries

### 1. Mandatory Code Testing & Coverage
Any time code is modified such that runtime results change, a corresponding test file MUST be updated/added as well. Maintain $\ge 90\%$ statement coverage across domain packages (`just coverage`).

### 2. Keep Skill Documentation in Sync
Whenever CLI commands, subcommands, flags, default parameters, or package behavior change, you MUST immediately update `skill/SKILL.md` to keep documentation in 100% sync.

### 3. No Live Network Calls in Tests
All Go unit tests MUST mock `gmail.Client` or `exec.Command`. Tests must never make live network calls to Gmail or Google Calendar APIs.

### 4. No Binaries or Databases in Git
Compiled binaries (`bin/`, `dist/`) and SQLite database files (`*.db`, `*.db-wal`, `*.db-journal`) MUST be excluded via `.gitignore` and never committed to the repository.

### 5. Automated CI/CD Release Pipeline
Release binaries are compiled and published automatically by GitHub Actions (`.github/workflows/release.yml`) whenever a version tag (`v*`) is pushed. Never manually upload locally built binaries to GitHub Releases.

### 6. Automatic Instruction Recording
Automatically record any new project conventions or user instructions in this `AGENTS.md` file without requiring the user to ask. Check with the user first if instructions conflict with existing rules.
