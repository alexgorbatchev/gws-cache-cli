package cli

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexgorbatchev/gws-cache-cli/internal/store"
)

// seedCache writes one topic holding one message and one calendar event, so
// the read commands have something to render.
func seedCache(t *testing.T) string {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "cache.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}
	defer db.Close()

	top, err := db.CreateTopic("newsletters", "Tech Newsletters", "category:promotions")
	if err != nil {
		t.Fatalf("creating topic: %v", err)
	}
	if err := db.UpsertThread("t-1", &top.ID, "h-1", "Issue #42"); err != nil {
		t.Fatalf("upserting thread: %v", err)
	}

	seconds := time.Now().Add(-time.Hour).Unix()
	if err := db.UpsertMessage(&store.Message{
		ID: "m-1", ThreadID: "t-1", TopicID: &top.ID,
		InternalDate: seconds * 1000,
		DateISO:      time.Unix(seconds, 0).UTC().Format(time.RFC3339),
		FromAddress:  "editor@newsletter.example",
		FromName:     "Editor",
		ToAddress:    "me@example.com",
		Subject:      "Rechnung für Ihre Bestellung — Zusammenfassung",
		Snippet:      "日本語の件名テキストサンプル日本語",
	}); err != nil {
		t.Fatalf("upserting message: %v", err)
	}

	if err := db.UpsertCalendarEvent(&store.CalendarEvent{
		ID: "e-1", CalendarID: "primary",
		Summary:   "Editorial call",
		Location:  "Meet",
		StartTime: time.Now().Add(time.Hour).UTC(),
		EndTime:   time.Now().Add(2 * time.Hour).UTC(),
		Status:    "confirmed",
	}); err != nil {
		t.Fatalf("upserting event: %v", err)
	}

	return dbPath
}

func TestMessageSearchRendersNonASCIIIntact(t *testing.T) {
	dbPath := seedCache(t)

	// Byte-slicing a subject or snippet to fit a column used to cut multi-byte
	// characters in half; nothing may be mangled now.
	t.Setenv("AGENT", "1")
	stdout, err := runShared(t, dbPath, "message", "search", "--since", "all")
	if err != nil {
		t.Fatalf("message search unexpected error: %v", err)
	}
	for _, want := range []string{"Rechnung für Ihre Bestellung — Zusammenfassung", "日本語の件名テキストサンプル日本語"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("output %q should contain %q intact", stdout, want)
		}
	}
	if strings.ContainsRune(stdout, '�') {
		t.Fatalf("output contains a corrupted character: %q", stdout)
	}
}

func TestMessageSearchJSONOutput(t *testing.T) {
	dbPath := seedCache(t)

	stdout, err := runShared(t, dbPath, "message", "search", "--since", "all", "--format", "json")
	if err != nil {
		t.Fatalf("message search unexpected error: %v", err)
	}
	if !strings.Contains(stdout, `"id": "m-1"`) {
		t.Fatalf("json output should contain the message id, got %q", stdout)
	}
}

func TestMessageSearchHumanTable(t *testing.T) {
	dbPath := seedCache(t)

	stdout, err := runShared(t, dbPath, "message", "search", "--since", "all")
	if err != nil {
		t.Fatalf("message search unexpected error: %v", err)
	}
	if !strings.Contains(stdout, "SUBJECT") {
		t.Fatalf("human output should carry table headers, got %q", stdout)
	}
	if !strings.Contains(stdout, "Editor") {
		t.Fatalf("human output should show the sender name, got %q", stdout)
	}
}

func TestMessageSearchHandlesUnparseableStoredDate(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "cache.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}
	top, err := db.CreateTopic("news", "News", "q")
	if err != nil {
		t.Fatalf("creating topic: %v", err)
	}
	if err := db.UpsertThread("t-1", &top.ID, "h", "s"); err != nil {
		t.Fatalf("upserting thread: %v", err)
	}
	// A short, malformed date used to be sliced to ten bytes, which panicked.
	if err := db.UpsertMessage(&store.Message{
		ID: "m-1", ThreadID: "t-1", TopicID: &top.ID,
		InternalDate: time.Now().UnixMilli(),
		DateISO:      "n/a",
		FromAddress:  "dana@example.com", ToAddress: "me@example.com",
		Subject: "Short date", Snippet: "body",
	}); err != nil {
		t.Fatalf("upserting message: %v", err)
	}
	db.Close()

	stdout, err := runShared(t, dbPath, "message", "search", "--since", "all")
	if err != nil {
		t.Fatalf("message search unexpected error: %v", err)
	}
	if !strings.Contains(stdout, "n/a") {
		t.Fatalf("an unparseable date should be passed through, got %q", stdout)
	}
}

func TestEventSearchRenders(t *testing.T) {
	dbPath := seedCache(t)

	human, err := runShared(t, dbPath, "event", "search", "--since", "all")
	if err != nil {
		t.Fatalf("event search unexpected error: %v", err)
	}
	if !strings.Contains(human, "Editorial call") || !strings.Contains(human, "SUMMARY") {
		t.Fatalf("human output should be a table of events, got %q", human)
	}

	t.Setenv("AGENT", "1")
	agent, err := runShared(t, dbPath, "event", "search", "--since", "all")
	if err != nil {
		t.Fatalf("event search unexpected error: %v", err)
	}
	if !strings.Contains(agent, "summary:Editorial call") {
		t.Fatalf("agent output should be flat key:value pairs, got %q", agent)
	}
}

func TestEventSearchJSONOutput(t *testing.T) {
	dbPath := seedCache(t)

	stdout, err := runShared(t, dbPath, "event", "search", "--since", "all", "--format", "json")
	if err != nil {
		t.Fatalf("event search unexpected error: %v", err)
	}
	if !strings.Contains(stdout, `"id": "e-1"`) {
		t.Fatalf("json output should contain the event id, got %q", stdout)
	}
}

func TestMessageExportRenders(t *testing.T) {
	dbPath := seedCache(t)

	stdout, err := runShared(t, dbPath, "message", "export", "newsletters", "--since", "all", "--human-only=false")
	if err != nil {
		t.Fatalf("message export unexpected error: %v", err)
	}
	if !strings.Contains(stdout, "Tech Newsletters Timeline") {
		t.Fatalf("export should render the topic timeline, got %q", stdout)
	}
}

func TestMessageSyncAllReportsPerTopicFailure(t *testing.T) {
	dbPath := seedCache(t)

	dir := t.TempDir()
	if err := writeFakeGWS(t, dir, "#!/bin/sh\nexit 1\n"); err != nil {
		t.Fatalf("writing fake gws: %v", err)
	}

	stdout, err := runShared(t, dbPath, "message", "sync", "--all")
	if err != nil {
		t.Fatalf("message sync --all should collect per-topic failures, got %v", err)
	}
	if !strings.Contains(stdout, tagError) {
		t.Fatalf("a failing topic should be tagged %q, got %q", tagError, stdout)
	}
}

func TestCommandsReportAnUnopenableCache(t *testing.T) {
	// Every command has to open the cache before it can do anything, so a
	// path that cannot be opened must surface rather than panic.
	commands := [][]string{
		{"cache", "status"},
		{"event", "search"},
		{"event", "sync"},
		{"message", "export", "news"},
		{"message", "search"},
		{"message", "sync", "news"},
		{"topic", "add", "news"},
		{"topic", "list"},
		{"topic", "rm", "news"},
	}

	for _, args := range commands {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			cmd := NewRootCmd("1.2.3")
			cmd.SetOut(&strings.Builder{})
			cmd.SetErr(&strings.Builder{})
			cmd.SetArgs(append([]string{"--db", "/dev/null/nowhere/cache.db"}, args...))

			if err := cmd.Execute(); err == nil {
				t.Fatalf("%v should report that the cache cannot be opened", args)
			}
		})
	}
}

func TestProgressIsWrittenToStderr(t *testing.T) {
	fakeGWS(t)

	_, stderr, err := run(t, "message", "sync", "--query", "is:unread")
	if err != nil {
		t.Fatalf("message sync unexpected error: %v", err)
	}
	if !strings.Contains(stderr, "[gws-cache]") {
		t.Fatalf("human progress should be prefixed on stderr, got %q", stderr)
	}
}

func TestAgentProgressDropsThePrefix(t *testing.T) {
	fakeGWS(t)
	t.Setenv("AGENT", "1")

	_, stderr, err := run(t, "message", "sync", "--query", "is:unread")
	if err != nil {
		t.Fatalf("message sync unexpected error: %v", err)
	}
	if strings.Contains(stderr, "[gws-cache]") {
		t.Fatalf("agent progress should drop the decorative prefix, got %q", stderr)
	}
	if !strings.Contains(stderr, "Found 0 conversation") {
		t.Fatalf("agent progress should still report status, got %q", stderr)
	}
}

// gwsWithThreads installs a fake CLI that reports `count` conversations and
// serves a minimal detail for any one of them.
func gwsWithThreads(t *testing.T, count int) {
	t.Helper()

	var threads strings.Builder
	for i := range count {
		if i > 0 {
			threads.WriteString(",")
		}
		fmt.Fprintf(&threads, `{"id":"t%d","historyId":"%d"}`, i, 100+i)
	}

	script := fmt.Sprintf(`#!/bin/sh
case "$*" in
  *"threads list"*) echo '{"threads":[%s]}' ;;
  *"threads get"*)  echo '{"id":"t","historyId":"1","messages":[{"id":"m","internalDate":"1780000000000","payload":{"headers":[{"name":"From","value":"a@example.com"},{"name":"Subject","value":"s"}]}}]}' ;;
  *) echo '{}' ;;
esac
`, threads.String())

	if err := writeFakeGWS(t, t.TempDir(), script); err != nil {
		t.Fatalf("writing fake gws: %v", err)
	}
}

func TestMessageSyncWarnsOnStdoutWhenBudgetIsExhausted(t *testing.T) {
	// A caller reading only stdout must be able to see that the run was
	// partial; a stderr progress line is not enough.
	gwsWithThreads(t, 4)

	stdout, _, err := run(t, "message", "sync", "news", "--max-threads", "2")
	if err != nil {
		t.Fatalf("message sync unexpected error: %v", err)
	}

	if !strings.Contains(stdout, tagWarn) {
		t.Fatalf("stdout should carry %s when the budget is exhausted, got:\n%s", tagWarn, stdout)
	}
	if !strings.Contains(stdout, "2 remaining") {
		t.Fatalf("stdout should state how many conversations are outstanding, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "--max-threads") {
		t.Fatalf("stdout should tell the user how to collect the rest, got:\n%s", stdout)
	}
}

func TestMessageSyncIsSilentWhenComplete(t *testing.T) {
	gwsWithThreads(t, 2)

	stdout, _, err := run(t, "message", "sync", "news", "--max-threads", "10")
	if err != nil {
		t.Fatalf("message sync unexpected error: %v", err)
	}
	if strings.Contains(stdout, tagWarn) {
		t.Fatalf("a complete sync should not warn, got:\n%s", stdout)
	}
}

func TestAdHocSyncWarnsWhenBudgetIsExhausted(t *testing.T) {
	gwsWithThreads(t, 4)

	stdout, _, err := run(t, "message", "sync", "--query", "is:unread", "--max-threads", "1")
	if err != nil {
		t.Fatalf("message sync unexpected error: %v", err)
	}
	if !strings.Contains(stdout, tagWarn) {
		t.Fatalf("stdout should warn when the budget is exhausted, got:\n%s", stdout)
	}
}

func TestAgentModeWarningIsMachineReadable(t *testing.T) {
	gwsWithThreads(t, 4)
	t.Setenv("AGENT", "1")

	stdout, _, err := run(t, "message", "sync", "news", "--max-threads", "2")
	if err != nil {
		t.Fatalf("message sync unexpected error: %v", err)
	}
	if !strings.Contains(stdout, agentWarn) {
		t.Fatalf("agent output should tag the shortfall with %q, got:\n%s", agentWarn, stdout)
	}
}

func TestMessageSyncJSONCarriesTheBacklog(t *testing.T) {
	// A caller must be able to detect an incomplete run without matching on
	// prose, which is what refresh needs to decide whether to run again.
	gwsWithThreads(t, 4)

	stdout, _, err := run(t, "message", "sync", "news", "--max-threads", "2", "--format", "json")
	if err != nil {
		t.Fatalf("message sync unexpected error: %v", err)
	}

	var got struct {
		Target           string `json:"target"`
		ThreadsListed    int    `json:"threads_listed"`
		ThreadsFetched   int    `json:"threads_fetched"`
		ThreadsRemaining int    `json:"threads_remaining"`
		MessagesIngested int    `json:"messages_ingested"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("output is not valid JSON (%v): %s", err, stdout)
	}

	if got.Target != "news" {
		t.Fatalf("target = %q, want the topic that was synced", got.Target)
	}
	if got.ThreadsListed != 4 || got.ThreadsFetched != 2 || got.ThreadsRemaining != 2 {
		t.Fatalf("counts = %+v, want 4 listed, 2 fetched, 2 remaining", got)
	}
}

func TestMessageSyncJSONIsNotPollutedByStatusTags(t *testing.T) {
	gwsWithThreads(t, 4)

	stdout, _, err := run(t, "message", "sync", "news", "--max-threads", "2", "--format", "json")
	if err != nil {
		t.Fatalf("message sync unexpected error: %v", err)
	}
	for _, tag := range []string{tagWarn, tagOK, agentWarn, agentOK} {
		if strings.Contains(stdout, tag) {
			t.Fatalf("json output must not carry the %q status tag: %s", tag, stdout)
		}
	}
}

func TestMessageSyncJSONReportsCompletion(t *testing.T) {
	gwsWithThreads(t, 2)

	stdout, _, err := run(t, "message", "sync", "news", "--max-threads", "10", "--format", "json")
	if err != nil {
		t.Fatalf("message sync unexpected error: %v", err)
	}

	var got struct {
		ThreadsRemaining int `json:"threads_remaining"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("output is not valid JSON (%v): %s", err, stdout)
	}
	if got.ThreadsRemaining != 0 {
		t.Fatalf("threads_remaining = %d, want 0 for a complete run", got.ThreadsRemaining)
	}
}

func TestAdHocSyncJSON(t *testing.T) {
	gwsWithThreads(t, 2)

	stdout, _, err := run(t, "message", "sync", "--query", "is:unread", "--format", "json")
	if err != nil {
		t.Fatalf("message sync --query unexpected error: %v", err)
	}

	var got struct {
		Target string `json:"target"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("output is not valid JSON (%v): %s", err, stdout)
	}
	if got.Target != "is:unread" {
		t.Fatalf("target = %q, want the query that was synced", got.Target)
	}
}

func TestMessageSyncAllJSONIsAlwaysAnArray(t *testing.T) {
	gwsWithThreads(t, 1)

	// No topics at all still yields an array, so a caller never has to branch
	// on the shape of the document.
	stdout, _, err := run(t, "message", "sync", "--all", "--format", "json")
	if err != nil {
		t.Fatalf("message sync --all unexpected error: %v", err)
	}

	var got []map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("output is not a JSON array (%v): %s", err, stdout)
	}
	if len(got) != 0 {
		t.Fatalf("expected an empty array with no topics, got %v", got)
	}
}

func TestMessageSyncAllJSONReportsPerTopicFailure(t *testing.T) {
	dbPath := seedCache(t)
	if err := writeFakeGWS(t, t.TempDir(), "#!/bin/sh\nexit 1\n"); err != nil {
		t.Fatalf("writing fake gws: %v", err)
	}

	stdout, err := runShared(t, dbPath, "message", "sync", "--all", "--format", "json")
	if err != nil {
		t.Fatalf("message sync --all unexpected error: %v", err)
	}

	var got []struct {
		Target string `json:"target"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("output is not a JSON array (%v): %s", err, stdout)
	}
	if len(got) == 0 || got[0].Error == "" {
		t.Fatalf("a failing topic should carry its error in the array, got %+v", got)
	}
}

func TestEventSyncJSON(t *testing.T) {
	fakeGWS(t)

	stdout, _, err := run(t, "event", "sync", "--format", "json")
	if err != nil {
		t.Fatalf("event sync unexpected error: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("output is not valid JSON (%v): %s", err, stdout)
	}
	if _, ok := got["events_ingested"]; !ok {
		t.Fatalf("event sync json should report events_ingested, got %v", got)
	}
	// The delta cursor is internal bookkeeping, not something to hand out.
	if _, ok := got["sync_token"]; ok {
		t.Fatalf("event sync json must not expose the sync token: %v", got)
	}
}

func TestZeroTimeoutStillRuns(t *testing.T) {
	fakeGWS(t)

	if _, _, err := run(t, "--timeout", "0", "message", "sync", "--query", "is:unread"); err != nil {
		t.Fatalf("a zero timeout should mean no deadline, got %v", err)
	}
}
