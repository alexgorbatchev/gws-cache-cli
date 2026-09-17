// Package classifier separates automated mail (notifications, bulk lists,
// transactional receipts) from mail written by a person.
package classifier

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Reason values reported when no rule matched, or when a standards-based mail
// header settled the classification on its own.
const (
	ReasonHuman             = "human"
	reasonHeaderPrefix      = "header:"
	reasonSenderPrefix      = "sender:"
	reasonSnippetPrefix     = "snippet:"
	headerAutoSubmitted     = "Auto-Submitted"
	headerListUnsubscribe   = "List-Unsubscribe"
	autoSubmittedSuppressed = "no"
)

// Result is the outcome of classifying a single message.
type Result struct {
	IsAutomated bool
	Reason      string
}

// Rules is the serialized form of the pattern sets, both for the built-in
// defaults and for the optional user configuration file.
type Rules struct {
	SenderPatterns  []string `json:"sender_patterns"`
	SnippetPatterns []string `json:"snippet_patterns"`
}

// DefaultRules returns the built-in rules. They are deliberately generic:
// anything naming a specific vendor or product belongs in the user's own
// configuration file, not in a tool that ships to everyone.
func DefaultRules() Rules {
	return Rules{
		SenderPatterns: []string{
			`(?i)no-?reply@`,
			`(?i)do-?not-?reply@`,
			`(?i)notifications?@`,
			`(?i)mailer-daemon@`,
			`(?i)postmaster@`,
		},
	}
}

// Classifier applies a compiled set of rules to messages.
type Classifier struct {
	senderPatterns  []*regexp.Regexp
	snippetPatterns []*regexp.Regexp
}

// New compiles rules into a Classifier, reporting any invalid expression
// rather than silently dropping it.
func New(rules Rules) (*Classifier, error) {
	senders, err := compileAll(rules.SenderPatterns)
	if err != nil {
		return nil, fmt.Errorf("compiling sender patterns: %w", err)
	}
	snippets, err := compileAll(rules.SnippetPatterns)
	if err != nil {
		return nil, fmt.Errorf("compiling snippet patterns: %w", err)
	}
	return &Classifier{senderPatterns: senders, snippetPatterns: snippets}, nil
}

func compileAll(patterns []string) ([]*regexp.Regexp, error) {
	compiled := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("pattern %q: %w", p, err)
		}
		compiled = append(compiled, re)
	}
	return compiled, nil
}

// Default returns a Classifier using only the built-in rules. The default
// rules are known-good, so a compile failure here is a programmer error.
func Default() *Classifier {
	c, err := New(DefaultRules())
	if err != nil {
		panic(fmt.Sprintf("classifier: built-in rules do not compile: %v", err))
	}
	return c
}

// RulesPath returns the XDG location of the optional user rules file.
func RulesPath() string {
	if xdgConfig := os.Getenv("XDG_CONFIG_HOME"); xdgConfig != "" {
		return filepath.Join(xdgConfig, "gws-cache", "classifier.json")
	}
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "gws-cache", "classifier.json")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".config", "gws-cache", "classifier.json")
	}
	return filepath.Join(".", "classifier.json")
}

// Load reads additional rules from path and appends them to the defaults. A
// missing file is not an error: it simply means the user has not customized
// anything. A malformed file is an error, because silently ignoring it would
// hide a misconfiguration behind wrong classifications.
func Load(path string) (*Classifier, error) {
	rules := DefaultRules()

	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return New(rules)
	case err != nil:
		return nil, fmt.Errorf("reading classifier rules %s: %w", path, err)
	}

	var userRules Rules
	if err := json.Unmarshal(data, &userRules); err != nil {
		return nil, fmt.Errorf("parsing classifier rules %s: %w", path, err)
	}

	rules.SenderPatterns = append(rules.SenderPatterns, userRules.SenderPatterns...)
	rules.SnippetPatterns = append(rules.SnippetPatterns, userRules.SnippetPatterns...)
	return New(rules)
}

// Classify decides whether a message was machine generated. Standards-based
// headers are checked first because they are declared by the sender and are
// far more reliable than any pattern match.
func (c *Classifier) Classify(from, snippet string, headers map[string]string) Result {
	if value, ok := headers[headerAutoSubmitted]; ok && value != "" && !strings.EqualFold(value, autoSubmittedSuppressed) {
		return Result{IsAutomated: true, Reason: reasonHeaderPrefix + headerAutoSubmitted + "=" + value}
	}
	if _, ok := headers[headerListUnsubscribe]; ok {
		return Result{IsAutomated: true, Reason: reasonHeaderPrefix + headerListUnsubscribe}
	}

	for _, pattern := range c.senderPatterns {
		if pattern.MatchString(from) {
			return Result{IsAutomated: true, Reason: reasonSenderPrefix + pattern.String()}
		}
	}

	for _, pattern := range c.snippetPatterns {
		if pattern.MatchString(snippet) {
			return Result{IsAutomated: true, Reason: reasonSnippetPrefix + pattern.String()}
		}
	}

	return Result{IsAutomated: false, Reason: ReasonHuman}
}
