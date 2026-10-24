package main

import (
	"bytes"
	"fmt"
	"lilmail/models"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/template/html/v2"
)

// TestTemplatesParse ensures every template in ./templates parses with the
// same custom funcs registered in main(). go build does not compile templates,
// so this guards against unbalanced {{end}} / unknown functions at runtime.
// testEngine registers the same custom funcs main() does.
func testEngine() *html.Engine {
	engine := html.New("./templates", ".html")
	engine.AddFunc("asset", func(p string) string { return p })
	engine.AddFunc("split", strings.Split)
	engine.AddFunc("join", strings.Join)
	engine.AddFunc("lower", strings.ToLower)
	engine.AddFunc("upper", strings.ToUpper)
	engine.AddFunc("title", strings.Title)
	engine.AddFunc("trim", strings.TrimSpace)
	engine.AddFunc("hasPrefix", strings.HasPrefix)
	engine.AddFunc("urlEncode", func(s string) string { return s })
	engine.AddFunc("linkify", linkifyText)
	engine.AddFunc("formatDate", func(tm time.Time) string { return tm.String() })
	engine.AddFunc("formatSize", func(n int) string { return fmt.Sprintf("%d", n) })
	engine.AddFunc("initial", func(name, email string) string { return "?" })
	engine.AddFunc("caldavEnabled", func() bool { return false })
	engine.AddFunc("notificationsEnabled", func() bool { return false })
	engine.AddFunc("webPushEnabled", func() bool { return false })
	engine.AddFunc("accountsEnabled", func() bool { return false })
	return engine
}

func TestTemplatesParse(t *testing.T) {
	engine := testEngine()
	if err := engine.Load(); err != nil {
		t.Fatalf("template parse error: %v", err)
	}
}

// The list rows carry mailbox-controlled values (folder name, account address,
// UID, subject). They must reach the page only as HTML-escaped data-* attributes
// read by openMessage(): never spliced into inline JS, and no bearer token may be
// embedded per row.
func TestEmailRowsHostileValuesAndNoToken(t *testing.T) {
	engine := testEngine()
	if err := engine.Load(); err != nil {
		t.Fatal(err)
	}
	const evil = `x'+alert(1)+'" onmouseover="alert(2)`
	msg := models.Email{ID: "7", Subject: evil, From: "a@b", FromName: evil,
		AccountEmail: evil, AccountLabel: evil, Date: time.Unix(1000, 0), Flags: []string{}}
	reply := msg
	reply.ID = "8"
	data := map[string]any{
		"CurrentFolder": evil,
		"Token":         "eyJSECRETSECRETSECRET.payload.sig",
		"Unified":       true,
		"Threads": []models.Thread{
			{Root: msg, Latest: msg, Messages: []models.Email{msg}, Count: 1},
			{Root: msg, Latest: reply, Messages: []models.Email{msg, reply}, Count: 2},
		},
	}
	var buf bytes.Buffer
	if err := engine.Render(&buf, "partials/email-list", data, ""); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	if strings.Contains(out, "Bearer") || strings.Contains(out, "SECRETSECRET") {
		t.Error("a token leaked into the list markup")
	}
	// The raw hostile string must never appear unescaped inside an attribute.
	if strings.Contains(out, `x'+alert(1)`) || strings.Contains(out, `" onmouseover="alert(2)`) {
		t.Error("hostile value reached the page unescaped")
	}
	if strings.Count(out, `data-folder="`) < 3 || !strings.Contains(out, `data-account="`) {
		t.Error("rows must carry data-folder / data-account")
	}
	// Inline handlers are fixed strings: no interpolated {{...}} output inside @click.
	for _, line := range strings.Split(out, "\n") {
		if i := strings.Index(line, `@click="`); i >= 0 {
			v := line[i+len(`@click="`):]
			v = v[:strings.Index(v, `"`)]
			if strings.ContainsAny(v, "{}") || strings.Contains(v, "alert") || strings.Contains(v, "'") && !strings.Contains(v, "$el.closest('[data-id]')") {
				t.Errorf("@click carries data: %q", v)
			}
		}
	}
}
