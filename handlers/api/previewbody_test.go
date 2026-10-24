package api

import (
	"strings"
	"testing"

	"github.com/emersion/go-imap"
)

func TestPreviewBody(t *testing.T) {
	multi := func(b string, parts ...*imap.BodyStructure) *imap.BodyStructure {
		return &imap.BodyStructure{MIMEType: "multipart", MIMESubType: "alternative", Params: map[string]string{"boundary": b}, Parts: parts}
	}
	text := &imap.BodyStructure{MIMEType: "text", MIMESubType: "plain"}

	cases := []struct {
		name string
		in   string
		bs   *imap.BodyStructure
		want string
	}{
		{"single part keeps first paragraph", "Hello Bob,\r\n\r\nSee attached.", text, "Hello Bob,\r\n\r\nSee attached."},
		{"alternative stops at next boundary",
			"--B1\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nplain version\r\n--B1\r\nContent-Type: text/html\r\n\r\n<p>x</p>\r\n--B1--\r\n",
			multi("B1", text, text), "plain version"},
		{"nested multipart",
			"--outer\r\nContent-Type: multipart/alternative; boundary=inner\r\n\r\n--inner\r\nContent-Type: text/plain\r\n\r\nHi there\r\n--inner--\r\n--outer--\r\n",
			multi("outer", multi("inner", text)), "Hi there"},
		{"no part headers", "--B\r\n\r\nbody only\r\n--B--", multi("B", text), "body only"},
		{"truncated mid boundary", "--B\r\nContent-Type: text/plain\r\n\r\nlong text that got cut\r\n--", multi("B", text), "long text that got cut\n--"},
	}
	for _, c := range cases {
		if got := previewBody(c.in, c.bs); strings.TrimSpace(got) != strings.TrimSpace(c.want) {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}
