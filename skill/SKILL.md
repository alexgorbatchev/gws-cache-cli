---
name: gws-cache
description: >-
  ALWAYS USE when interacting with cached Gmail threads or Google Calendar events,
  exporting timelines, syncing topics/feeds, scanning inbox, or searching cached emails via gws-cache CLI (`./bin/gws-cache`).
  You MUST load this skill to run fast, local, token-efficient timeline queries from SQLite instead of raw gws CLI.
author: alexgorbatchev
metadata:
  created_on: 2026-07-30 16:00
  last_modified: 2026-08-15 01:15
  status: current
---

## Binary Location & Setup

The `gws-cache` executable is located at `./bin/gws-cache` (or built via `just build` in root).
Database path default: `./bin/cache.db` (adjacent to binary).

## Prerequisites & Authentication Setup

`gws-cache` requires the `gws` CLI ([@googleworkspace/cli](https://github.com/googleworkspace/cli)) installed and authenticated.

### 1. Install `gws` CLI
```bash
npm install -g @googleworkspace/cli
```

### 2. Required Google Cloud APIs
Enable the following APIs in your Google Cloud project (or run `gws auth setup`):
- **Gmail API** (`gmail.googleapis.com`)
- **Google Calendar API** (`calendar-json.googleapis.com`)

### 3. Authenticate `gws`
Authorize `gws` for Gmail and Calendar scopes:
```bash
gws auth login -s gmail,calendar
```

Required OAuth Scopes:
- **Gmail Readonly**: `https://www.googleapis.com/auth/gmail.readonly` (`users.threads.list`, `users.threads.get`)
- **Calendar Readonly**: `https://www.googleapis.com/auth/calendar.readonly` (`events.list`)

## Core Commands & Usage

### 1. Topic & Tracking Management (`topic`)
Manage tracked topics, feeds, and queries (e.g. newsletters, billing, project threads).
```bash
# Explicitly register a topic or query to track
./bin/gws-cache topic add newsletters --name "Tech Newsletters" --query "category:promotions"
./bin/gws-cache topic add billing --name "Invoices & Billing" --query "from:billing@"

# List tracked topics
./bin/gws-cache topic list

# Remove a topic
./bin/gws-cache topic remove newsletters

# Show database health and stats
./bin/gws-cache status
```

### 2. Synchronizing Email Topics (`sync`)
Syncs email threads for a specific topic or query into local SQLite using high-watermark delta ingestion.
```bash
# Sync recent emails for a topic (defaults to --since 4w lookback and max 25 threads)
./bin/gws-cache sync newsletters

# Customize lookback window or max thread limit
./bin/gws-cache sync newsletters --since 2w --max-threads 10

# Sync all tracked topics
./bin/gws-cache sync --all
```

### 3. Searching Cached Emails (`search`)
Searches local SQLite cache (0ms network overhead) by keyword, topic, date, or sender.
```bash
# Search cached emails for a keyword across all topics
./bin/gws-cache search "Digest"

# Filter search by topic, date lookback, or human-only emails
./bin/gws-cache search --topic newsletters --since 14d --human-only --format table
./bin/gws-cache search "Invoice" --format json
```

### 4. Scanning Inbox (`scan`)
Scans Gmail inbox for emails matching a query with pre-fetch deduplication (skips already-cached threads in 0ms).
```bash
# Scan recent inbox (default query 'in:inbox', --since 7d --max-threads 50)
./bin/gws-cache scan --since 7d --max-threads 30

# Scan inbox with custom query
./bin/gws-cache scan --query "is:unread" --since 3d
```

### 5. Exporting Token-Efficient Timelines (`export`)
Queries local SQLite cache and emits timelines in Markdown or JSON.
```bash
# Export Markdown timeline (defaults to --since 4w lookback, human emails only)
./bin/gws-cache export newsletters --format markdown

# Export full history or specific lookback window
./bin/gws-cache export newsletters --since all --format markdown
./bin/gws-cache export newsletters --since 2w --format markdown

# Export structured JSON payload
./bin/gws-cache export newsletters --format json --human-only
```

### 6. Synchronizing & Inspecting Google Calendar (`calendar`)
Syncs Google Calendar events using a bounded dual-horizon window (`--past`, `--future`) and Google Calendar `syncToken` delta ingestion. Automatically matches calendar invites to tracked topics.
```bash
# Sync primary calendar events (defaults to 4 weeks past, 4 weeks future lookahead)
./bin/gws-cache calendar sync

# Expand lookback and lookahead windows
./bin/gws-cache calendar sync --past 8w --future 6w

# Force full window re-sync (bypassing syncToken)
./bin/gws-cache calendar sync --force-full

# List cached calendar events for a specific topic or all topics
./bin/gws-cache calendar list newsletters
./bin/gws-cache calendar list newsletters --format json --since 4w
```

## Mandatory Maintenance Rule

**CRITICAL MAINTENANCE BOUNDARY:** Whenever CLI flags, subcommands, defaults, or behavior are modified in `gws-cache/`, you MUST immediately update this `SKILL.md` file to keep documentation in 100% sync.
