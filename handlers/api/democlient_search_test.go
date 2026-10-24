package api

import "testing"

func TestDemoSearchMessagesPaged(t *testing.T) {
	d := NewDemoClient()
	all, err := d.SearchMessages("INBOX", "e", 0) // "e" matches nearly everything
	if err != nil || len(all) < 3 {
		t.Fatalf("need >=3 demo matches, got %d err=%v", len(all), err)
	}
	p1, more, _ := d.SearchMessagesPaged("INBOX", "e", nil, 2, 0)
	if len(p1) != 2 || !more {
		t.Fatalf("page 1: len=%d more=%v", len(p1), more)
	}
	p2, _, _ := d.SearchMessagesPaged("INBOX", "e", nil, 2, 2)
	if len(p2) == 0 || p2[0].ID != all[2].ID {
		t.Fatalf("page 2 must continue after page 1")
	}
	if none, more, _ := d.SearchMessagesPaged("INBOX", "e", nil, 2, 10000); len(none) != 0 || more {
		t.Fatalf("past the end: %v %v", none, more)
	}
}
