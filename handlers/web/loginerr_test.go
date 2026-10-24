package web

import (
	"errors"
	"fmt"
	"lilmail/config"
	"lilmail/handlers/api"
	"os"
	"path/filepath"
	"testing"
)

func TestLoginFailure(t *testing.T) {
	cases := []struct {
		err    error
		status int
		msg    string
	}{
		{fmt.Errorf("%w: invalid credentials", api.ErrLogin), 401, "Invalid email or password"},
		{fmt.Errorf("oauth2 %w: bad token", api.ErrLogin), 401, "Invalid email or password"},
		{fmt.Errorf("%w: i/o timeout", api.ErrConnect), 503, "Could not reach the mail server. Check that it is running and try again."},
		{errors.New("something else"), 502, "Login failed because of a mail server error. Try again later."},
	}
	for _, c := range cases {
		status, msg := loginFailure(c.err)
		if status != c.status || msg != c.msg {
			t.Errorf("loginFailure(%v) = %d %q; want %d %q", c.err, status, msg, c.status, c.msg)
		}
	}
}

// foldersClient serves a fixed folder list; every other MailClient method is nil.
type foldersClient struct {
	api.MailClient
	folders []*api.MailboxInfo
	err     error
	calls   int
}

func (f *foldersClient) FetchFolders() ([]*api.MailboxInfo, error) {
	f.calls++
	return f.folders, f.err
}

// A missing cache directory (cleared, or a session that outlived it) must be
// rebuilt from IMAP, not fail the page; a second call then reads the file.
func TestLoadFolders_RebuildsMissingCache(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "gone") // does not exist yet
	h := &EmailHandler{config: &config.Config{Cache: config.CacheConfig{Folder: cache}}}
	fc := &foldersClient{folders: []*api.MailboxInfo{{Name: "INBOX"}, {Name: "Sent"}}}

	got, err := h.loadFolders(fc, "alice")
	if err != nil || len(got) != 2 || fc.calls != 1 {
		t.Fatalf("rebuild: got=%v err=%v calls=%d", got, err, fc.calls)
	}
	if _, err := os.Stat(filepath.Join(cache, api.SanitizeUsername("alice"), "folders.json")); err != nil {
		t.Fatalf("cache not rewritten: %v", err)
	}
	got, err = h.loadFolders(fc, "alice")
	if err != nil || len(got) != 2 || fc.calls != 1 {
		t.Fatalf("second call should hit the cache: got=%v err=%v calls=%d", got, err, fc.calls)
	}
}

func TestLoadFolders_IMAPErrorPropagates(t *testing.T) {
	h := &EmailHandler{config: &config.Config{Cache: config.CacheConfig{Folder: t.TempDir()}}}
	if _, err := h.loadFolders(&foldersClient{err: errors.New("down")}, "bob"); err == nil {
		t.Fatal("expected the IMAP error")
	}
}
