package api

import (
	"bytes"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/commands"
)

// --- helpers ----------------------------------------------------------------

// hdr asserts a header criterion is present. textproto.MIMEHeader canonicalises
// keys on Add (FROM -> From), so we look up the canonical form; go-imap's
// Format() upper-cases it back to FROM on the wire.
func hdr(t *testing.T, c *imap.SearchCriteria, key, want string) {
	t.Helper()
	vals := c.Header[textproto.CanonicalMIMEHeaderKey(key)]
	for _, v := range vals {
		if v == want {
			return
		}
	}
	t.Fatalf("Header[%q] = %v; want to contain %q", key, vals, want)
}

// orTerms returns the values of every free-text term in c.Or, asserting each is
// a Subject/From pair carrying the same value.
func orTerms(t *testing.T, c *imap.SearchCriteria) []string {
	t.Helper()
	var out []string
	for _, pair := range c.Or {
		hdr(t, pair[0], "SUBJECT", pair[0].Header.Get("Subject"))
		v := pair[0].Header.Get("Subject")
		hdr(t, pair[1], "FROM", v)
		out = append(out, v)
	}
	return out
}

func hasFlag(flags []string, f string) bool {
	for _, x := range flags {
		if x == f {
			return true
		}
	}
	return false
}

// encodeSearch renders the criteria to the exact bytes lilmail would put on the
// IMAP wire for a SEARCH command, so tests can assert the serialised form.
func encodeSearch(t *testing.T, c *imap.SearchCriteria) string {
	t.Helper()
	var buf bytes.Buffer
	cmd := (&commands.Search{Criteria: c}).Command()
	cmd.Tag = "A001"
	if err := cmd.WriteTo(imap.NewWriter(&buf)); err != nil {
		t.Fatalf("encode search: %v", err)
	}
	return buf.String()
}

// --- operator mapping -------------------------------------------------------

func TestParse_HeaderOperators(t *testing.T) {
	c, folder := parseSearchQuery(`from:alice to:bob cc:carol bcc:dave subject:report`)
	if folder != "" {
		t.Fatalf("unexpected folder override %q", folder)
	}
	hdr(t, c, "FROM", "alice")
	hdr(t, c, "TO", "bob")
	hdr(t, c, "CC", "carol")
	hdr(t, c, "BCC", "dave")
	hdr(t, c, "SUBJECT", "report")
}

func TestParse_HasAttachment(t *testing.T) {
	c, _ := parseSearchQuery(`has:attachment`)
	hdr(t, c, "Content-Type", "multipart")
}

func TestParse_IsFlags(t *testing.T) {
	c, _ := parseSearchQuery(`is:unread`)
	if !hasFlag(c.WithoutFlags, imap.SeenFlag) {
		t.Fatalf("is:unread -> WithoutFlags = %v; want \\Seen", c.WithoutFlags)
	}
	c, _ = parseSearchQuery(`is:read`)
	if !hasFlag(c.WithFlags, imap.SeenFlag) {
		t.Fatalf("is:read -> WithFlags = %v; want \\Seen", c.WithFlags)
	}
	c, _ = parseSearchQuery(`is:starred`)
	if !hasFlag(c.WithFlags, imap.FlaggedFlag) {
		t.Fatalf("is:starred -> WithFlags = %v; want \\Flagged", c.WithFlags)
	}
	c, _ = parseSearchQuery(`is:unstarred`)
	if !hasFlag(c.WithoutFlags, imap.FlaggedFlag) {
		t.Fatalf("is:unstarred -> WithoutFlags = %v; want \\Flagged", c.WithoutFlags)
	}
}

func TestParse_InFolder(t *testing.T) {
	_, folder := parseSearchQuery(`in:Archive from:alice`)
	if folder != "Archive" {
		t.Fatalf("folder override = %q; want Archive", folder)
	}
	// inbox alias normalises to INBOX.
	_, folder = parseSearchQuery(`in:inbox`)
	if folder != "INBOX" {
		t.Fatalf("in:inbox -> %q; want INBOX", folder)
	}
}

func TestParse_Dates(t *testing.T) {
	c, _ := parseSearchQuery(`before:2026/01/15 after:2025-12-01`)
	if c.Before.IsZero() || c.Before.Year() != 2026 || c.Before.Month() != time.January || c.Before.Day() != 15 {
		t.Fatalf("before -> %v", c.Before)
	}
	if c.Since.IsZero() || c.Since.Year() != 2025 || c.Since.Month() != time.December || c.Since.Day() != 1 {
		t.Fatalf("after -> %v", c.Since)
	}
}

func TestParse_RelativeDates(t *testing.T) {
	c, _ := parseSearchQuery(`newer_than:7d`)
	if c.Since.IsZero() {
		t.Fatalf("newer_than:7d did not set Since")
	}
	if d := time.Since(c.Since); d < 6*24*time.Hour || d > 8*24*time.Hour {
		t.Fatalf("newer_than:7d Since=%v (delta %v) not ~7d", c.Since, d)
	}
	c, _ = parseSearchQuery(`older_than:1y`)
	if c.Before.IsZero() {
		t.Fatalf("older_than:1y did not set Before")
	}
	if !c.Before.Before(time.Now().AddDate(0, 0, -300)) {
		t.Fatalf("older_than:1y Before=%v not ~1y ago", c.Before)
	}
}

// --- phrase, negation, combination -----------------------------------------

func TestParse_QuotedPhrase(t *testing.T) {
	c, _ := parseSearchQuery(`"quarterly report"`)
	if got := orTerms(t, c); len(got) != 1 || got[0] != "quarterly report" || len(c.Text) != 0 {
		t.Fatalf("phrase -> Or=%v Text=%v; want Or=[quarterly report], no Text", got, c.Text)
	}
	// Operator value may itself be quoted.
	c, _ = parseSearchQuery(`from:"john doe"`)
	hdr(t, c, "FROM", "john doe")
}

func TestParse_Negation(t *testing.T) {
	c, _ := parseSearchQuery(`-from:spammer`)
	if len(c.Not) != 1 {
		t.Fatalf("negation -> Not=%v; want 1 entry", c.Not)
	}
	hdr(t, c.Not[0], "FROM", "spammer")

	// Negated free text becomes NOT ( OR SUBJECT/FROM ... ).
	c, _ = parseSearchQuery(`-urgent`)
	if len(c.Not) != 1 {
		t.Fatalf("negated free text -> Not=%+v", c.Not)
	}
	if got := orTerms(t, c.Not[0]); len(got) != 1 || got[0] != "urgent" {
		t.Fatalf("negated free text -> Not Or=%v", got)
	}
}

func TestParse_Combined(t *testing.T) {
	c, folder := parseSearchQuery(`in:Work from:alice subject:"status update" has:attachment is:unread -draft newsletter`)
	if folder != "Work" {
		t.Fatalf("folder = %q; want Work", folder)
	}
	hdr(t, c, "FROM", "alice")
	hdr(t, c, "SUBJECT", "status update")
	hdr(t, c, "Content-Type", "multipart")
	if !hasFlag(c.WithoutFlags, imap.SeenFlag) {
		t.Fatalf("is:unread not mapped")
	}
	// -draft is an unknown operator? no: "draft" isn't an operator, so -draft is
	// negated free text.
	foundDraftNot := false
	for _, n := range c.Not {
		if hasFlag(orTerms(t, n), "draft") {
			foundDraftNot = true
		}
	}
	if !foundDraftNot {
		t.Fatalf("-draft not mapped to NOT(Subject/From draft); Not=%+v", c.Not)
	}
	// free text "newsletter" is a Subject/From term.
	if !hasFlag(orTerms(t, c), "newsletter") {
		t.Fatalf("free text newsletter missing from Or=%v", c.Or)
	}
}

// --- unknown operator degrades to free text --------------------------------

func TestParse_UnknownOperator(t *testing.T) {
	c, folder := parseSearchQuery(`label:important`)
	if folder != "" {
		t.Fatalf("unexpected folder %q", folder)
	}
	if !hasFlag(orTerms(t, c), "label:important") {
		t.Fatalf("unknown operator -> Or=%v; want to contain label:important", c.Or)
	}
	// Unknown value on a known operator (has:foo) also degrades to text.
	c, _ = parseSearchQuery(`has:foo`)
	if !hasFlag(orTerms(t, c), "has:foo") {
		t.Fatalf("has:foo -> Or=%v; want to contain has:foo", c.Or)
	}
	// Unparseable date degrades to text.
	c, _ = parseSearchQuery(`before:notadate`)
	if !hasFlag(orTerms(t, c), "before:notadate") {
		t.Fatalf("before:notadate -> Or=%v; want to contain before:notadate", c.Or)
	}
}

// --- plain free text --------------------------------------------------------

func TestParse_PlainWords(t *testing.T) {
	// No operators: each word is ANDed, matched against Subject OR From only.
	// The body is never scanned unless body:/text: is used.
	c, folder := parseSearchQuery(`big invoice from vendor`)
	if folder != "" {
		t.Fatalf("unexpected folder %q", folder)
	}
	got := orTerms(t, c)
	if len(got) != 4 || got[0] != "big" || got[3] != "vendor" {
		t.Fatalf("plain words -> Or=%v; want 4 terms", got)
	}
	if len(c.Text) != 0 || len(c.Body) != 0 || len(c.Header) != 0 || len(c.Not) != 0 {
		t.Fatalf("plain words must not scan the body: %+v", c)
	}
}

func TestParse_BodyAndTextOperators(t *testing.T) {
	c, _ := parseSearchQuery(`body:invoice text:receipt`)
	if len(c.Body) != 1 || c.Body[0] != "invoice" {
		t.Fatalf("body: -> Body=%v", c.Body)
	}
	if len(c.Text) != 1 || c.Text[0] != "receipt" {
		t.Fatalf("text: -> Text=%v", c.Text)
	}
	if len(c.Or) != 0 {
		t.Fatalf("body:/text: must not add header terms: %v", c.Or)
	}
}

func TestParse_PlainWordsWireFormat(t *testing.T) {
	c, _ := parseSearchQuery(`alice`)
	wire := encodeSearch(t, c)
	if !strings.Contains(wire, "OR") || strings.Contains(wire, "TEXT") || strings.Contains(wire, "BODY") {
		t.Fatalf("wire = %q; want OR HEADER ... without TEXT/BODY", wire)
	}
}

// --- injection safety -------------------------------------------------------

// A CR/LF in an operator value must never appear in the criterion value, and
// must never appear un-escaped in the serialised SEARCH command (which would
// let an attacker inject a second IMAP command).
func TestParse_CRLFInjection(t *testing.T) {
	c, _ := parseSearchQuery("from:alice\r\nA002 DELETE INBOX")
	// The value is sanitised: no CR/LF survives into the criterion.
	for _, v := range c.Header["FROM"] {
		if strings.ContainsAny(v, "\r\n") {
			t.Fatalf("FROM value still contains CR/LF: %q", v)
		}
	}
	// And the wire encoding contains exactly one line (one CRLF, at the end).
	wire := encodeSearch(t, c)
	body := strings.TrimSuffix(wire, "\r\n")
	if strings.Contains(body, "\r") || strings.Contains(body, "\n") {
		t.Fatalf("serialised SEARCH contains an embedded CRLF (injection!): %q", wire)
	}
	if strings.Contains(wire, "DELETE") {
		// Even if it appeared as data it must be inside a quoted/literal atom,
		// never as a bare command token on its own line.
		if strings.Contains(body, "\r") || strings.Contains(body, "\n") {
			t.Fatalf("injected DELETE reached a new line: %q", wire)
		}
	}
}

// A double-quote in an operator value must be escaped by go-imap's encoder so
// it can't close the atom early.
func TestParse_QuoteInjection(t *testing.T) {
	c, _ := parseSearchQuery(`subject:a"b`)
	wire := encodeSearch(t, c)
	// go-imap uses strconv.Quote, so the embedded quote is backslash-escaped.
	if !strings.Contains(wire, `\"`) {
		t.Fatalf("embedded quote not escaped in wire form: %q", wire)
	}
	// Still a single logical line.
	body := strings.TrimSuffix(wire, "\r\n")
	if strings.ContainsAny(body, "\r\n") {
		t.Fatalf("quote injection produced extra line: %q", wire)
	}
}

// A NUL byte in an operator value must never survive into the criterion, and the
// serialised SEARCH must contain no NUL (a NUL would otherwise force go-imap onto
// its literal path — still framed, but we strip it as defence in depth so the
// value stays a plain quoted atom).
func TestParse_NULInjection(t *testing.T) {
	c, _ := parseSearchQuery("subject:a\x00b\x00c")
	for _, v := range c.Header["Subject"] {
		if strings.ContainsRune(v, 0) {
			t.Fatalf("SUBJECT value still contains NUL: %q", v)
		}
		if v != "abc" {
			t.Fatalf("NUL not cleanly stripped: got %q want %q", v, "abc")
		}
	}
	wire := encodeSearch(t, c)
	if strings.ContainsRune(wire, 0) {
		t.Fatalf("serialised SEARCH contains a NUL byte: %q", wire)
	}
	body := strings.TrimSuffix(wire, "\r\n")
	if strings.ContainsAny(body, "\r\n") {
		t.Fatalf("NUL value produced an extra line: %q", wire)
	}
}

// The in:<folder> value becomes the argument to an IMAP SELECT, not a SEARCH
// atom, but a CR/LF/NUL there could still splice a command onto the SELECT line.
// resolveSearchFolder must strip framing bytes so the returned mailbox name is a
// single clean token.
func TestParse_FolderInjection(t *testing.T) {
	// Quoted so the whole hostile payload is captured as the in: value.
	c, folder := parseSearchQuery("in:\"INBOX\r\nA002 DELETE INBOX\" from:alice")
	if strings.ContainsAny(folder, "\r\n") || strings.ContainsRune(folder, 0) {
		t.Fatalf("folder override carries framing bytes (SELECT injection): %q", folder)
	}
	if strings.Contains(folder, "\n") {
		t.Fatalf("folder override spans multiple lines: %q", folder)
	}
	// The positive from:alice criterion must still be present — the injection
	// attempt does not disturb the rest of the parse.
	hdr(t, c, "FROM", "alice")
	// A NUL-bearing folder is likewise cleaned.
	_, folder = parseSearchQuery("in:IN\x00BOX")
	if strings.ContainsRune(folder, 0) {
		t.Fatalf("folder override carries NUL: %q", folder)
	}
}

// --- bounds -----------------------------------------------------------------

func TestParse_BoundsQueryLength(t *testing.T) {
	long := "from:" + strings.Repeat("a", maxSearchQueryLen*2)
	c, _ := parseSearchQuery(long)
	for _, v := range c.Header["FROM"] {
		if len(v) > maxSearchValueLen {
			t.Fatalf("FROM value not bounded: len=%d", len(v))
		}
	}
}

func TestParse_BoundsValueLength(t *testing.T) {
	c, _ := parseSearchQuery("subject:" + strings.Repeat("x", maxSearchValueLen+50))
	for _, v := range c.Header["SUBJECT"] {
		if len(v) > maxSearchValueLen {
			t.Fatalf("value not bounded: len=%d", len(v))
		}
	}
}

// --- selectable free-text fields (search_mods) ------------------------------

func TestNormalizeSearchMods(t *testing.T) {
	cases := map[string][]string{
		"":                     {"subject", "from"},
		"bogus,;DROP":          {"subject", "from"},
		"body":                 {"body"},
		"BODY, Subject ,body":  {"subject", "body"},
		"cc,to,from,subject,x": {"subject", "from", "to", "cc"},
	}
	for in, want := range cases {
		got := NormalizeSearchMods(in)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("NormalizeSearchMods(%q) = %v; want %v", in, got, want)
		}
	}
}

func TestParseMods_FieldsAndWire(t *testing.T) {
	// One field: merged straight into the criteria (no OR).
	c, _ := parseSearchQueryMods(`alice`, []string{"subject"})
	hdr(t, c, "SUBJECT", "alice")
	if len(c.Or) != 0 || len(c.Body) != 0 {
		t.Fatalf("single field should not OR or scan the body: %+v", c)
	}

	// Body only.
	c, _ = parseSearchQueryMods(`alice`, []string{"body"})
	if len(c.Body) != 1 || c.Body[0] != "alice" || len(c.Header) != 0 {
		t.Fatalf("body-only -> %+v", c)
	}

	// Three fields: nested OR, serialised as OR a OR b c.
	c, _ = parseSearchQueryMods(`alice`, []string{"subject", "from", "body"})
	wire := encodeSearch(t, c)
	if strings.Count(wire, "OR") != 2 || !strings.Contains(wire, "BODY") ||
		!strings.Contains(wire, "SUBJECT") || !strings.Contains(wire, "FROM") {
		t.Fatalf("3-field wire = %q", wire)
	}

	// Two words are ANDed, each across all selected fields; negation wraps the group.
	c, _ = parseSearchQueryMods(`foo -bar`, []string{"subject", "to"})
	if len(c.Or) != 1 || len(c.Not) != 1 || len(c.Not[0].Or) != 1 {
		t.Fatalf("foo -bar -> Or=%d Not=%d", len(c.Or), len(c.Not))
	}
}
