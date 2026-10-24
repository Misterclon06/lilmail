package web

import (
	"fmt"
	"lilmail/models"
	"testing"
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
		got, more := unifiedPage(mk(c.n), c.anyMore, c.page)
		if len(got) != c.want || more != c.more {
			t.Errorf("n=%d page=%d anyMore=%v: got len=%d more=%v; want %d %v", c.n, c.page, c.anyMore, len(got), more, c.want, c.more)
		}
	}
}
