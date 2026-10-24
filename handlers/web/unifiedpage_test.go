package web

import (
	"fmt"
	"lilmail/models"
	"testing"
	"time"
)

func TestUnifiedPage(t *testing.T) {
	mk := func(n int) []models.Email {
		out := make([]models.Email, n)
		for i := range out {
			out[i].ID = fmt.Sprint(i)
		}
		return out
	}
	cases := []struct {
		n, page int
		anyMore bool
		want    int
		more    bool
	}{
		{10, 1, false, 10, false},
		{120, 1, false, 50, true},
		{120, 3, false, 20, false},
		{120, 3, true, 20, true}, // an account may still have older messages
		{120, 4, true, 0, false}, // window past the merged list
		{50, 1, true, 50, true},  // exactly one page, accounts may have more
		{50, maxUnifiedPage, true, 0, false},
	}
	for _, c := range cases {
		got, more, _ := unifiedPage(mk(c.n), c.anyMore, c.page)
		if len(got) != c.want || more != c.more {
			t.Errorf("n=%d page=%d anyMore=%v: got len=%d more=%v; want %d %v", c.n, c.page, c.anyMore, len(got), more, c.want, c.more)
		}
	}
}

// At the last unified page the list stops; if accounts still hold older mail the
// UI must say so (capped) rather than just end.
func TestUnifiedPage_Capped(t *testing.T) {
	merged := make([]models.Email, maxUnifiedPage*mailPageSize)
	win, more, capped := unifiedPage(merged, true, maxUnifiedPage)
	if len(win) != mailPageSize || more || !capped {
		t.Fatalf("last page with older mail: len=%d more=%v capped=%v", len(win), more, capped)
	}
	if _, _, capped := unifiedPage(merged, false, maxUnifiedPage); capped {
		t.Fatal("nothing older anywhere: must not report capped")
	}
	if _, more, capped := unifiedPage(merged, true, maxUnifiedPage-1); !more || capped {
		t.Fatalf("a middle page keeps paging: more=%v capped=%v", more, capped)
	}
}

// Two accounts with the same subject (identical newsletters) must stay separate
// threads; JWZ subject grouping would otherwise fuse them across accounts.
func TestThreadPerAccount_NoCrossAccountSubjectGrouping(t *testing.T) {
	at := time.Unix(1000, 0)
	emails := []models.Email{
		{ID: "1", AccountEmail: "a@x", MessageID: "<1@a>", Subject: "Weekly digest", Date: at},
		{ID: "1", AccountEmail: "b@x", MessageID: "<1@b>", Subject: "Weekly digest", Date: at.Add(time.Minute)},
		{ID: "2", AccountEmail: "a@x", MessageID: "<2@a>", InReplyTo: "<1@a>", References: []string{"<1@a>"}, Subject: "Re: Weekly digest", Date: at.Add(2 * time.Minute)},
	}
	th := threadPerAccount(emails)
	if len(th) != 2 {
		t.Fatalf("want 2 threads (one per account), got %d", len(th))
	}
	// Sorted by newest message: account a's thread (reply at +2m) first, with both messages.
	if th[0].Latest.AccountEmail != "a@x" || th[0].Count != 2 || th[1].Count != 1 {
		t.Fatalf("unexpected threads: %+v", th)
	}
}
