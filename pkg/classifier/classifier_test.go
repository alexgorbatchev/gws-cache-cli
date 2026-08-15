package classifier

import "testing"

func TestClassify(t *testing.T) {
	tests := []struct {
		name        string
		from        string
		subject     string
		snippet     string
		headers     map[string]string
		wantAuto    bool
		wantReason  string
	}{
		{
			name:       "Human Recruiter Email",
			from:       "Leither Moise <leither.moise@envoy.com>",
			subject:    "Alex's Recruiter Phone Call Availability with Envoy!",
			snippet:    "Hi Alex, Thank you so much for your interest",
			headers:    nil,
			wantAuto:   false,
			wantReason: "HUMAN_MATCH",
		},
		{
			name:       "Header Auto-Submitted",
			from:       "user@example.com",
			subject:    "Auto response",
			snippet:    "Out of office",
			headers:    map[string]string{"Auto-Submitted": "auto-replied"},
			wantAuto:   true,
			wantReason: "HEADER:Auto-Submitted=auto-replied",
		},
		{
			name:       "Header List-Unsubscribe",
			from:       "news@newsletter.com",
			subject:    "Weekly digest",
			snippet:    "Read our updates",
			headers:    map[string]string{"List-Unsubscribe": "<mailto:unsub@example.com>"},
			wantAuto:   true,
			wantReason: "HEADER:List-Unsubscribe",
		},
		{
			name:       "Bot Sender Ashby",
			from:       "Envoy Recruiting Team <no-reply@ashbyhq.com>",
			subject:    "Reminder: Interview",
			snippet:    "Hi Alex!",
			headers:    nil,
			wantAuto:   true,
			wantReason: "SENDER_PATTERN:(?i)no-?reply@",
		},
		{
			name:       "Bot Sender Google Calendar",
			from:       "Google Calendar <calendar-notification@google.com>",
			subject:    "Notification: Screen",
			snippet:    "Meeting notification",
			headers:    nil,
			wantAuto:   true,
			wantReason: "SENDER_PATTERN:(?i)calendar-notification@google\\.com",
		},
		{
			name:       "Automated Snippet Match",
			from:       "Recruiting <team@envoy.com>",
			subject:    "Reminder",
			snippet:    "This event isn't in your calendar yet",
			headers:    nil,
			wantAuto:   true,
			wantReason: "SNIPPET_PATTERN:(?i)This event isn't in your calendar yet",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Classify(tt.from, tt.subject, tt.snippet, tt.headers)
			if got.IsAutomated != tt.wantAuto {
				t.Fatalf("Classify() IsAutomated = %v, want %v (reason: %s)", got.IsAutomated, tt.wantAuto, got.Reason)
			}
		})
	}
}
