package web

import (
	"fmt"
	"lilmail/handlers/api"
	"lilmail/models"
	"testing"
	"time"
)

// threadedStub is a newest-first mailbox of uids n..1 where every even uid
// replies to uid-1, so some conversations straddle a page boundary.
type threadedStub struct {
	api.MailClient
	n int
}

func (s threadedStub) FetchMessagesPaged(_ string, limit, offset uint32) ([]models.Email, error) {
	var out []models.Email
	for uid := s.n - int(offset); uid >= 1 && len(out) < int(limit); uid-- {
		e := models.Email{
			ID:        fmt.Sprint(uid),
			MessageID: fmt.Sprintf("<m%d@t>", uid),
			Subject:   fmt.Sprintf("s%d", uid),
			Date:      time.Unix(int64(1000+uid), 0),
		}
		if uid%2 == 0 {
			e.InReplyTo = fmt.Sprintf("<m%d@t>", uid-1)
			e.References = []string{e.InReplyTo}
		}
		out = append(out, e)
	}
	return out, nil
}

// TestLoadFolderPage_EveryMessageOnce walks all pages in order and asserts that
// each message is listed exactly once: no repeats from earlier pages and none
// hidden inside a row rendered before it was fetched.
func TestLoadFolderPage_EveryMessageOnce(t *testing.T) {
	const n = 230
	h := &EmailHandler{}
	seen := map[string]int{}
	for page := 1; page <= 6; page++ {
		p, err := h.loadFolderPage(threadedStub{n: n}, "u", "u@t", "INBOX", page, false, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, th := range p.Threads {
			for _, m := range th.Messages {
				seen[m.ID]++
			}
		}
		if page == 5 && p.HasMore {
			t.Errorf("page 5 of %d messages must be the last", n)
		}
	}
	if len(seen) != n {
		t.Errorf("listed %d distinct messages, want %d", len(seen), n)
	}
	for id, c := range seen {
		if c != 1 {
			t.Errorf("message %s listed %d times", id, c)
		}
	}
}
