package classifier

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultClassify(t *testing.T) {
	tests := []struct {
		name       string
		from       string
		snippet    string
		headers    map[string]string
		wantAuto   bool
		wantReason string
	}{
		{
			name:       "human sender",
			from:       "Dana Reed <dana@example.com>",
			snippet:    "Are you free to talk on Thursday?",
			wantAuto:   false,
			wantReason: ReasonHuman,
		},
		{
			name:       "auto submitted header",
			from:       "Dana Reed <dana@example.com>",
			headers:    map[string]string{"Auto-Submitted": "auto-generated"},
			wantAuto:   true,
			wantReason: "header:Auto-Submitted=auto-generated",
		},
		{
			name:     "auto submitted set to no is not automated",
			from:     "Dana Reed <dana@example.com>",
			headers:  map[string]string{"Auto-Submitted": "no"},
			wantAuto: false,
		},
		{
			name:     "empty auto submitted is not automated",
			from:     "Dana Reed <dana@example.com>",
			headers:  map[string]string{"Auto-Submitted": ""},
			wantAuto: false,
		},
		{
			name:       "list unsubscribe header",
			from:       "News <news@example.com>",
			headers:    map[string]string{"List-Unsubscribe": "<https://example.com/u>"},
			wantAuto:   true,
			wantReason: "header:List-Unsubscribe",
		},
		{
			name:     "no-reply sender",
			from:     "no-reply@example.com",
			wantAuto: true,
		},
		{
			name:     "noreply sender without hyphen",
			from:     "Vendor <messages-noreply@example.com>",
			wantAuto: true,
		},
		{
			name:     "notification sender",
			from:     "calendar-notification@google.com",
			wantAuto: true,
		},
		{
			name:     "do-not-reply sender",
			from:     "donotreply@example.com",
			wantAuto: true,
		},
		{
			name:     "mailer daemon sender",
			from:     "MAILER-DAEMON@example.com",
			wantAuto: true,
		},
		{
			name:     "nil headers are tolerated",
			from:     "dana@example.com",
			headers:  nil,
			wantAuto: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Default().Classify(tt.from, tt.snippet, tt.headers)
			if got.IsAutomated != tt.wantAuto {
				t.Fatalf("Classify(%q) IsAutomated = %v, want %v (reason %q)", tt.from, got.IsAutomated, tt.wantAuto, got.Reason)
			}
			if tt.wantReason != "" && got.Reason != tt.wantReason {
				t.Fatalf("Classify(%q) Reason = %q, want %q", tt.from, got.Reason, tt.wantReason)
			}
		})
	}
}

func TestDefaultRulesCarryNoVendorSpecificPatterns(t *testing.T) {
	// The cache is a generic layer; vendor names belong in user configuration,
	// not compiled into the binary.
	vendors := []string{"ashbyhq", "hellosign", "linkedin", "interview"}
	for _, pattern := range append(DefaultRules().SenderPatterns, DefaultRules().SnippetPatterns...) {
		for _, vendor := range vendors {
			if contains(pattern, vendor) {
				t.Fatalf("default rule %q contains vendor-specific token %q", pattern, vendor)
			}
		}
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func TestNewRejectsInvalidPattern(t *testing.T) {
	if _, err := New(Rules{SenderPatterns: []string{"("}}); err == nil {
		t.Fatal("New should reject an invalid regular expression")
	}
	if _, err := New(Rules{SnippetPatterns: []string{"("}}); err == nil {
		t.Fatal("New should reject an invalid snippet regular expression")
	}
}

func TestLoadMissingFileUsesDefaults(t *testing.T) {
	got, err := Load(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("Load unexpected error: %v", err)
	}
	if res := got.Classify("no-reply@example.com", "", nil); !res.IsAutomated {
		t.Fatal("Load should fall back to the default rules when no config file exists")
	}
}

func TestLoadMergesUserRules(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "classifier.json")
	body := `{"sender_patterns":["(?i)@vendor\\.example$"],"snippet_patterns":["(?i)has requested a signature"]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load unexpected error: %v", err)
	}

	if res := c.Classify("someone@vendor.example", "", nil); !res.IsAutomated {
		t.Fatal("user sender pattern was not applied")
	}
	if res := c.Classify("dana@example.com", "Dana has requested a signature", nil); !res.IsAutomated {
		t.Fatal("user snippet pattern was not applied")
	}
	if res := c.Classify("no-reply@example.com", "", nil); !res.IsAutomated {
		t.Fatal("default rules should still apply alongside user rules")
	}
	if res := c.Classify("dana@example.com", "lunch?", nil); res.IsAutomated {
		t.Fatal("unrelated human mail should stay human")
	}
}

func TestLoadRejectsMalformedConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "classifier.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("Load should report a malformed config file instead of silently ignoring it")
	}
}

func TestRulesPathFollowsXDG(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if got, want := RulesPath(), filepath.Join(dir, "gws-cache", "classifier.json"); got != want {
		t.Fatalf("RulesPath() = %q, want %q", got, want)
	}
}

func TestRulesPathFallsBackToTheUserConfigDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", home)

	got := RulesPath()
	if got == "" {
		t.Fatal("RulesPath() should always return a path")
	}
	if filepath.Base(got) != "classifier.json" {
		t.Fatalf("RulesPath() = %q, want it to end in classifier.json", got)
	}
	if !strings.HasPrefix(got, home) {
		t.Fatalf("RulesPath() = %q, want it under the home directory %q", got, home)
	}
}

func TestLoadReportsUnreadableFile(t *testing.T) {
	// A directory in place of the rules file is readable by stat but not by
	// ReadFile, which is the failure path a permissions problem would take.
	dir := filepath.Join(t.TempDir(), "classifier.json")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("creating directory: %v", err)
	}

	if _, err := Load(dir); err == nil {
		t.Fatal("Load should report a rules file it cannot read")
	}
}

func TestDefaultRulesCompile(t *testing.T) {
	// Default panics on a malformed built-in rule set, so this also asserts
	// the shipped defaults stay valid.
	if Default() == nil {
		t.Fatal("Default() returned nil")
	}
}
