// handlers/web/unified.go
//
// Multiplexed cross-account inbox fetch for the unified-inbox feature.
//
// FetchUnified opens one IMAP connection per account concurrently (goroutines),
// tags each message with the source account's label and colour, and merges the
// results into a single list sorted by date descending.
//
// Design decisions:
//   - One goroutine per account; all run concurrently.
//   - Per-account timeout via context.WithTimeout (10 s default).
//   - One account failing MUST NOT block or break the others: errors are
//     collected into AccountFetchError and returned alongside any messages that
//     did succeed.
//   - The primary session account is included; its label/colour may be empty
//     (single-account mode) — callers can handle that case.
//   - No global lock on the IMAP fetch; each goroutine owns its own connection.

package web

import (
	"context"
	"fmt"
	"lilmail/handlers/api"
	"lilmail/models"
	"log"
	"sort"
	"sync"
	"time"
)

const unifiedFetchTimeout = 10 * time.Second

// AccountFetchResult holds the messages and/or error from one account.
type AccountFetchResult struct {
	// AccountEmail is the IMAP login address for this account.
	AccountEmail string
	// AccountLabel is the display label (may equal AccountEmail when not set).
	AccountLabel string
	// AccountColor is the CSS colour string for the badge (may be empty).
	AccountColor string
	// Emails are the fetched messages, already tagged with account metadata.
	Emails []models.Email
	// Err is non-nil when the fetch for this account failed.
	Err error
	// More reports that the account may hold older messages than it returned.
	More bool
}

// listFetcher loads up to limit newest messages from one account's client and
// reports whether older ones remain. Unified listing and unified search differ
// only in this function.
type listFetcher func(c api.MailClient, limit uint32) ([]models.Email, bool, error)

// folderFetcher lists a folder (more = the account returned a full window).
func folderFetcher(folder string) listFetcher {
	return func(c api.MailClient, limit uint32) ([]models.Email, bool, error) {
		emails, err := c.FetchMessages(folder, limit)
		return emails, err == nil && uint32(len(emails)) >= limit, err
	}
}

// searchFetcher searches a folder, paging when the client supports it.
func searchFetcher(folder, query string, mods []string) listFetcher {
	return func(c api.MailClient, limit uint32) ([]models.Email, bool, error) {
		if sp, ok := c.(searchPager); ok {
			return sp.SearchMessagesPaged(folder, query, mods, limit, 0)
		}
		emails, err := c.SearchMessages(folder, query, limit)
		return emails, err == nil && uint32(len(emails)) >= limit, err
	}
}

// AccountFetchError aggregates per-account errors returned alongside a
// successful (possibly partial) unified result.
type AccountFetchError []AccountFetchResult

func (e AccountFetchError) Error() string {
	var failed []string
	for _, r := range e {
		if r.Err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", r.AccountEmail, r.Err))
		}
	}
	if len(failed) == 0 {
		return ""
	}
	return fmt.Sprintf("unified fetch: %d account(s) failed: %v", len(failed), failed)
}

// HasErrors reports whether any account fetch failed.
func (e AccountFetchError) HasErrors() bool {
	for _, r := range e {
		if r.Err != nil {
			return true
		}
	}
	return false
}

// unifiedFetcher carries the dependencies needed by FetchUnified.
type unifiedFetcher struct {
	auth  *AuthHandler
	fn    listFetcher
	limit uint32
}

// fetchForEntry fetches messages for one additional (non-session) account.
// It tags each email with the account's label and colour.
func (f *unifiedFetcher) fetchForEntry(entry AccountEntry) AccountFetchResult {
	result := AccountFetchResult{
		AccountEmail: entry.Email,
		AccountLabel: entry.Label,
		AccountColor: entry.Color,
	}
	if result.AccountLabel == "" {
		result.AccountLabel = entry.Email
	}

	ctx, cancel := context.WithTimeout(context.Background(), unifiedFetchTimeout)
	defer cancel()

	done := make(chan struct{})
	var emails []models.Email
	var more bool
	var err error

	go func() {
		defer close(done)
		client, clientErr := f.auth.CreateIMAPClientForAccount(entry)
		if clientErr != nil {
			err = fmt.Errorf("connect: %w", clientErr)
			return
		}
		defer client.Close()

		emails, more, err = f.fn(client, f.limit)
	}()

	select {
	case <-ctx.Done():
		result.Err = fmt.Errorf("timeout after %s", unifiedFetchTimeout)
		return result
	case <-done:
	}

	if err != nil {
		result.Err = err
		return result
	}

	// Tag each message with the source account.
	for i := range emails {
		emails[i].AccountEmail = entry.Email
		emails[i].AccountLabel = result.AccountLabel
		emails[i].AccountColor = entry.Color
	}
	result.Emails = emails
	result.More = more
	return result
}

// FetchUnified fetches messages from the primary account (its client is already
// open and passed directly as primaryClient to avoid a double-connect) and from
// each additional account in entries, concurrently.
//
// primaryEmail / primaryLabel / primaryColor identify the session account in the
// unified list.  When primaryLabel is empty the badge is suppressed in the
// template (single-account mode).
//
// Returns:
//   - merged []models.Email sorted date descending
//   - []AccountFetchResult — one entry per account; Err != nil for failures
func FetchUnified(
	primaryClient api.MailClient,
	primaryEmail, primaryLabel, primaryColor string,
	entries []AccountEntry,
	auth *AuthHandler,
	folder string,
	limit uint32,
) ([]models.Email, []AccountFetchResult) {
	merged, _, allResults := fetchUnifiedMerged(primaryClient, primaryEmail, primaryLabel, primaryColor,
		entries, auth, limit, folderFetcher(folder))

	// Cap at limit × account count to avoid unbounded growth; cap at 200.
	maxMerged := int(limit) * (len(allResults))
	if maxMerged > 200 {
		maxMerged = 200
	}
	if len(merged) > maxMerged {
		merged = merged[:maxMerged]
	}
	return merged, allResults
}

// maxUnifiedPage bounds unified paging: page N asks every account for N pages'
// worth of messages (see unifiedPage), so the cost grows with N.
const maxUnifiedPage = 20

// unifiedPage returns the date-descending window for a 1-based page of a merged
// unified list, whether further pages exist, and whether the list stops at
// maxUnifiedPage while accounts still hold older mail (capped: the UI says so
// instead of just ending).
func unifiedPage(merged []models.Email, anyMore bool, page int) (window []models.Email, hasMore, capped bool) {
	start := (page - 1) * mailPageSize
	end := start + mailPageSize
	if start >= len(merged) {
		return nil, false, false
	}
	if end > len(merged) {
		end = len(merged)
	}
	more := end < len(merged) || anyMore
	if page >= maxUnifiedPage {
		return merged[start:end], false, more
	}
	return merged[start:end], more, false
}

// threadPerAccount threads each account's messages separately and merges the
// threads by newest message. UIDs and Message-IDs are only meaningful within
// one mailbox, and subject grouping must not fuse two accounts' unrelated
// mail that merely share a subject (e.g. identical newsletters).
func threadPerAccount(emails []models.Email) []models.Thread {
	byAcct := map[string][]models.Email{}
	var order []string
	for _, e := range emails {
		if _, ok := byAcct[e.AccountEmail]; !ok {
			order = append(order, e.AccountEmail)
		}
		byAcct[e.AccountEmail] = append(byAcct[e.AccountEmail], e)
	}
	var out []models.Thread
	for _, acct := range order {
		out = append(out, api.ThreadMessages(byAcct[acct])...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Latest.Date.After(out[j].Latest.Date) })
	return out
}

// FetchUnifiedPage lists one page of the unified folder view. Accounts have
// independent UID spaces, so there is no cross-account cursor: each account is
// asked for page*mailPageSize messages and the merged, date-sorted list is
// windowed. Simple and correct; the cost grows with the page number.
func FetchUnifiedPage(
	primaryClient api.MailClient,
	primaryEmail, primaryLabel, primaryColor string,
	entries []AccountEntry,
	auth *AuthHandler,
	folder string,
	page int,
) ([]models.Email, bool, bool, []AccountFetchResult) {
	merged, more, results := fetchUnifiedMerged(primaryClient, primaryEmail, primaryLabel, primaryColor,
		entries, auth, uint32(page*mailPageSize), folderFetcher(folder))
	window, hasMore, capped := unifiedPage(merged, more, page)
	return window, hasMore, capped, results
}

// SearchUnifiedPage is FetchUnifiedPage for a search across all accounts.
func SearchUnifiedPage(
	primaryClient api.MailClient,
	primaryEmail, primaryLabel, primaryColor string,
	entries []AccountEntry,
	auth *AuthHandler,
	folder, query string,
	mods []string,
	page int,
) ([]models.Email, bool, bool, []AccountFetchResult) {
	merged, more, results := fetchUnifiedMerged(primaryClient, primaryEmail, primaryLabel, primaryColor,
		entries, auth, uint32(page*mailPageSize), searchFetcher(folder, query, mods))
	window, hasMore, capped := unifiedPage(merged, more, page)
	return window, hasMore, capped, results
}

// fetchUnifiedMerged runs fn against the primary client and every additional
// account concurrently and returns the merged, date-sorted, UNCAPPED messages,
// whether any account may have more, and the per-account results.
func fetchUnifiedMerged(
	primaryClient api.MailClient,
	primaryEmail, primaryLabel, primaryColor string,
	entries []AccountEntry,
	auth *AuthHandler,
	limit uint32,
	fn listFetcher,
) ([]models.Email, bool, []AccountFetchResult) {

	type primaryResult struct {
		emails []models.Email
		more   bool
		err    error
	}

	// Fetch primary account — already connected.
	primCh := make(chan primaryResult, 1)
	go func() {
		emails, more, err := fn(primaryClient, limit)
		primCh <- primaryResult{emails, more, err}
	}()

	// Fetch additional accounts concurrently.
	fetcher := &unifiedFetcher{auth: auth, fn: fn, limit: limit}
	results := make([]AccountFetchResult, len(entries))
	var wg sync.WaitGroup
	for i, entry := range entries {
		wg.Add(1)
		go func(idx int, e AccountEntry) {
			defer wg.Done()
			results[idx] = fetcher.fetchForEntry(e)
		}(i, entry)
	}

	// Wait for primaries.
	prim := <-primCh
	wg.Wait()

	// Build primary result.
	primaryFetchResult := AccountFetchResult{
		AccountEmail: primaryEmail,
		AccountLabel: primaryLabel,
		AccountColor: primaryColor,
		Emails:       prim.emails,
		Err:          prim.err,
		More:         prim.more,
	}
	if primaryFetchResult.AccountLabel == "" {
		primaryFetchResult.AccountLabel = primaryEmail
	}

	// Tag primary emails.
	if prim.err == nil {
		for i := range primaryFetchResult.Emails {
			primaryFetchResult.Emails[i].AccountEmail = primaryEmail
			primaryFetchResult.Emails[i].AccountLabel = primaryFetchResult.AccountLabel
			primaryFetchResult.Emails[i].AccountColor = primaryColor
		}
	} else {
		log.Printf("unified: primary account %s fetch error: %v", primaryEmail, prim.err)
	}

	// Merge all results.
	allResults := append([]AccountFetchResult{primaryFetchResult}, results...)

	var merged []models.Email
	for _, r := range allResults {
		if r.Err != nil {
			continue
		}
		merged = append(merged, r.Emails...)
	}

	// Sort by date descending (most-recent first).
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].Date.After(merged[j].Date)
	})

	anyMore := false
	for _, r := range allResults {
		if r.Err == nil && r.More {
			anyMore = true
		}
	}
	return merged, anyMore, allResults
}
