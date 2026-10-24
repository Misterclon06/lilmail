package main

// assetver.go — cache-busting URLs for the embedded static assets.
//
// Assets are served with a long max-age so a page load costs no requests for
// css/js. That is only safe if a changed file gets a new URL, otherwise browsers
// keep running yesterday's app.js against today's HTML (a real failure: after an
// upgrade the old script lacked a helper the new templates called). The
// `asset` template func appends ?v=<hash of the embedded file>; the file is
// compiled into the binary, so the hash is computed once per path and changes
// exactly when the content does. No build step.

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"strings"
	"sync"
)

type assetVersioner struct {
	fsys fs.FS // rooted at the directory that contains "assets/"
	mu   sync.Mutex
	memo map[string]string
}

func newAssetVersioner(fsys fs.FS) *assetVersioner {
	return &assetVersioner{fsys: fsys, memo: map[string]string{}}
}

// URL returns path (e.g. "/assets/css/mail.css") with ?v=<10 hex of sha256>.
// An unreadable path is returned unchanged.
func (a *assetVersioner) URL(path string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if v, ok := a.memo[path]; ok {
		return v
	}
	out := path
	if b, err := fs.ReadFile(a.fsys, strings.TrimPrefix(path, "/")); err == nil {
		sum := sha256.Sum256(b)
		out = path + "?v=" + hex.EncodeToString(sum[:])[:10]
	}
	a.memo[path] = out
	return out
}

// assetCacheControl picks the Cache-Control for a /assets response: a versioned
// URL is immutable for a year; an unversioned one (icons, manifest) is cached
// for an hour so an upgrade is picked up promptly.
func assetCacheControl(versioned bool) string {
	if versioned {
		return "public, max-age=31536000, immutable"
	}
	return "public, max-age=3600"
}

// compressibleAsset reports whether a /assets path is text worth compressing.
// Images (png/ico/webp/jpg) are already compressed; svg is XML text.
func compressibleAsset(path string) bool {
	switch strings.ToLower(path[strings.LastIndex(path, ".")+1:]) {
	case "css", "js", "svg", "webmanifest", "json", "txt", "map":
		return true
	}
	return false
}
