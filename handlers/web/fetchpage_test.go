package web

import (
	"fmt"
	"lilmail/handlers/api"
	"lilmail/models"
	"testing"
)

// pagedStub serves a newest-first mailbox of uids n..1 and honours limit/offset.
type pagedStub struct {
	api.MailClient
	n int
}

func (s pagedStub) FetchMessagesPaged(_ string, limit, offset uint32) ([]models.Email, error) {
	var out []models.Email
	for uid := s.n - int(offset); uid >= 1 && len(out) < int(limit); uid-- {
		out = append(out, models.Email{ID: fmt.Sprint(uid)})
	}
	return out, nil
}

func TestFetchPage(t *testing.T) {
	cases := []struct {
		n, page, want int
		more          bool
	}{
		{0, 1, 0, false},
		{10, 1, 10, false},
		{50, 1, 50, false},
		{51, 1, 50, true},
		{120, 1, 50, true},
		{120, 3, 20, false},
		{120, 4, 0, false},
	}
	for _, c := range cases {
		got, more, err := fetchPage(pagedStub{n: c.n}, "INBOX", c.page)
		if err != nil || len(got) != c.want || more != c.more {
			t.Errorf("n=%d page=%d: got len=%d more=%v err=%v, want len=%d more=%v", c.n, c.page, len(got), more, err, c.want, c.more)
		}
	}
	// Pages must be contiguous and non-overlapping: page 2 starts right after page 1.
	p1, _, _ := fetchPage(pagedStub{n: 120}, "INBOX", 1)
	p2, _, _ := fetchPage(pagedStub{n: 120}, "INBOX", 2)
	if p1[len(p1)-1].ID != "71" || p2[0].ID != "70" {
		t.Errorf("page boundary: p1 last=%s p2 first=%s", p1[len(p1)-1].ID, p2[0].ID)
	}
}
