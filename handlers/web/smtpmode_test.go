package web

import (
	"lilmail/config"
	"lilmail/handlers/api"
	"testing"
)

func TestSMTPModeForAccount(t *testing.T) {
	cfg := &config.Config{}
	cfg.SMTP.Server, cfg.SMTP.Port, cfg.SMTP.UseSTARTTLS, cfg.SMTP.InsecureSkipVerify = "smtp.home.lan", 587, true, true

	cases := []struct {
		name              string
		server            string
		port              int
		wantTLS, wantSkip bool
	}{
		{"global server: inherit both", "smtp.home.lan", 587, true, true},
		{"global server, case-insensitive", "SMTP.HOME.LAN", 587, true, true},
		{"other server on 465: implicit TLS, verify", "smtp.other.com", 465, false, false},
		{"other server on 587: STARTTLS, verify", "smtp.other.com", 587, true, false},
		{"same host, different port: not the global server", "smtp.home.lan", 465, false, false},
		{"other server on 25: STARTTLS, verify", "mx.other.com", 25, true, false},
	}
	for _, c := range cases {
		gotTLS, gotSkip := smtpModeForAccount(AccountEntry{SMTPServer: c.server, SMTPPort: c.port}, cfg)
		if gotTLS != c.wantTLS || gotSkip != c.wantSkip {
			t.Errorf("%s: got startTLS=%v skip=%v, want %v %v", c.name, gotTLS, gotSkip, c.wantTLS, c.wantSkip)
		}
	}

	// Global implicit-TLS setup (use_starttls = false) is inherited by its own accounts.
	cfg.SMTP.Port, cfg.SMTP.UseSTARTTLS, cfg.SMTP.InsecureSkipVerify = 465, false, false
	if tls, skip := smtpModeForAccount(AccountEntry{SMTPServer: "smtp.home.lan", SMTPPort: 465}, cfg); tls || skip {
		t.Errorf("global implicit TLS not inherited: startTLS=%v skip=%v", tls, skip)
	}
}

// End to end through CreateSMTPClientForAccount: the snapshot the client sends
// with must carry the per-account mode, not the global one.
func TestCreateSMTPClientForAccount_UsesPerAccountMode(t *testing.T) {
	cfg := &config.Config{}
	cfg.Encryption.Key = "0123456789abcdef0123456789abcdef"
	cfg.SMTP.Server, cfg.SMTP.Port, cfg.SMTP.UseSTARTTLS, cfg.SMTP.InsecureSkipVerify = "smtp.home.lan", 587, true, true
	h := &AuthHandler{config: cfg}

	enc, err := api.EncryptJSON("secret", cfg.Encryption.Key)
	if err != nil {
		t.Fatal(err)
	}
	cl, err := h.CreateSMTPClientForAccount(AccountEntry{Email: "u@other.com", SMTPServer: "smtp.other.com", SMTPPort: 465, EncryptedPassword: enc})
	if err != nil {
		t.Fatal(err)
	}
	tr := cl.Transport()
	if tr.UseSTARTTLS || tr.InsecureSkip || tr.Port != 465 {
		t.Fatalf("465 account under global STARTTLS+skip-verify: %+v", tr)
	}
}
