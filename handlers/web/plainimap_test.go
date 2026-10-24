package web

import (
	"lilmail/config"
	"testing"
)

func TestPlainIMAPAllowed(t *testing.T) {
	tlsOn := &config.Config{}
	tlsOn.IMAP.Server, tlsOn.IMAP.TLS = "mail.example.com", true
	plainCfg := &config.Config{}
	plainCfg.IMAP.Server, plainCfg.IMAP.TLS = "mail.lan", false

	cases := []struct {
		host string
		cfg  *config.Config
		want bool
	}{
		{"localhost", tlsOn, true},
		{"LOCALHOST", tlsOn, true},
		{"127.0.0.1", tlsOn, true},
		{"127.5.5.5", tlsOn, true},
		{"::1", tlsOn, true},
		{"mail.example.com", tlsOn, false},   // TLS server: never plain
		{"mail.lan", tlsOn, false},           // not the configured plain server
		{"mail.lan", plainCfg, true},         // operator opted in for exactly this host
		{"other.lan", plainCfg, false},       // ...but not for others
		{"127.0.0.1.evil.com", tlsOn, false}, // looks loopback, is a DNS name
		{"10.0.0.5", tlsOn, false},           // private, not loopback
		{"", tlsOn, false},
	}
	for _, c := range cases {
		if got := plainIMAPAllowed(c.host, c.cfg); got != c.want {
			t.Errorf("plainIMAPAllowed(%q) = %v, want %v", c.host, got, c.want)
		}
	}
}
