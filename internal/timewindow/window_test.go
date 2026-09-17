package timewindow

import (
	"testing"
	"time"
)

var reference = time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)

func TestParse(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantKind Kind
		wantDur  time.Duration
		wantAt   time.Time
		wantErr  bool
	}{
		{name: "empty is unbounded", input: "", wantKind: Unbounded},
		{name: "all is unbounded", input: "all", wantKind: Unbounded},
		{name: "ALL is case insensitive", input: "ALL", wantKind: Unbounded},
		{name: "hours", input: "24h", wantKind: Relative, wantDur: 24 * time.Hour},
		{name: "days", input: "30d", wantKind: Relative, wantDur: 30 * 24 * time.Hour},
		{name: "weeks", input: "4w", wantKind: Relative, wantDur: 4 * 7 * 24 * time.Hour},
		{name: "months", input: "2m", wantKind: Relative, wantDur: 2 * 30 * 24 * time.Hour},
		{name: "years", input: "1y", wantKind: Relative, wantDur: 365 * 24 * time.Hour},
		{name: "absolute date", input: "2026-06-01", wantKind: Absolute, wantAt: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)},
		{name: "unknown unit", input: "5q", wantErr: true},
		{name: "missing value", input: "w", wantErr: true},
		{name: "negative value", input: "-4w", wantErr: true},
		{name: "not a duration", input: "soon", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Parse(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if got.Kind != tt.wantKind {
				t.Fatalf("Parse(%q).Kind = %v, want %v", tt.input, got.Kind, tt.wantKind)
			}
			if got.Duration != tt.wantDur {
				t.Fatalf("Parse(%q).Duration = %v, want %v", tt.input, got.Duration, tt.wantDur)
			}
			if !got.At.Equal(tt.wantAt) {
				t.Fatalf("Parse(%q).At = %v, want %v", tt.input, got.At, tt.wantAt)
			}
		})
	}
}

func TestKindString(t *testing.T) {
	tests := []struct {
		name string
		kind Kind
		want string
	}{
		{name: "unbounded", kind: Unbounded, want: "unbounded"},
		{name: "relative", kind: Relative, want: "relative"},
		{name: "absolute", kind: Absolute, want: "absolute"},
		{name: "out of range", kind: Kind(99), want: "invalid"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.kind.String(); got != tt.want {
				t.Fatalf("Kind(%d).String() = %q, want %q", tt.kind, got, tt.want)
			}
		})
	}
}

func TestWindowStart(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    time.Time
		wantSet bool
	}{
		{name: "all has no lower bound", input: "all", wantSet: false},
		{name: "relative counts back from now", input: "4w", want: reference.Add(-4 * 7 * 24 * time.Hour), wantSet: true},
		{name: "absolute is used verbatim", input: "2026-06-01", want: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), wantSet: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, err := Parse(tt.input)
			if err != nil {
				t.Fatalf("Parse(%q) unexpected error: %v", tt.input, err)
			}
			got, ok := w.Start(reference)
			if ok != tt.wantSet {
				t.Fatalf("Start ok = %v, want %v", ok, tt.wantSet)
			}
			if tt.wantSet && !got.Equal(tt.want) {
				t.Fatalf("Start = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWindowEnd(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    time.Time
		wantSet bool
	}{
		{name: "all has no upper bound", input: "all", wantSet: false},
		{name: "relative counts forward from now", input: "6w", want: reference.Add(6 * 7 * 24 * time.Hour), wantSet: true},
		{name: "absolute is used verbatim", input: "2026-12-01", want: time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC), wantSet: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, err := Parse(tt.input)
			if err != nil {
				t.Fatalf("Parse(%q) unexpected error: %v", tt.input, err)
			}
			got, ok := w.End(reference)
			if ok != tt.wantSet {
				t.Fatalf("End ok = %v, want %v", ok, tt.wantSet)
			}
			if tt.wantSet && !got.Equal(tt.want) {
				t.Fatalf("End = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStartUnix(t *testing.T) {
	w, err := Parse("all")
	if err != nil {
		t.Fatalf("Parse unexpected error: %v", err)
	}
	if got, ok := w.StartUnix(reference); ok || got != 0 {
		t.Fatalf("StartUnix for unbounded = (%d, %v), want (0, false)", got, ok)
	}

	w, err = Parse("1d")
	if err != nil {
		t.Fatalf("Parse unexpected error: %v", err)
	}
	got, ok := w.StartUnix(reference)
	if !ok {
		t.Fatal("StartUnix for relative window should be set")
	}
	if want := reference.Add(-24 * time.Hour).Unix(); got != want {
		t.Fatalf("StartUnix = %d, want %d", got, want)
	}
}
