package classifier

import (
	"regexp"
)

type Result struct {
	IsAutomated bool
	Reason      string
}

var botSenderPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)no-?reply@`),
	regexp.MustCompile(`(?i)calendar-notification@google\.com`),
	regexp.MustCompile(`(?i)hellosign-noreply@`),
	regexp.MustCompile(`(?i)messages-noreply@linkedin\.com`),
	regexp.MustCompile(`(?i)notifications?@`),
	regexp.MustCompile(`(?i)no-?reply@ashbyhq\.com`),
}

var automatedSnippetPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)coming up in 24 hours`),
	regexp.MustCompile(`(?i)upcoming interview in 1 hour`),
	regexp.MustCompile(`(?i)This event isn't in your calendar yet`),
	regexp.MustCompile(`(?i)You have successfully signed your document`),
	regexp.MustCompile(`(?i)has requested a signature`),
	regexp.MustCompile(`(?i)Notification:`),
}

// Classify determines if an email message is automated/bot-generated or human.
func Classify(from, subject, snippet string, headers map[string]string) Result {
	if headers != nil {
		if autoVal, ok := headers["Auto-Submitted"]; ok && autoVal != "no" && autoVal != "" {
			return Result{IsAutomated: true, Reason: "HEADER:Auto-Submitted=" + autoVal}
		}
		if _, ok := headers["List-Unsubscribe"]; ok {
			return Result{IsAutomated: true, Reason: "HEADER:List-Unsubscribe"}
		}
	}

	for _, pattern := range botSenderPatterns {
		if pattern.MatchString(from) {
			return Result{IsAutomated: true, Reason: "SENDER_PATTERN:" + pattern.String()}
		}
	}

	for _, pattern := range automatedSnippetPatterns {
		if pattern.MatchString(snippet) {
			return Result{IsAutomated: true, Reason: "SNIPPET_PATTERN:" + pattern.String()}
		}
	}

	return Result{IsAutomated: false, Reason: "HUMAN_MATCH"}
}
