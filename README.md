`gws-cache` keeps a local copy of your Gmail conversations and Google Calendar events so you can search, read and export them instantly, without waiting on the network.

# What It Does

- Groups related emails into named topics, such as newsletters, billing or a particular project.
- Keeps each topic up to date by fetching only what has changed since the last run.
- Searches everything saved locally by words, sender, topic or date, with no network round trip.
- Writes a topic's history out as a Markdown or JSON timeline.
- Saves a copy of recent mailbox conversations, skipping anything already stored.
- Keeps a local copy of your calendar, including events that were later cancelled.
- Separates automatic notifications and receipts from mail a person actually wrote.

# How It Works

- You name a topic and give it an email search, for example everything from your billing address.
- Each time you sync, `gws-cache` asks Google for the mail matching that search and stores it on your machine. You can also fetch a one-off search without tracking it.
- On later syncs it only asks for mail newer than what it already has, so repeat runs are quick.
- Anything already saved is left alone, so syncing twice costs almost nothing.
- Each run downloads a bounded number of new conversations. If more are waiting, it says so and the next run picks up where it left off, so nothing is ever quietly left behind.
- Searching, listing and exporting read the local copy only, so they return immediately and work offline.
- Calendar events are copied the same way, and events removed from your calendar stay in the local copy marked as cancelled.
- Every command prints a readable table by default, and switches to compact machine-readable lines when an automated tool runs it.

# How it Really Works

- All state lives in one SQLite database, created at `$XDG_CACHE_HOME/gws-cache/cache.db` and opened through the CGO-free `modernc.org/sqlite` driver.
- Gmail and Google Calendar are reached by invoking the [`gws`](https://github.com/googleworkspace/cli) CLI as a subprocess and parsing its JSON, so `gws-cache` never handles your OAuth credentials itself.
- Thread and event listings follow `nextPageToken` to the end. Gmail pages at 100 threads and Calendar at 250 events, so reading a single page would silently truncate the window.
- Topic sync keeps a high-watermark per topic: the newest cached message `internalDate`, minus a 24 hour overlap, translated into a Gmail `after:` term. Passing `--since` explicitly overrides that watermark for the run.
- Before fetching a conversation, the cached `historyId` is compared with the one Gmail reports; a match skips the fetch entirely.
- `--max-threads` is a per-run budget on downloads, not a cap on the listing. Cached conversations cost nothing against it, anything it does not reach is counted and reported, and the watermark is held back until the window is fully cached — so a bounded run can never strand mail behind the mark.
- Calendar sync stores the `nextSyncToken` Google returns and replays it for delta ingestion, falling back to a bounded `timeMin`/`timeMax` window when the token is rejected.
- Calendar events are matched to topics by whole-word search over the event summary and description, with longer topic names taking precedence, so the same event always resolves to the same topic.
- Classification is rule based: the RFC 3834 `Auto-Submitted` and RFC 2369 `List-Unsubscribe` headers decide first, then regular expressions over the sender and snippet.
- Every subprocess call runs under a context deadline set by `--timeout`, and `Ctrl-C` cancels the run in progress.

# Prerequisites

- [`gws`](https://github.com/googleworkspace/cli), the Google Workspace CLI, installed and on your `PATH`: `npm install -g @googleworkspace/cli`.
- A Google Cloud project with the [Gmail API](https://console.cloud.google.com/apis/library/gmail.googleapis.com) and the [Google Calendar API](https://console.cloud.google.com/apis/library/calendar-json.googleapis.com) enabled. Running `gws auth setup` will do this for you.
- An authenticated `gws` session covering both services: `gws auth login -s gmail,calendar`. The read-only scopes `https://www.googleapis.com/auth/gmail.readonly` and `https://www.googleapis.com/auth/calendar.readonly` are enough.

# Installation

Download the archive for your platform from the [latest release](https://github.com/alexgorbatchev/gws-cache-cli/releases/latest), unpack it and move the binary onto your `PATH`. For example, on macOS with Apple Silicon:

```bash
curl -L https://github.com/alexgorbatchev/gws-cache-cli/releases/download/v2.0.0/gws-cache_2.0.0_darwin_arm64.tar.gz | tar -xz
sudo mv gws-cache /usr/local/bin/
```

# Quick Start

Commands are named after the thing they act on: `topic` for the searches you
want kept up to date, `message` and `event` for fetching and reading each kind
of content, and `cache` for the local copy itself.

```bash
# Name a search you want kept up to date
gws-cache topic add newsletters --name "Tech Newsletters" --query "category:promotions"

# Fetch its mail into the local copy
gws-cache message sync newsletters

# Fetch a one-off search without tracking it
gws-cache message sync --query "is:unread"

# Search everything saved locally
gws-cache message search "digest"

# Write a topic out as a timeline
gws-cache message export newsletters --format markdown

# Copy your calendar and read it back
gws-cache event sync
gws-cache event search "standup"

# See what the local copy holds
gws-cache cache status
```

# Options & Flags

Global flags apply to every command.

| Flag | Short | Default | Description |
| :--- | :--- | :--- | :--- |
| `--db <path>` | | `$XDG_CACHE_HOME/gws-cache/cache.db` | Path to the local cache file; quoted `~/` paths expand to your home directory. Help and status display paths inside your home with `~`. |
| `--timeout <duration>` | | `2m0s` | How long to wait for a single Google Workspace request |
| `--version` | `-v` | | Print the version and exit |
| `--help` | `-h` | | Show the command tree |

`topic add <name>`

| Flag | Short | Default | Description |
| :--- | :--- | :--- | :--- |
| `--name <text>` | | slug | Friendly name to show in listings |
| `--query <search>` | | | Email search to collect for this topic |

`message sync [topic]`

| Flag | Short | Default | Description |
| :--- | :--- | :--- | :--- |
| `--all` | | `false` | Sync every topic you track |
| `--query <search>` | | | Fetch a one-off search instead of a topic |
| `--force-full` | | `false` | Fetch everything again instead of only what changed |
| `--since <window>` | | `4w` | How far back to look: a span, a date, or `all` |
| `--max-threads <n>` | | `25` | New conversations to download in one run; the rest are reported and collected next run |
| `--format <style>` | | `table` | Output style: `table` or `json` |

`message search [words]`

| Flag | Short | Default | Description |
| :--- | :--- | :--- | :--- |
| `--topic <name>` | | | Only look inside this topic |
| `--since <window>` | | `14d` | How far back to search: a span, a date, or `all` |
| `--human-only` | | `false` | Leave out automatic notifications and receipts |
| `--format <style>` | | `table` | Output style: `table` or `json` |

`message export <topic>`

| Flag | Short | Default | Description |
| :--- | :--- | :--- | :--- |
| `--human-only` | | `true` | Leave out automatic notifications and receipts |
| `--format <style>` | | `markdown` | Output style: `markdown` or `json` |
| `--since <window>` | | `4w` | How far back to include: a span, a date, or `all` |

`event sync`

| Flag | Short | Default | Description |
| :--- | :--- | :--- | :--- |
| `--calendar-id <id>` | | `primary` | Which calendar to copy |
| `--past <window>` | | `4w` | How far back to include: a span, a date, or `all` |
| `--future <window>` | | `4w` | How far ahead to include: a span, a date, or `all` |
| `--force-full` | | `false` | Fetch the whole window again instead of only what changed |
| `--format <style>` | | `table` | Output style: `table` or `json` |

`event search [words]`

| Flag | Short | Default | Description |
| :--- | :--- | :--- | :--- |
| `--calendar-id <id>` | | `primary` | Which calendar to read |
| `--topic <name>` | | | Only show entries matched to this topic |
| `--since <window>` | | `4w` | How far back to include: a span, a date, or `all` |
| `--format <style>` | | `table` | Output style: `table` or `json` |

# Incomplete Runs

`--max-threads` bounds how many *new* conversations a single run downloads, because each one is a separate request to Google. Already-cached conversations do not count against it.

When a run cannot finish the window it says so on stdout and leaves the sync watermark where it was, so the next run continues from the same point instead of skipping ahead:

```console
$ gws-cache message sync newsletters
[WARN] newsletters: 25 of 118 new conversations fetched; 93 remaining, run again or raise --max-threads
[OK] newsletters: 25 threads, 63 messages

$ gws-cache message sync newsletters   # picks up where the last run stopped
[WARN] newsletters: 25 of 93 new conversations fetched; 68 remaining, run again or raise --max-threads
[OK] newsletters: 25 threads, 71 messages
```

Repeat the command until the warning stops, or pass a larger `--max-threads` to finish in one run.

A partial run still exits `0`, because it succeeded at what it attempted. Rather than matching on the warning text, an automated caller should pass `--format json` and read `threads_remaining`:

```console
$ gws-cache message sync newsletters --max-threads 25 --format json
{
  "target": "newsletters",
  "threads_listed": 118,
  "threads_fetched": 25,
  "threads_remaining": 93,
  "messages_ingested": 63,
  "max_history_id": "4412098"
}
```

`threads_remaining` above zero means the cache is not yet complete: run the command again. `--all` always returns an array of these, one per topic, each carrying an `error` field if that topic failed. In JSON mode nothing else is written to stdout, and progress lines stay on stderr, so the output is safe to pipe straight into a parser.

# Time Windows

Every `--since`, `--past` and `--future` flag accepts the same three forms:

- A span counted from now: `24h`, `30d`, `4w`, `2m`, `1y`.
- A calendar date: `2026-06-01`.
- `all`, which applies no bound in that direction.

On a topic that has been synced before, `message sync` resumes from where it left off and ignores the default `--since`. Passing `--since` yourself overrides that and reaches back as far as you asked.

# Agent Mode

Setting `AGENT=1` switches every command to token-conservative output for automated callers: no tables, no dividers, no alignment padding, and full untruncated values as tab-separated `key:value` pairs.

```bash
AGENT=1 gws-cache topic list
# slug:newsletters	name:Tech Newsletters	search:category:promotions	last_synced:never
```

# Classifying Automated Mail

Out of the box, mail is treated as automatic when it declares the `Auto-Submitted` or `List-Unsubscribe` header, or when the sender looks like `no-reply@`, `do-not-reply@`, `notifications@`, `mailer-daemon@` or `postmaster@`.

To add your own rules, create `$XDG_CONFIG_HOME/gws-cache/classifier.json`. Its patterns are Go regular expressions and are applied in addition to the built-in ones:

```json
{
  "sender_patterns": ["(?i)no-reply@ashbyhq\\.com"],
  "snippet_patterns": ["(?i)has requested a signature", "(?i)upcoming interview in 1 hour"]
}
```

# License

[MIT](LICENSE)
