# gws-cache

`gws-cache` is a fast, local SQLite caching layer for Gmail threads and Google Calendar events accessed via the [`gws`](https://github.com/googleworkspace/cli) CLI.

It provides token-efficient, 0ms local offline queries, keyword search, timeline exporting, and high-watermark delta synchronization for Gmail messages and Google Calendar invites.

## Features

- **Topic & Feed Management**: Group related Gmail search queries into named topics (e.g. newsletters, billing, projects).
- **Incremental Delta Sync**: Uses Gmail `historyId` and message internal timestamps for fast, high-watermark syncs.
- **Fast Local Search**: Query cached messages instantly by keyword, topic, date range, or sender without making remote Gmail API calls.
- **Token-Efficient Exporter**: Export formatted message timelines in Markdown or JSON for local analysis or LLM contexts.
- **Inbox Scanning**: Scan inbox queries with pre-fetch deduplication to cache matching emails in SQLite.
- **Google Calendar Sync**: Synchronize calendar invites using Google Calendar `syncToken` delta ingestion and dual-horizon lookback/lookahead windows.
- **Email Classifier**: Automatically identifies automated system notifications, bulk lists, and transactional emails versus human messages.

## Prerequisites & Authentication Setup

`gws-cache` relies on the [`gws`](https://github.com/googleworkspace/cli) (Google Workspace) CLI for live fetching from Gmail and Google Calendar APIs.

### 1. Install `gws` CLI

Install `gws` globally via npm or download from [googleworkspace/cli releases](https://github.com/googleworkspace/cli/releases):

```bash
npm install -g @googleworkspace/cli

# Verify gws is installed:
gws --version
```

### 2. Enable Required Google Cloud APIs

`gws` requires a Google Cloud project with the following APIs enabled:
- **Gmail API** (`gmail.googleapis.com`)
- **Google Calendar API** (`calendar-json.googleapis.com`)

Run `gws auth setup` to configure a Google Cloud project automatically, or enable these APIs manually in the [Google Cloud Console](https://console.cloud.google.com/).

### 3. Authenticate `gws` with Gmail & Calendar Scopes

Run `gws auth login` targeting the `gmail` and `calendar` services:

```bash
# Interactively authenticate gws for Gmail and Calendar
gws auth login -s gmail,calendar
```

#### Required Scopes:
- **Gmail Readonly**: `https://www.googleapis.com/auth/gmail.readonly` (for `users.threads.list` and `users.threads.get`).
- **Calendar Readonly**: `https://www.googleapis.com/auth/calendar.readonly` or `https://www.googleapis.com/auth/calendar.events.readonly` (for `events.list`).

### 4. Verify `gws` Connection

Verify `gws` authentication before using `gws-cache`:

```bash
# Test Gmail thread access
gws gmail users threads list --params '{"userId": "me", "q": "in:inbox"}'

# Test Calendar access
gws calendar events list --params '{"calendarId": "primary"}'
```

## Installation

### Download Pre-Built Release Archives

Download the latest release archive from [GitHub Releases](https://github.com/alexgorbatchev/gws-cache-cli/releases):

```bash
# macOS (Apple Silicon / ARM64)
curl -L https://github.com/alexgorbatchev/gws-cache-cli/releases/download/v1.0.0/gws-cache_1.0.0_darwin_arm64.tar.gz | tar -xz
sudo mv gws-cache /usr/local/bin/

# Linux (x86_64)
curl -L https://github.com/alexgorbatchev/gws-cache-cli/releases/download/v1.0.0/gws-cache_1.0.0_linux_amd64.tar.gz | tar -xz
sudo mv gws-cache /usr/local/bin/
```

### Build From Source

Requires Go 1.22+ installed:

```bash
git clone https://github.com/alexgorbatchev/gws-cache-cli.git
cd gws-cache-cli
just build
# Binary output is at ./bin/gws-cache
```

Or install directly into `$GOPATH/bin`:

```bash
go install github.com/alexgorbatchev/gws-cache-cli/cmd/gws-cache@latest
```

## Quick Start & Usage

### 1. Register Topics to Track

Register topics with custom Gmail queries:

```bash
# Register topics with specific Gmail queries
gws-cache topic add newsletters --name "Tech Newsletters" --query "category:promotions"
gws-cache topic add billing --name "Invoices & Receipts" --query "from:billing@"

# List tracked topics
gws-cache topic list

# Remove a topic
gws-cache topic remove newsletters
```

### 2. Synchronize Email Topics (`sync`)

Fetch and cache emails for tracked topics into local SQLite:

```bash
# Sync recent emails for a topic (defaults to --since 4w and max 25 threads)
gws-cache sync newsletters

# Custom lookback window and max thread limit
gws-cache sync newsletters --since 2w --max-threads 10

# Sync all tracked topics
gws-cache sync --all
```

### 3. Search Cached Emails (`search`)

Query cached messages instantly from local SQLite with 0ms network latency:

```bash
# Search cached emails across all topics
gws-cache search "Digest"

# Filter search by topic, date, or human-only emails
gws-cache search "Invoice" --topic billing --since 14d --format table
gws-cache search --human-only --format json
```

### 4. Scan Inbox (`scan`)

Scan inbox threads with pre-fetch deduplication (skips already cached threads in 0ms):

```bash
# Scan recent inbox (default query 'in:inbox', lookback --since 7d)
gws-cache scan --since 7d --max-threads 30

# Scan inbox with custom search query
gws-cache scan --query "is:unread" --since 3d
```

### 5. Export Message Timelines (`export`)

Emit formatted message timelines in Markdown or JSON:

```bash
# Export Markdown timeline (human emails only)
gws-cache export newsletters --format markdown

# Export full history or specific lookback window
gws-cache export newsletters --since all --format markdown
gws-cache export newsletters --since 2w --format markdown

# Export structured JSON payload
gws-cache export newsletters --format json --human-only
```

### 6. Synchronize Google Calendar (`calendar`)

Sync Google Calendar events using `syncToken` delta ingestion:

```bash
# Sync primary calendar events (defaults to 4 weeks past, 4 weeks future lookahead)
gws-cache calendar sync

# Expand lookback and lookahead windows
gws-cache calendar sync --past 8w --future 6w

# List cached calendar events
gws-cache calendar list newsletters
gws-cache calendar list --format json --since 4w
```

### 7. Database Health & Statistics (`status`)

Inspect database statistics and count of tracked topics, cached threads, and cached messages:

```bash
gws-cache status
```

## Configuration & Flags

- `--db <path>`: Custom path to SQLite cache database (defaults to `cache.db` adjacent to executable).
- `--version`: Print CLI version string.

## Development

Run unit tests and generate coverage reports:

```bash
# Run unit tests
just test

# Build executable
just build

# Generate test coverage report
just coverage
```

## License

[MIT](LICENSE)
