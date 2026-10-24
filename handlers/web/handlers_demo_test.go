package web

import (
	"io"
	"lilmail/config"
	"lilmail/handlers/api"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/session"
	"github.com/valyala/fasthttp"
)

// demoApp wires the real session middleware and handlers around demo mode (an
// in-memory MailClient), so handlers can be driven end to end without IMAP.
func demoApp(t *testing.T) (*fiber.App, *EmailHandler, *session.Store) {
	t.Helper()
	cfg := &config.Config{}
	cfg.JWT.Secret = "test-secret-test-secret-test-secret"
	cfg.Cache.Folder = t.TempDir()
	store := session.New()
	auth := NewAuthHandler(store, cfg)
	eh := NewEmailHandler(store, cfg, auth)

	app := fiber.New()
	// Test-only: establish a demo session (what HandleDemoLogin does).
	app.Get("/_login", func(c *fiber.Ctx) error {
		sess, err := store.Get(c)
		if err != nil {
			return err
		}
		sess.Set("authenticated", true)
		sess.Set("email", "demo@lilmail.dev")
		sess.Set("username", "demo")
		sess.Set("auth_type", demoAuthType)
		return sess.Save()
	})
	protected := app.Group("", api.SessionMiddleware(store))
	protected.Get("/api/email/:id/body", eh.HandleEmailBody)
	return app, eh, store
}

func loginCookie(t *testing.T, app *fiber.App) string {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest("GET", "/_login", nil))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range resp.Cookies() {
		return c.Name + "=" + c.Value
	}
	t.Fatal("no session cookie")
	return ""
}

// HandleEmailBody serves the body document for "Display images": it must be
// session-gated, carry its own strict CSP (stricter when images are off), and
// stop the app URL leaking as Referer to remote hosts.
func TestHandleEmailBody(t *testing.T) {
	app, _, _ := demoApp(t)

	// Unauthenticated: redirected to login, no body.
	resp, err := app.Test(httptest.NewRequest("GET", "/api/email/1001/body?folder=INBOX", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != fiber.StatusFound || resp.Header.Get("Location") != "/login" {
		t.Fatalf("unauthenticated: %d -> %q", resp.StatusCode, resp.Header.Get("Location"))
	}

	cookie := loginCookie(t, app)
	get := func(path string) (*fiber.Ctx, string, map[string]string, int) {
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("Cookie", cookie)
		r, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(r.Body)
		h := map[string]string{}
		for k := range r.Header {
			h[strings.ToLower(k)] = r.Header.Get(k)
		}
		return nil, string(b), h, r.StatusCode
	}

	_, body, h, status := get("/api/email/1001/body?folder=INBOX")
	if status != 200 {
		t.Fatalf("status %d: %s", status, body)
	}
	if !strings.Contains(h["content-security-policy"], cspBlocked) || !strings.Contains(h["content-security-policy"], "frame-ancestors 'self'") {
		t.Errorf("blocked-mode CSP header = %q", h["content-security-policy"])
	}
	if h["referrer-policy"] != "no-referrer" || !strings.Contains(h["cache-control"], "no-store") {
		t.Errorf("referrer/cache headers = %q / %q", h["referrer-policy"], h["cache-control"])
	}
	if !strings.Contains(h["content-type"], "text/html") || !strings.Contains(body, "<!DOCTYPE html>") {
		t.Errorf("not an HTML document: %q", h["content-type"])
	}

	_, _, h, _ = get("/api/email/1001/body?folder=INBOX&images=1")
	if !strings.Contains(h["content-security-policy"], cspAllowed) {
		t.Errorf("images=1 CSP header = %q", h["content-security-policy"])
	}

	// Unknown message: an error, not a document.
	if _, _, _, st := get("/api/email/999999/body?folder=INBOX"); st != 500 {
		t.Errorf("unknown message status = %d, want 500", st)
	}
}

// Regression: Subscribe used to start a goroutine that touched the Fiber ctx
// after the handler returned (Fiber recycles it): a nil dereference that killed
// the whole process on the first /events subscription. The IMAP client must be
// created inside the request. Here the ctx is released right after Subscribe
// returns, exactly as Fiber does when a handler finishes; a goroutine that still
// uses it panics and fails the whole test binary.
func TestSubscribe_DoesNotKeepFiberCtx(t *testing.T) {
	app, eh, store := demoApp(t)
	cfg := eh.config
	cfg.Notifications.Enabled, cfg.Notifications.Idle = true, true
	hub := NewNotificationHub(store, cfg, eh.auth, nil, nil)
	cookie := loginCookie(t, app)

	ch := make(chan MailEvent, 1)
	fctx := &fasthttp.RequestCtx{}
	fctx.Request.Header.Set("Cookie", cookie)
	c := app.AcquireCtx(fctx)
	hub.Subscribe("demo", ch, c)
	app.ReleaseCtx(c) // the handler "returned": Fiber recycles c
	time.Sleep(150 * time.Millisecond)

	hub.mu.Lock()
	st := hub.users["demo"]
	running := st != nil && st.stop != nil
	hub.mu.Unlock()
	if !running {
		t.Fatal("watcher should be running after Subscribe")
	}
	hub.Unsubscribe("demo", ch)
	time.Sleep(50 * time.Millisecond)
	hub.mu.Lock()
	_, still := hub.users["demo"]
	hub.mu.Unlock()
	if still {
		t.Fatal("hub should drop the user once the last subscriber leaves")
	}
}
