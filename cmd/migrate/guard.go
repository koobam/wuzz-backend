package main

import (
	"fmt"
	"regexp"
	"strings"
)

// A deploy runs the migration while the previous version of the service is still serving, so
// a migration that removes something the old code uses breaks production for the minutes
// between the two. These are the statements that do that. Adding a table, a column or an
// index, and loosening or replacing a constraint, are not on the list.
var destructive = []struct {
	re   *regexp.Regexp
	what string
}{
	{regexp.MustCompile(`(?i)\bDROP\s+TABLE\b`), "drops a table"},
	{regexp.MustCompile(`(?i)\bDROP\s+COLUMN\b`), "drops a column"},
	{regexp.MustCompile(`(?i)\bRENAME\s+(TO|COLUMN)\b`), "renames a table or a column"},
	{regexp.MustCompile(`(?i)\bTRUNCATE\b`), "truncates a table"},
	{regexp.MustCompile(`(?i)\bALTER\s+COLUMN\s+\S+\s+(SET\s+DATA\s+)?TYPE\b`), "changes a column's type"},
}

// upSection returns the Up half of a goose migration, comments stripped.
func upSection(body string) string {
	up := strings.Index(body, "-- +goose Up")
	if up < 0 {
		return ""
	}
	body = body[up+len("-- +goose Up"):]
	if down := strings.Index(body, "-- +goose Down"); down >= 0 {
		body = body[:down]
	}
	var kept []string
	for _, line := range strings.Split(body, "\n") {
		if i := strings.Index(line, "--"); i >= 0 {
			line = line[:i]
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// destructiveUp lists what a migration's Up half would remove, in words.
func destructiveUp(body string) []string {
	up := upSection(body)
	var found []string
	for _, d := range destructive {
		if d.re.MatchString(up) {
			found = append(found, d.what)
		}
	}
	return found
}

// refuseDestructive turns a list of (file, findings) into the error `migrate up` returns.
func refuseDestructive(files map[string][]string) error {
	var b strings.Builder
	b.WriteString("migrate: refusing to run migrations that remove something the previous version may still use:\n")
	for file, what := range files {
		fmt.Fprintf(&b, "  %s: %s\n", file, strings.Join(what, ", "))
	}
	b.WriteString("Ship the code that stops using it first and drop it in a later deploy, or set MIGRATE_ALLOW_DESTRUCTIVE=true if this is safe.")
	return fmt.Errorf("%s", b.String())
}
