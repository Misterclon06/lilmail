package api

import "testing"

func TestSearchWindow(t *testing.T) {
	cases := []struct {
		total, limit, offset, start, end uint32
		more                             bool
	}{
		{0, 50, 0, 0, 0, false},
		{10, 50, 0, 0, 10, false},
		{120, 50, 0, 70, 120, true},
		{120, 50, 50, 20, 70, true},
		{120, 50, 100, 0, 20, false},
		{120, 50, 120, 0, 0, false},
		{120, 0, 0, 0, 120, false}, // limit 0 = unbounded
	}
	for _, c := range cases {
		s, e, m := searchWindow(c.total, c.limit, c.offset)
		if s != c.start || e != c.end || m != c.more {
			t.Errorf("total=%d limit=%d offset=%d: got [%d,%d) more=%v; want [%d,%d) more=%v",
				c.total, c.limit, c.offset, s, e, m, c.start, c.end, c.more)
		}
	}
}
