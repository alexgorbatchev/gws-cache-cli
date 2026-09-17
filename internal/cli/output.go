package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	cobrahelptree "github.com/alexgorbatchev/cobra-help-tree"
	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
)

// Status tags used instead of emoji, which are prohibited in both modes.
const (
	tagOK     = "[OK]"
	tagWarn   = "[WARN]"
	tagError  = "[ERROR]"
	agentOK   = "OK:"
	agentWarn = "WARN:"
	agentErr  = "ERR:"
)

// dateDisplayLayout is how dates are rendered for people; agent mode keeps the
// original RFC 3339 timestamps so they stay machine readable.
const dateDisplayLayout = "Jan 02, 2006"

// field is one labelled value in a record.
type field struct {
	Key   string
	Value string
}

// record is an ordered set of fields describing a single row.
type record []field

// output renders results in one of two modes. Human mode aims for a readable
// terminal display; agent mode (AGENT=1) aims for the fewest tokens that stay
// machine parseable, with no tables, dividers or alignment padding.
type output struct {
	out   io.Writer
	err   io.Writer
	agent bool
}

// newOutput builds an output bound to a command's streams, so that tests and
// shell redirection both see the right thing.
func newOutput(cmd *cobra.Command) *output {
	return &output{
		out:   cmd.OutOrStdout(),
		err:   cmd.ErrOrStderr(),
		agent: cobrahelptree.IsAgentMode(),
	}
}

// printf writes a line to stdout.
func (o *output) printf(format string, args ...any) {
	fmt.Fprintf(o.out, format+"\n", args...)
}

// progress writes a status line to stderr, where it cannot corrupt piped
// results. Agent mode drops the decorative prefix.
func (o *output) progress(format string, args ...any) {
	prefix := "[gws-cache] "
	if o.agent {
		prefix = ""
	}
	fmt.Fprintf(o.err, prefix+format+"\n", args...)
}

// ok reports a successful outcome.
func (o *output) ok(format string, args ...any) {
	tag := tagOK
	if o.agent {
		tag = agentOK
	}
	fmt.Fprintf(o.out, tag+" "+format+"\n", args...)
}

// warn reports an incomplete result on stdout, beside the result it qualifies,
// so that a caller reading only stdout cannot mistake a partial run for a
// finished one.
func (o *output) warn(format string, args ...any) {
	tag := tagWarn
	if o.agent {
		tag = agentWarn
	}
	fmt.Fprintf(o.out, tag+" "+format+"\n", args...)
}

// failure reports a per-item failure that did not stop the command.
func (o *output) failure(format string, args ...any) {
	tag := tagError
	if o.agent {
		tag = agentErr
	}
	fmt.Fprintf(o.out, tag+" "+format+"\n", args...)
}

// records renders rows as an aligned table for people, or as one flat
// tab-separated key:value line per row for agents.
func (o *output) records(rows []record) error {
	if len(rows) == 0 {
		return nil
	}

	if o.agent {
		for _, row := range rows {
			pairs := make([]string, 0, len(row))
			for _, f := range row {
				pairs = append(pairs, f.Key+":"+f.Value)
			}
			fmt.Fprintln(o.out, strings.Join(pairs, "\t"))
		}
		return nil
	}

	headers := make([]string, 0, len(rows[0]))
	for _, f := range rows[0] {
		headers = append(headers, strings.ToUpper(f.Key))
	}

	table := tablewriter.NewTable(o.out, tablewriter.WithMaxWidth(tableWidth()))
	table.Header(headers)
	for _, row := range rows {
		cells := make([]string, 0, len(row))
		for _, f := range row {
			cells = append(cells, f.Value)
		}
		if err := table.Append(cells); err != nil {
			return fmt.Errorf("rendering table row: %w", err)
		}
	}
	return table.Render()
}

// fallbackWidth is used when stdout is not a terminal and no width can be
// detected, so that piped output stays a predictable shape.
const fallbackWidth = 100

// tableWidth keeps a table inside the terminal, so that wide values wrap
// instead of breaking the column alignment.
func tableWidth() int {
	if width := cobrahelptree.GetTerminalWidth(); width > 0 {
		return width
	}
	return fallbackWidth
}

// emitJSON writes a value as indented JSON, which is identical in both modes
// because it is already machine readable.
func (o *output) emitJSON(v any) error {
	encoded, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding json output: %w", err)
	}
	fmt.Fprintln(o.out, string(encoded))
	return nil
}

// formatDate renders an RFC 3339 timestamp for display. Agent mode keeps the
// original value. A value that does not parse is passed through untouched
// rather than sliced, which would corrupt it.
func (o *output) formatDate(iso string) string {
	if o.agent {
		return iso
	}
	parsed, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return iso
	}
	return parsed.Format(dateDisplayLayout)
}

// formatTime renders a timestamp for display, keeping RFC 3339 for agents.
func (o *output) formatTime(t time.Time, layout string) string {
	if o.agent {
		return t.Format(time.RFC3339)
	}
	return t.Format(layout)
}
