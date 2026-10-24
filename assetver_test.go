package main

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestAssetVersioner(t *testing.T) {
	fsys := fstest.MapFS{"assets/js/app.js": {Data: []byte("v1")}}
	a := newAssetVersioner(fsys)
	u1 := a.URL("/assets/js/app.js")
	if !strings.HasPrefix(u1, "/assets/js/app.js?v=") || len(u1) != len("/assets/js/app.js?v=")+10 {
		t.Fatalf("url = %q", u1)
	}
	if a.URL("/assets/js/app.js") != u1 {
		t.Fatal("not stable across calls")
	}
	// Same path, different content (a new build) -> different version.
	fsys2 := fstest.MapFS{"assets/js/app.js": {Data: []byte("v2")}}
	if u2 := newAssetVersioner(fsys2).URL("/assets/js/app.js"); u2 == u1 {
		t.Fatalf("content change did not change the URL: %q", u2)
	}
	if got := a.URL("/assets/missing.js"); got != "/assets/missing.js" {
		t.Fatalf("missing file should be returned unchanged, got %q", got)
	}
}

func TestAssetCacheControl(t *testing.T) {
	if !strings.Contains(assetCacheControl(true), "immutable") {
		t.Fatal("versioned must be immutable")
	}
	if strings.Contains(assetCacheControl(false), "immutable") {
		t.Fatal("unversioned must not be immutable")
	}
}

// Every css/js the layout references must resolve in the real embedded FS,
// otherwise the page silently loses its cache-busting.
func TestEmbeddedAssetsGetVersion(t *testing.T) {
	a := newAssetVersioner(assetsFS)
	for _, p := range []string{"/assets/css/mail.css", "/assets/vendor/htmx.min.js", "/assets/vendor/alpine.min.js",
		"/assets/js/app.js", "/assets/js/notifications.js", "/assets/js/push.js", "/assets/lilmail-favicon.svg"} {
		if !strings.Contains(a.URL(p), "?v=") {
			t.Errorf("%s has no version (missing from the embedded FS?)", p)
		}
	}
}
