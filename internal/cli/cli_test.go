package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// run executes the command tree against a scratch cache and returns whatever
// it wrote to stdout and stderr.
func run(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()

	cmd := NewRootCmd("1.2.3")
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(append([]string{"--db", filepath.Join(t.TempDir(), "cache.db")}, args...))

	err = cmd.ExecuteContext(context.Background())
	return out.String(), errOut.String(), err
}

// runShared executes against a cache path the caller controls, so several
// commands can operate on the same data. Only stdout is returned: progress
// lines go to stderr precisely so they cannot corrupt piped results, and
// merging the two here would hide that.
func runShared(t *testing.T, dbPath string, args ...string) (stdout string, err error) {
	t.Helper()

	cmd := NewRootCmd("1.2.3")
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(append([]string{"--db", dbPath}, args...))

	err = cmd.ExecuteContext(context.Background())
	return out.String(), err
}

// writeFakeGWS installs a stand-in Google Workspace CLI in dir and puts it at
// the front of PATH, so command wiring can be exercised without a network.
func writeFakeGWS(t *testing.T, dir, script string) error {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake executable is a POSIX shell script")
	}

	if err := os.WriteFile(filepath.Join(dir, "gws"), []byte(script), 0o755); err != nil {
		return err
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return nil
}

// fakeGWS installs a stand-in that returns an empty result for every call.
func fakeGWS(t *testing.T) {
	t.Helper()
	if err := writeFakeGWS(t, t.TempDir(), "#!/bin/sh\necho '{}'\n"); err != nil {
		t.Fatalf("writing fake gws: %v", err)
	}
}

func TestVersionPrintsOnlyTheVersion(t *testing.T) {
	stdout, _, err := run(t, "--version")
	if err != nil {
		t.Fatalf("--version unexpected error: %v", err)
	}
	if stdout != "1.2.3\n" {
		t.Fatalf("--version printed %q, want exactly the version string", stdout)
	}
}

func TestCommandTreeIsSubjectFirst(t *testing.T) {
	// Every verb must live under the subject it acts on, at most three levels
	// deep including the program name.
	subjects := map[string][]string{
		"cache":   {"status"},
		"event":   {"search", "sync"},
		"message": {"export", "search", "sync"},
		"topic":   {"add", "list", "rm"},
	}

	root := NewRootCmd("1.2.3")
	for subject, verbs := range subjects {
		t.Run(subject, func(t *testing.T) {
			found, _, err := root.Find([]string{subject})
			if err != nil || found.Name() != subject {
				t.Fatalf("subject %q is missing from the command tree: %v", subject, err)
			}
			for _, verb := range verbs {
				cmd, _, err := root.Find([]string{subject, verb})
				if err != nil || cmd.Name() != verb {
					t.Fatalf("%s %s is missing from the command tree: %v", subject, verb, err)
				}
				if cmd.HasSubCommands() {
					t.Fatalf("%s %s nests deeper than three levels", subject, verb)
				}
			}
		})
	}
}

func TestNoTopLevelVerbs(t *testing.T) {
	// The pre-restructure spellings were verbs at the root; they must not
	// come back.
	verbs := []string{"sync", "scan", "search", "export", "status"}

	root := NewRootCmd("1.2.3")
	for _, verb := range verbs {
		for _, cmd := range root.Commands() {
			if cmd.Name() == verb {
				t.Fatalf("%q is a verb at the top level; it belongs under a subject", verb)
			}
		}
	}
}

func TestHelpRendersATree(t *testing.T) {
	stdout, _, err := run(t, "--help")
	if err != nil {
		t.Fatalf("--help unexpected error: %v", err)
	}
	if !strings.Contains(stdout, "├─") && !strings.Contains(stdout, "╰─") {
		t.Fatalf("help output should render a command tree, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "topic") || !strings.Contains(stdout, "sync") {
		t.Fatalf("help output should list subjects and their verbs, got:\n%s", stdout)
	}
}

func TestSubcommandHelpRendersItsSubtree(t *testing.T) {
	stdout, _, err := run(t, "topic", "--help")
	if err != nil {
		t.Fatalf("topic --help unexpected error: %v", err)
	}
	for _, verb := range []string{"add", "list", "rm"} {
		if !strings.Contains(stdout, verb) {
			t.Fatalf("topic --help should show the full subtree, missing %q in:\n%s", verb, stdout)
		}
	}
}

func TestAgentModeChangesHelpOutput(t *testing.T) {
	human, _, err := run(t, "--help")
	if err != nil {
		t.Fatalf("--help unexpected error: %v", err)
	}

	t.Setenv("AGENT", "1")
	agent, _, err := run(t, "--help")
	if err != nil {
		t.Fatalf("--help under AGENT=1 unexpected error: %v", err)
	}

	if human == agent {
		t.Fatal("AGENT=1 should produce different help output from human mode")
	}
	if strings.Contains(agent, "├─") || strings.Contains(agent, "╰─") {
		t.Fatalf("agent help should not draw tree glyphs, got:\n%s", agent)
	}
}

func TestTopicListHumanModeRendersATable(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "cache.db")
	if _, err := runShared(t, dbPath, "topic", "add", "newsletters", "--name", "Tech Newsletters", "--query", "category:promotions"); err != nil {
		t.Fatalf("topic add unexpected error: %v", err)
	}

	stdout, err := runShared(t, dbPath, "topic", "list")
	if err != nil {
		t.Fatalf("topic list unexpected error: %v", err)
	}
	if !strings.Contains(stdout, "SLUG") || !strings.Contains(stdout, "newsletters") {
		t.Fatalf("human output should be a headed table, got:\n%s", stdout)
	}
	if !strings.ContainsAny(stdout, "│┌└├─") {
		t.Fatalf("human output should use table borders, got:\n%s", stdout)
	}
}

func TestTopicListAgentModeIsFlatKeyValues(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "cache.db")
	if _, err := runShared(t, dbPath, "topic", "add", "newsletters", "--name", "Tech Newsletters"); err != nil {
		t.Fatalf("topic add unexpected error: %v", err)
	}

	t.Setenv("AGENT", "1")
	stdout, err := runShared(t, dbPath, "topic", "list")
	if err != nil {
		t.Fatalf("topic list unexpected error: %v", err)
	}

	if strings.ContainsAny(stdout, "│┌└") {
		t.Fatalf("agent output must not draw tables, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "slug:newsletters") {
		t.Fatalf("agent output should be flat key:value pairs, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "\t") {
		t.Fatalf("agent output should separate pairs with tabs, got:\n%q", stdout)
	}
}

func TestOutputContainsNoEmoji(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "cache.db")

	for _, agent := range []string{"", "1"} {
		t.Setenv("AGENT", agent)
		stdout, err := runShared(t, dbPath, "topic", "add", "billing")
		if err != nil {
			t.Fatalf("topic add unexpected error: %v", err)
		}
		for _, r := range stdout {
			if r > 0x2100 && !strings.ContainsRune("│┌┐└┘├┤┬┴┼─╭╮╰╯━┃", r) {
				t.Fatalf("output contains a non-text symbol %q (AGENT=%q): %s", r, agent, stdout)
			}
		}
		if err := os.Remove(dbPath); err != nil {
			t.Fatalf("clearing cache: %v", err)
		}
	}
}

func TestCacheStatusReportsCounts(t *testing.T) {
	t.Setenv("AGENT", "1")
	stdout, _, err := run(t, "cache", "status")
	if err != nil {
		t.Fatalf("cache status unexpected error: %v", err)
	}
	for _, want := range []string{"topics:0", "conversations:0", "messages:0"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("cache status output %q missing %q", stdout, want)
		}
	}
}

func TestMessageSyncRequiresATarget(t *testing.T) {
	_, _, err := run(t, "message", "sync")
	if err == nil {
		t.Fatal("message sync without a topic, --all or --query should be rejected")
	}
	for _, want := range []string{"--all", "--query"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q should mention %s", err, want)
		}
	}
}

func TestMessageSyncRejectsAmbiguousTargets(t *testing.T) {
	tests := [][]string{
		{"message", "sync", "newsletters", "--all"},
		{"message", "sync", "newsletters", "--query", "is:unread"},
		{"message", "sync", "--all", "--query", "is:unread"},
	}

	for _, args := range tests {
		t.Run(strings.Join(args[2:], " "), func(t *testing.T) {
			if _, _, err := run(t, args...); err == nil {
				t.Fatalf("%v names more than one thing to sync and should be rejected", args)
			}
		})
	}
}

func TestMessageSyncAcceptsAnAdHocQuery(t *testing.T) {
	// The old "inbox scan" is now a one-off query that registers nothing.
	fakeGWS(t)

	stdout, _, err := run(t, "message", "sync", "--query", "is:unread")
	if err != nil {
		t.Fatalf("message sync --query unexpected error: %v", err)
	}
	if !strings.Contains(stdout, "is:unread") {
		t.Fatalf("output should name the query it synced, got %q", stdout)
	}
}

func TestAdHocSyncRegistersNoTopic(t *testing.T) {
	fakeGWS(t)

	dbPath := filepath.Join(t.TempDir(), "cache.db")
	if _, err := runShared(t, dbPath, "message", "sync", "--query", "is:unread"); err != nil {
		t.Fatalf("message sync --query unexpected error: %v", err)
	}

	stdout, err := runShared(t, dbPath, "topic", "list")
	if err != nil {
		t.Fatalf("topic list unexpected error: %v", err)
	}
	if !strings.Contains(stdout, "No topics tracked yet") {
		t.Fatalf("a one-off query must not create a topic, got:\n%s", stdout)
	}
}

func TestInvalidWindowIsReported(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "message sync", args: []string{"message", "sync", "news", "--since", "soon"}},
		{name: "message export", args: []string{"message", "export", "news", "--since", "soon"}},
		{name: "message search", args: []string{"message", "search", "--since", "soon"}},
		{name: "event sync past", args: []string{"event", "sync", "--past", "soon"}},
		{name: "event sync future", args: []string{"event", "sync", "--future", "soon"}},
		{name: "event search", args: []string{"event", "search", "--since", "soon"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := run(t, tt.args...); err == nil {
				t.Fatalf("%v should reject an unparseable window", tt.args)
			}
		})
	}
}

func TestMessageSearchReportsNoMatches(t *testing.T) {
	stdout, _, err := run(t, "message", "search", "anything")
	if err != nil {
		t.Fatalf("message search unexpected error: %v", err)
	}
	if !strings.Contains(stdout, "No saved email matches") {
		t.Fatalf("expected an empty-result message, got %q", stdout)
	}
}

func TestMessageSearchJSON(t *testing.T) {
	stdout, _, err := run(t, "message", "search", "--format", "json")
	if err != nil {
		t.Fatalf("message search unexpected error: %v", err)
	}
	if strings.Contains(stdout, "[") && !strings.Contains(stdout, "No saved email matches") {
		t.Fatalf("unexpected output %q", stdout)
	}
}

func TestEventSearchReportsNoMatches(t *testing.T) {
	stdout, _, err := run(t, "event", "search")
	if err != nil {
		t.Fatalf("event search unexpected error: %v", err)
	}
	if !strings.Contains(stdout, "No saved calendar entries match") {
		t.Fatalf("expected an empty-result message, got %q", stdout)
	}
}

func TestMessageExportUnknownTopicFails(t *testing.T) {
	if _, _, err := run(t, "message", "export", "nonexistent"); err == nil {
		t.Fatal("exporting an unknown topic should fail")
	}
}

func TestTopicRemoveUnknownTopicFails(t *testing.T) {
	if _, _, err := run(t, "topic", "rm", "nonexistent"); err == nil {
		t.Fatal("removing an unknown topic should fail")
	}
}

func TestTopicListEmpty(t *testing.T) {
	stdout, _, err := run(t, "topic", "list")
	if err != nil {
		t.Fatalf("topic list unexpected error: %v", err)
	}
	if !strings.Contains(stdout, "No topics tracked yet") {
		t.Fatalf("expected an empty-result message, got %q", stdout)
	}
}

func TestMessageSyncAllWithNoTopics(t *testing.T) {
	fakeGWS(t)

	stdout, _, err := run(t, "message", "sync", "--all")
	if err != nil {
		t.Fatalf("message sync --all unexpected error: %v", err)
	}
	if !strings.Contains(stdout, "No topics tracked yet") {
		t.Fatalf("expected an empty-result message, got %q", stdout)
	}
}

func TestEventSyncRunsEndToEnd(t *testing.T) {
	fakeGWS(t)

	stdout, _, err := run(t, "event", "sync")
	if err != nil {
		t.Fatalf("event sync unexpected error: %v", err)
	}
	if !strings.Contains(stdout, "0 entries saved") {
		t.Fatalf("expected a sync summary, got %q", stdout)
	}
}

func TestMessageSyncReportsGWSFailure(t *testing.T) {
	if err := writeFakeGWS(t, t.TempDir(), "#!/bin/sh\nexit 1\n"); err != nil {
		t.Fatalf("writing fake gws: %v", err)
	}

	if _, _, err := run(t, "message", "sync", "news"); err == nil {
		t.Fatal("message sync should report a failure from the gws CLI")
	}
}

func TestUnreadableClassifierRulesAreReported(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configDir)
	if err := os.MkdirAll(filepath.Join(configDir, "gws-cache"), 0o755); err != nil {
		t.Fatalf("creating config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "gws-cache", "classifier.json"), []byte("{oops"), 0o600); err != nil {
		t.Fatalf("writing rules: %v", err)
	}

	// Only the commands that classify incoming mail read the rules file.
	fakeGWS(t)
	if _, _, err := run(t, "message", "sync", "--query", "is:unread"); err == nil {
		t.Fatal("a malformed classifier rules file should be reported, not ignored")
	}
}
