---
name: gws-cache
description: >-
  ALWAYS USE when interacting with cached Gmail messages or Google Calendar entries,
  exporting timelines, syncing topics or one-off searches, or searching cached email via the
  gws-cache CLI (`./bin/gws-cache`). You MUST load this skill to run fast, local, token-efficient
  timeline queries from SQLite instead of the raw gws CLI.
author: alexgorbatchev
metadata:
  created_on: 2026-07-30 16:00
  last_modified: 2026-09-17 12:00
  status: current
---

## Binary Location & Setup

The `gws-cache` executable is at `./bin/gws-cache` (build it with `just build`).

Always invoke it with `AGENT=1` set. That switches every command to
token-conservative output: no tables, no dividers, no padding, full untruncated
values as tab-separated `key:value` pairs.

```bash
AGENT=1 ./bin/gws-cache topic list
# slug:newsletters	name:Tech Newsletters	search:category:promotions	last_synced:never
```

The cache lives at `$XDG_CACHE_HOME/gws-cache/cache.db` (falling back to
`~/.cache/gws-cache/cache.db`). Override it with `--db <path>`.

## Prerequisites & Authentication Setup

`gws-cache` shells out to the `gws` CLI ([@googleworkspace/cli](https://github.com/googleworkspace/cli)), which must be installed and authenticated.

```bash
npm install -g @googleworkspace/cli
gws auth setup                   # enables the Gmail and Calendar APIs
gws auth login -s gmail,calendar # authorizes both services
```

Required read-only scopes:
- `https://www.googleapis.com/auth/gmail.readonly` (`users.threads.list`, `users.threads.get`)
- `https://www.googleapis.com/auth/calendar.readonly` (`events.list`)

## Command Structure

Commands are subject first: `gws-cache <subject> <verb>`.

```
gws-cache
├─ cache
│  ╰─ status                 Show what the local copy currently holds
├─ event                     Calendar entries
│  ├─ search [words]         Search saved entries without going online
│  ╰─ sync                   Fetch entries into the local copy
├─ message                   Email
│  ├─ export <topic>         Write a topic's email out as a timeline
│  ├─ search [words]         Search saved email without going online
│  ╰─ sync [topic]           Fetch new email into the local copy
╰─ topic                     Saved searches (definition only)
   ├─ add <name>             Start tracking a new topic
   ├─ list                   Show the topics you are tracking
   ╰─ rm <name>              Stop tracking a topic and forget its email
```

Each content type owns both its fetch and its read, so `message` and `event`
take the same verbs. A `topic` only *names* a search; fetching the mail it
stands for is `message sync`.

## Global Flags

| Flag | Default | Description |
| :--- | :--- | :--- |
| `--db <path>` | `$XDG_CACHE_HOME/gws-cache/cache.db` | Path to the local cache file |
| `--timeout <duration>` | `2m0s` | Deadline for a single `gws` request |
| `--version` | | Prints only the version string |

## Time Windows

`--since`, `--past` and `--future` all accept:
- a span from now: `24h`, `30d`, `4w`, `2m`, `1y`
- a date: `2026-06-01`
- `all`, which applies no bound in that direction

`message sync` resumes from a topic's stored high-watermark when it has synced
that topic before, ignoring the default `--since`. Passing `--since` explicitly
overrides the watermark for that run.

## Core Commands & Usage

### 1. Topics (`topic`)

A topic is a saved search and nothing more. Registering one does not fetch
anything; `message sync` does that.

```bash
AGENT=1 ./bin/gws-cache topic add newsletters --name "Tech Newsletters" --query "category:promotions"
AGENT=1 ./bin/gws-cache topic list
AGENT=1 ./bin/gws-cache topic rm newsletters
```

`topic add` flags: `--name <text>`, `--query <search>`.

### 2. Fetching Email (`message sync`)

Exactly one of a topic name, `--all` or `--query` is required.

```bash
# One topic (defaults: --since 4w, --max-threads 25)
AGENT=1 ./bin/gws-cache message sync newsletters

# Every tracked topic
AGENT=1 ./bin/gws-cache message sync --all

# A one-off search that registers nothing and belongs to no topic
AGENT=1 ./bin/gws-cache message sync --query "is:unread" --since 3d

# Reach further back than the stored watermark
AGENT=1 ./bin/gws-cache message sync newsletters --since 1y
AGENT=1 ./bin/gws-cache message sync newsletters --force-full
```

`message sync` flags: `--all`, `--query <search>`, `--force-full`, `--since <window>`, `--max-threads <n>`, `--format table|json`.

**Check for an incomplete run.** `--max-threads` is a per-run budget on
downloads (default 25), not a cap on the listing. When a run cannot finish, it
prints a warning and holds the sync watermark so the next run resumes from the
same point:

```
WARN: newsletters: 25 of 118 new conversations fetched; 93 remaining, run again or raise --max-threads
OK: newsletters: 25 conversations, 63 messages
```

Prefer `--format json` over matching the warning text. It emits the result and
nothing else on stdout, with progress still on stderr:

```bash
AGENT=1 ./bin/gws-cache message sync newsletters --format json
```

```json
{
  "target": "newsletters",
  "threads_listed": 118,
  "threads_fetched": 25,
  "threads_remaining": 93,
  "messages_ingested": 63,
  "max_history_id": "4412098"
}
```

`threads_remaining` above zero means the cache is not yet complete: run the
command again until it reaches zero, or pass a larger `--max-threads`. `--all`
always returns an array of these, one per topic, each with an `error` field when
that topic failed. Never report a topic as fully synced while `threads_remaining`
is above zero.

### 3. Searching Saved Email (`message search`)

Reads the local SQLite cache only; no network access.

```bash
AGENT=1 ./bin/gws-cache message search "digest"
AGENT=1 ./bin/gws-cache message search "invoice" --topic billing --since 14d
AGENT=1 ./bin/gws-cache message search --human-only --since all --format json
```

`message search` flags: `--topic <name>`, `--since <window>`, `--human-only`, `--format table|json`.

### 4. Exporting Timelines (`message export`)

```bash
AGENT=1 ./bin/gws-cache message export newsletters --format markdown
AGENT=1 ./bin/gws-cache message export newsletters --since all --format json
AGENT=1 ./bin/gws-cache message export newsletters --human-only=false
```

`message export` flags: `--human-only` (default `true`), `--format markdown|json`, `--since <window>`.

### 5. Calendar Entries (`event`)

Delta ingestion via Google's `syncToken`, falling back to a bounded window when
the token is rejected. Entries are matched to topics by whole-word search over
the summary and description.

```bash
AGENT=1 ./bin/gws-cache event sync
AGENT=1 ./bin/gws-cache event sync --past 8w --future 6w
AGENT=1 ./bin/gws-cache event sync --past all --future all
AGENT=1 ./bin/gws-cache event sync --force-full

AGENT=1 ./bin/gws-cache event search
AGENT=1 ./bin/gws-cache event search "standup" --topic newsletters --since 4w --format json
```

`event sync` flags: `--calendar-id <id>`, `--past <window>`, `--future <window>`, `--force-full`, `--format table|json`.
`event search` flags: `--calendar-id <id>`, `--topic <name>`, `--since <window>`, `--format table|json`.

### 6. Cache Health (`cache status`)

```bash
AGENT=1 ./bin/gws-cache cache status
# location:/home/you/.cache/gws-cache/cache.db	topics:2	conversations:14	messages:57
```

## Classifying Automated Mail

Built-in rules mark mail automatic when it carries `Auto-Submitted` or
`List-Unsubscribe`, or when the sender matches `no-reply@`, `do-not-reply@`,
`notifications@`, `mailer-daemon@` or `postmaster@`.

Additional rules go in `$XDG_CONFIG_HOME/gws-cache/classifier.json` as Go
regular expressions, and are applied alongside the built-in ones:

```json
{
  "sender_patterns": ["(?i)no-reply@ashbyhq\\.com"],
  "snippet_patterns": ["(?i)has requested a signature"]
}
```

A malformed rules file is reported as an error rather than ignored.

## Mandatory Maintenance Rule

**CRITICAL MAINTENANCE BOUNDARY:** Whenever CLI flags, subcommands, defaults, or
behavior change, you MUST immediately update this `SKILL.md` to keep it in 100%
sync.
