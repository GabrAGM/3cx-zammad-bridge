# Call De-duplication Window Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Before auto-creating a phone ticket on call hangup, consolidate repeat calls from the same external number into the existing open ticket instead of opening a duplicate.

**Architecture:** Search-before-create in the bridge. On hangup (after the existing direction/extension filters pass), look up a recent new/open ticket in the phone group for this caller; if it was created within a configurable window, append the call as an article, otherwise create a ticket as today. Fail-open on any lookup error. Window is hot-swappable via the atomic `AutoCreateSettings` snapshot and editable in the admin UI.

**Tech Stack:** Go, Zammad REST API (`/api/v1/tickets/search`, `/api/v1/tickets/{id}`, `/api/v1/ticket_articles`), `net/http`, `html/template`, `gopkg.in/yaml.v2`, standard `testing` + `httptest`.

---

## File structure

- `config.go` — add `AutoCreateDedupWindowMinutes` to the `Zammad` config struct.
- `bridge.go` — add `DedupWindowMinutes` to `AutoCreateSettings`; thread through `SetAutoCreateSettings` + `loadAutoCreateFromConfig`.
- `autocreate.go` — pure `withinDedupWindow` helper (unit-tested).
- `zammad.go` — `callTypeFor` + `buildCallBody` (extracted, DRY), `ZammadFindRecentOpenPhoneTicket`, `ZammadAppendCallArticle`, `autoCreateOrAppend`; wire into `ZammadHangup`.
- `admin.go` — `DedupWindowMinutes` in `adminView`; numeric input in template; parse/validate/persist in `adminSaveHandler`.
- `config.yaml.dist`, `README.md` — document the new field.
- Tests: extend `autocreate_test.go`, `admin_test.go`; new `zammad_test.go`.

---

### Task 1: Config field + AutoCreateSettings plumbing

**Files:**
- Modify: `config.go` (Zammad struct, ~line 56)
- Modify: `bridge.go` (`AutoCreateSettings` ~line 41, `SetAutoCreateSettings` ~line 62, `loadAutoCreateFromConfig` ~line 107)
- Test: `autocreate_test.go`

- [ ] **Step 1: Write the failing test**

Add to `autocreate_test.go`:

```go
func TestDedupWindow_Plumbing(t *testing.T) {
	cfg := &Config{}
	cfg.Zammad.AutoCreateDedupWindowMinutes = 10
	b := &ZammadBridge{Config: cfg}
	b.loadAutoCreateFromConfig()

	if got := b.GetAutoCreateSettings().DedupWindowMinutes; got != 10 {
		t.Fatalf("loadAutoCreateFromConfig: DedupWindowMinutes = %d, want 10", got)
	}

	b.SetAutoCreateSettings(AutoCreateSettings{DedupWindowMinutes: 25})
	if got := b.GetAutoCreateSettings().DedupWindowMinutes; got != 25 {
		t.Fatalf("after hot-swap: DedupWindowMinutes = %d, want 25", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run TestDedupWindow_Plumbing -v`
Expected: FAIL — `cfg.Zammad.AutoCreateDedupWindowMinutes` and `AutoCreateSettings.DedupWindowMinutes` undefined (compile error).

- [ ] **Step 3: Add the config field**

In `config.go`, inside the `Zammad struct`, after the `ExtensionFilter` field:

```go
		// ExtensionFilter lists the 3CX extensions (agent numbers) that the filter
		// mode applies to. Ignored when mode is "all".
		ExtensionFilter []string `yaml:"extension_filter"`
		// AutoCreateDedupWindowMinutes consolidates repeat calls from the same
		// external number into an existing new/open phone ticket created within
		// this many minutes, instead of opening a duplicate. 0 disables
		// consolidation (always create) — the backward-compatible default.
		AutoCreateDedupWindowMinutes int `yaml:"auto_create_dedup_window_minutes"`
```

- [ ] **Step 4: Add the settings field + plumbing**

In `bridge.go`, add to `AutoCreateSettings`:

```go
type AutoCreateSettings struct {
	Enabled            bool
	Directions         string
	ExtMode            string
	ExtList            []string
	DedupWindowMinutes int
}
```

In `SetAutoCreateSettings`, include it in the snapshot:

```go
	snapshot := &AutoCreateSettings{
		Enabled:            s.Enabled,
		Directions:         s.Directions,
		ExtMode:            s.ExtMode,
		ExtList:            append([]string(nil), s.ExtList...),
		DedupWindowMinutes: s.DedupWindowMinutes,
	}
```

In `loadAutoCreateFromConfig`:

```go
	z.SetAutoCreateSettings(AutoCreateSettings{
		Enabled:            z.Config.Zammad.AutoCreateTicket,
		Directions:         z.Config.Zammad.AutoCreateDirections,
		ExtMode:            z.Config.Zammad.ExtensionFilterMode,
		ExtList:            z.Config.Zammad.ExtensionFilter,
		DedupWindowMinutes: z.Config.Zammad.AutoCreateDedupWindowMinutes,
	})
```

(`GetAutoCreateSettings` returns `*p`, a full struct copy, so it needs no change.)

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./... -run TestDedupWindow_Plumbing -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add config.go bridge.go autocreate_test.go
git commit -m "feat: add auto_create_dedup_window_minutes config + settings plumbing"
```

---

### Task 2: Pure `withinDedupWindow` helper

**Files:**
- Modify: `autocreate.go`
- Test: `autocreate_test.go`

- [ ] **Step 1: Write the failing test**

Add to `autocreate_test.go` (and add `"testing"` is already imported; add `"time"` to the import block — change `import "testing"` to a block):

```go
import (
	"testing"
	"time"
)
```

```go
func TestWithinDedupWindow(t *testing.T) {
	now := time.Date(2026, 6, 2, 9, 43, 0, 0, time.UTC)
	cases := []struct {
		name      string
		createdAt time.Time
		minutes   int
		want      bool
	}{
		{"disabled zero", now.Add(-1 * time.Minute), 0, false},
		{"disabled negative", now.Add(-1 * time.Minute), -5, false},
		{"inside window", now.Add(-3 * time.Minute), 10, true},
		{"outside window", now.Add(-11 * time.Minute), 10, false},
		{"exact boundary is inside", now.Add(-10 * time.Minute), 10, true},
		{"zero createdAt", time.Time{}, 10, false},
		{"created in future still inside", now.Add(1 * time.Minute), 10, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := withinDedupWindow(tc.createdAt, now, tc.minutes); got != tc.want {
				t.Fatalf("withinDedupWindow(%v, now, %d) = %v, want %v", tc.createdAt, tc.minutes, got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run TestWithinDedupWindow -v`
Expected: FAIL — `withinDedupWindow` undefined.

- [ ] **Step 3: Implement the helper**

In `autocreate.go`, change the import and add the function:

```go
import (
	"strings"
	"time"
)
```

```go
// withinDedupWindow reports whether createdAt is recent enough (>= now-minutes)
// to consolidate a new call into that ticket. minutes <= 0 disables
// consolidation; a zero createdAt never qualifies. The boundary is inclusive.
func withinDedupWindow(createdAt, now time.Time, minutes int) bool {
	if minutes <= 0 || createdAt.IsZero() {
		return false
	}
	cutoff := now.Add(-time.Duration(minutes) * time.Minute)
	return !createdAt.Before(cutoff)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./... -run TestWithinDedupWindow -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add autocreate.go autocreate_test.go
git commit -m "feat: add withinDedupWindow pure helper"
```

---

### Task 3: Extract `callTypeFor` + `buildCallBody` (DRY)

**Files:**
- Modify: `zammad.go` (`ZammadCreateTicket` ~lines 211-247)
- Test: `zammad_test.go` (new)

This refactor introduces no behaviour change; it lets the append path reuse the call-body formatting.

- [ ] **Step 1: Write the failing test**

Create `zammad_test.go`:

```go
package zammadbridge

import (
	"strings"
	"testing"
)

func TestCallTypeFor(t *testing.T) {
	cases := []struct {
		direction, cause, want string
	}{
		{"Inbound", "normalClearing", "Inbound"},
		{"Outbound", "normalClearing", "Outbound"},
		{"out", "normalClearing", "Outbound"},
		{"Inbound", "cancel", "Missed"},
		{"Inbound", "noAnswer", "Missed"},
	}
	for _, tc := range cases {
		call := &CallInformation{Direction: tc.direction}
		if got := callTypeFor(call, tc.cause); got != tc.want {
			t.Errorf("callTypeFor(%q,%q) = %q, want %q", tc.direction, tc.cause, got, tc.want)
		}
	}
}

func TestBuildCallBody(t *testing.T) {
	call := &CallInformation{CallFrom: "01223111842", AgentName: "Ramy Naeem", AgentNumber: "126", Direction: "Inbound"}
	body := buildCallBody(call, "Inbound")
	for _, want := range []string{"Caller: 01223111842", "Agent: Ramy Naeem (126)", "Call Type: Inbound", "Direction: Inbound"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q; got:\n%s", want, body)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run 'TestCallTypeFor|TestBuildCallBody' -v`
Expected: FAIL — `callTypeFor` / `buildCallBody` undefined.

- [ ] **Step 3: Add helpers and refactor `ZammadCreateTicket`**

In `zammad.go`, add the two helpers (above `ZammadCreateTicket`):

```go
// callTypeFor derives the human call-type label from direction + hangup cause.
func callTypeFor(call *CallInformation, cause string) string {
	callType := "Inbound"
	if call.Direction == "Outbound" || call.Direction == "out" {
		callType = "Outbound"
	}
	if cause == "cancel" || cause == "noAnswer" {
		callType = "Missed"
	}
	return callType
}

// buildCallBody renders the call-detail body shared by ticket creation and the
// repeat-call append article.
func buildCallBody(call *CallInformation, callType string) string {
	var parts []string
	parts = append(parts, fmt.Sprintf("Caller: %s", call.CallFrom))
	if call.AgentName != "" {
		parts = append(parts, fmt.Sprintf("Agent: %s (%s)", call.AgentName, call.AgentNumber))
	} else if call.AgentNumber != "" {
		parts = append(parts, fmt.Sprintf("Agent: %s", call.AgentNumber))
	}
	parts = append(parts, fmt.Sprintf("Call Type: %s", callType))
	parts = append(parts, fmt.Sprintf("Direction: %s", call.Direction))
	return strings.Join(parts, "\n")
}
```

Replace the inline body/callType construction in `ZammadCreateTicket` (the block building `callType`, `bodyParts`, and `ticket.Article.Body`) with:

```go
	callType := callTypeFor(call, cause)

	// Look up customer
	customerID, _ := z.ZammadLookupUser(call.CallFrom)

	ticket := ZammadTicketRequest{
		Title: fmt.Sprintf("Phone Call from %s (%s)", call.CallFrom, callType),
		Group: group,
		Article: ZammadArticleCreate{
			Subject:  "Phone Call",
			Body:     buildCallBody(call, callType),
			Type:     "phone",
			Internal: false,
		},
	}
```

(Delete the now-unused `bodyParts` lines and the old `callType` block above `customerID`.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./... -run 'TestCallTypeFor|TestBuildCallBody' -v`
Expected: PASS
Run: `go build ./...`
Expected: builds clean (confirms `ZammadCreateTicket` still compiles).

- [ ] **Step 5: Commit**

```bash
git add zammad.go zammad_test.go
git commit -m "refactor: extract callTypeFor + buildCallBody from ZammadCreateTicket"
```

---

### Task 4: `ZammadFindRecentOpenPhoneTicket` + `ZammadAppendCallArticle`

**Files:**
- Modify: `zammad.go` (imports; add types + two methods)
- Test: `zammad_test.go`

- [ ] **Step 1: Write the failing test**

Add to `zammad_test.go` (extend imports to include `encoding/json`, `net/http`, `net/http/httptest`, `time`):

```go
import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)
```

```go
// newTestBridge points a bridge's Zammad API + CTI endpoint at a test server.
func newTestBridge(apiURL string) *ZammadBridge {
	cfg := &Config{}
	cfg.Zammad.ApiUrl = apiURL
	cfg.Zammad.ApiToken = "test-token"
	cfg.Zammad.TicketGroup = "LC Phone"
	cfg.Zammad.Endpoint = apiURL + "/cti"
	return &ZammadBridge{Config: cfg, ClientZammad: http.Client{}}
}

func TestFindRecentOpenPhoneTicket_Found(t *testing.T) {
	created := time.Now().UTC().Add(-2 * time.Minute).Format(time.RFC3339)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/v1/users/search"):
			_, _ = w.Write([]byte(`[{"id":42}]`))
		case strings.HasPrefix(r.URL.Path, "/api/v1/tickets/search"):
			_, _ = w.Write([]byte(`{"tickets":[100],"tickets_count":1}`))
		case r.URL.Path == "/api/v1/tickets/100":
			_, _ = w.Write([]byte(`{"id":100,"created_at":"` + created + `","state_id":2}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	z := newTestBridge(srv.URL)
	call := &CallInformation{ExternalNumber: "01223111842"}
	id, found, err := z.ZammadFindRecentOpenPhoneTicket(call, 10, time.Now())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !found || id != 100 {
		t.Fatalf("found=%v id=%d, want found=true id=100", found, id)
	}
}

func TestFindRecentOpenPhoneTicket_TooOld(t *testing.T) {
	created := time.Now().UTC().Add(-30 * time.Minute).Format(time.RFC3339)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/v1/users/search"):
			_, _ = w.Write([]byte(`[{"id":42}]`))
		case strings.HasPrefix(r.URL.Path, "/api/v1/tickets/search"):
			_, _ = w.Write([]byte(`{"tickets":[100],"tickets_count":1}`))
		case r.URL.Path == "/api/v1/tickets/100":
			_, _ = w.Write([]byte(`{"id":100,"created_at":"` + created + `","state_id":2}`))
		}
	}))
	defer srv.Close()

	z := newTestBridge(srv.URL)
	call := &CallInformation{ExternalNumber: "01223111842"}
	_, found, err := z.ZammadFindRecentOpenPhoneTicket(call, 10, time.Now())
	if err != nil || found {
		t.Fatalf("found=%v err=%v, want found=false err=nil", found, err)
	}
}

func TestFindRecentOpenPhoneTicket_NoCustomer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[]`)) // users/search returns nobody
	}))
	defer srv.Close()
	z := newTestBridge(srv.URL)
	_, found, err := z.ZammadFindRecentOpenPhoneTicket(&CallInformation{ExternalNumber: "099"}, 10, time.Now())
	if err != nil || found {
		t.Fatalf("found=%v err=%v, want found=false err=nil", found, err)
	}
}

func TestFindRecentOpenPhoneTicket_DisabledWindow(t *testing.T) {
	z := newTestBridge("http://invalid.invalid")
	_, found, err := z.ZammadFindRecentOpenPhoneTicket(&CallInformation{ExternalNumber: "099"}, 0, time.Now())
	if err != nil || found {
		t.Fatalf("disabled window must short-circuit: found=%v err=%v", found, err)
	}
}

func TestAppendCallArticle_PostsArticle(t *testing.T) {
	var gotBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/ticket_articles" || r.Method != http.MethodPost {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":555}`))
	}))
	defer srv.Close()

	z := newTestBridge(srv.URL)
	call := &CallInformation{CallFrom: "01223111842", Direction: "Inbound", AgentNumber: "126"}
	if err := z.ZammadAppendCallArticle(100, call, "normalClearing"); err != nil {
		t.Fatalf("append err: %v", err)
	}
	if gotBody["ticket_id"].(float64) != 100 {
		t.Errorf("ticket_id = %v, want 100", gotBody["ticket_id"])
	}
	if body, _ := gotBody["body"].(string); !strings.Contains(body, "Repeat call") {
		t.Errorf("article body missing 'Repeat call' marker: %q", body)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run 'TestFindRecentOpenPhoneTicket|TestAppendCallArticle' -v`
Expected: FAIL — methods undefined.

- [ ] **Step 3: Implement the methods**

In `zammad.go`, extend the import block to add `"net/url"` and `"time"`:

```go
import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)
```

Add the types + methods (near the other Zammad helpers):

```go
// ZammadTicketSearchResponse is the id-list shape returned by
// /api/v1/tickets/search.
type ZammadTicketSearchResponse struct {
	Tickets []int `json:"tickets"`
}

// ZammadTicketDetail is the minimal projection of GET /api/v1/tickets/{id}.
type ZammadTicketDetail struct {
	ID        int    `json:"id"`
	CreatedAt string `json:"created_at"`
}

// ZammadArticleAppend is the body for POST /api/v1/ticket_articles.
type ZammadArticleAppend struct {
	TicketID int    `json:"ticket_id"`
	Subject  string `json:"subject"`
	Body     string `json:"body"`
	Type     string `json:"type"`
	Internal bool   `json:"internal"`
}

// ZammadFindRecentOpenPhoneTicket returns the most recent new/open ticket in
// the configured phone group whose customer is this call's external number,
// when it was created within windowMinutes. Returns (0,false,nil) when nothing
// qualifies and (0,false,err) on any API error so the caller can fail open.
func (z *ZammadBridge) ZammadFindRecentOpenPhoneTicket(call *CallInformation, windowMinutes int, now time.Time) (int, bool, error) {
	if windowMinutes <= 0 || call == nil || call.ExternalNumber == "" {
		return 0, false, nil
	}

	customerID, err := z.ZammadLookupUser(call.ExternalNumber)
	if err != nil {
		return 0, false, err
	}
	if customerID == 0 {
		return 0, false, nil
	}

	group := z.Config.Zammad.TicketGroup
	if group == "" {
		group = "Users"
	}

	query := fmt.Sprintf(`customer_id:%d AND group.name:"%s" AND (state.name:new OR state.name:open)`, customerID, group)
	searchURL := fmt.Sprintf("%s/api/v1/tickets/search?query=%s&limit=1&sort_by=created_at&order_by=desc",
		z.Config.Zammad.ApiUrl, url.QueryEscape(query))

	var search ZammadTicketSearchResponse
	if err := z.zammadGetJSON(searchURL, &search); err != nil {
		return 0, false, err
	}
	if len(search.Tickets) == 0 {
		return 0, false, nil
	}

	var detail ZammadTicketDetail
	detailURL := fmt.Sprintf("%s/api/v1/tickets/%d", z.Config.Zammad.ApiUrl, search.Tickets[0])
	if err := z.zammadGetJSON(detailURL, &detail); err != nil {
		return 0, false, err
	}

	createdAt, err := time.Parse(time.RFC3339, detail.CreatedAt)
	if err != nil {
		return 0, false, fmt.Errorf("unable to parse ticket created_at %q: %w", detail.CreatedAt, err)
	}
	if withinDedupWindow(createdAt, now, windowMinutes) {
		return detail.ID, true, nil
	}
	return 0, false, nil
}

// zammadGetJSON performs an authenticated GET and decodes a JSON body.
func (z *ZammadBridge) zammadGetJSON(rawURL string, out interface{}) error {
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Token token="+z.Config.Zammad.ApiToken)
	resp, err := z.ClientZammad.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("GET %s failed (HTTP %d): %s", rawURL, resp.StatusCode, string(body))
	}
	return json.Unmarshal(body, out)
}

// ZammadAppendCallArticle adds the call as a phone article on an existing
// ticket (used when a repeat call is consolidated).
func (z *ZammadBridge) ZammadAppendCallArticle(ticketID int, call *CallInformation, cause string) error {
	callType := callTypeFor(call, cause)
	article := ZammadArticleAppend{
		TicketID: ticketID,
		Subject:  "Phone Call",
		Body:     "Repeat call (consolidated into this ticket)\n\n" + buildCallBody(call, callType),
		Type:     "phone",
		Internal: false,
	}
	payload, err := json.Marshal(article)
	if err != nil {
		return fmt.Errorf("unable to serialize article JSON: %w", err)
	}
	req, err := http.NewRequest("POST", z.Config.Zammad.ApiUrl+"/api/v1/ticket_articles", bytes.NewBuffer(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Token token="+z.Config.Zammad.ApiToken)
	resp, err := z.ClientZammad.Do(req)
	if err != nil {
		return fmt.Errorf("unable to append article: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("article append failed (HTTP %d): %s", resp.StatusCode, string(data))
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./... -run 'TestFindRecentOpenPhoneTicket|TestAppendCallArticle' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add zammad.go zammad_test.go
git commit -m "feat: add recent-open-ticket lookup + repeat-call append article"
```

---

### Task 5: Wire dedup into `ZammadHangup`

**Files:**
- Modify: `zammad.go` (`ZammadHangup` auto-create block ~lines 131-146; add `autoCreateOrAppend`)
- Test: `zammad_test.go`

- [ ] **Step 1: Write the failing test**

Add to `zammad_test.go`:

```go
func TestZammadHangup_AppendsOnRecentDuplicate(t *testing.T) {
	created := time.Now().UTC().Add(-2 * time.Minute).Format(time.RFC3339)
	var createdTicket, appended bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/cti":
			w.WriteHeader(http.StatusOK)
		case strings.HasPrefix(r.URL.Path, "/api/v1/users/search"):
			_, _ = w.Write([]byte(`[{"id":42}]`))
		case strings.HasPrefix(r.URL.Path, "/api/v1/tickets/search"):
			_, _ = w.Write([]byte(`{"tickets":[100]}`))
		case r.URL.Path == "/api/v1/tickets/100":
			_, _ = w.Write([]byte(`{"id":100,"created_at":"` + created + `"}`))
		case r.URL.Path == "/api/v1/ticket_articles":
			appended = true
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":1}`))
		case r.URL.Path == "/api/v1/tickets":
			createdTicket = true
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":2}`))
		}
	}))
	defer srv.Close()

	z := newTestBridge(srv.URL)
	z.SetAutoCreateSettings(AutoCreateSettings{Enabled: true, Directions: "all", DedupWindowMinutes: 10})
	call := &CallInformation{ExternalNumber: "01223111842", CallFrom: "01223111842", Direction: "Inbound", AgentNumber: "126", ZammadInitialized: true}

	if err := z.ZammadHangup(call, "normalClearing"); err != nil {
		t.Fatalf("hangup err: %v", err)
	}
	if !appended {
		t.Errorf("expected repeat call to be appended")
	}
	if createdTicket {
		t.Errorf("must NOT create a new ticket when a recent duplicate exists")
	}
}

func TestZammadHangup_CreatesWhenNoDuplicate(t *testing.T) {
	var createdTicket bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/cti":
			w.WriteHeader(http.StatusOK)
		case strings.HasPrefix(r.URL.Path, "/api/v1/users/search"):
			_, _ = w.Write([]byte(`[{"id":42}]`))
		case strings.HasPrefix(r.URL.Path, "/api/v1/tickets/search"):
			_, _ = w.Write([]byte(`{"tickets":[]}`))
		case r.URL.Path == "/api/v1/tickets":
			createdTicket = true
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":2}`))
		}
	}))
	defer srv.Close()

	z := newTestBridge(srv.URL)
	z.SetAutoCreateSettings(AutoCreateSettings{Enabled: true, Directions: "all", DedupWindowMinutes: 10})
	call := &CallInformation{ExternalNumber: "01223111842", CallFrom: "01223111842", Direction: "Inbound", AgentNumber: "126", ZammadInitialized: true}

	if err := z.ZammadHangup(call, "normalClearing"); err != nil {
		t.Fatalf("hangup err: %v", err)
	}
	if !createdTicket {
		t.Errorf("expected a new ticket when there is no recent duplicate")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run TestZammadHangup -v`
Expected: FAIL — current `ZammadHangup` always creates (so `TestZammadHangup_AppendsOnRecentDuplicate` fails: `createdTicket` true / `appended` false).

- [ ] **Step 3: Implement the routing**

In `zammad.go`, replace the auto-create block inside `ZammadHangup`:

```go
	// Auto-create ticket if enabled and the call passes direction+extension filters
	settings := z.GetAutoCreateSettings()
	if settings.Enabled && z.Config.Zammad.ApiUrl != "" && z.Config.Zammad.ApiToken != "" {
		if z.ShouldAutoCreate(call) {
			z.autoCreateOrAppend(call, cause, settings.DedupWindowMinutes)
		} else {
			log.Debug().
				Str("call_id", call.CallUID).
				Str("direction", call.Direction).
				Str("agent", call.AgentNumber).
				Msg("Skipping Zammad ticket auto-creation (filtered out by config)")
		}
	}

	return nil
}

// autoCreateOrAppend consolidates a repeat call into a recent open phone ticket
// when one exists within the dedup window, otherwise creates a new ticket. It
// fails open: any lookup/append error falls through to ticket creation so a
// call is never dropped.
func (z *ZammadBridge) autoCreateOrAppend(call *CallInformation, cause string, windowMinutes int) {
	if windowMinutes > 0 {
		ticketID, found, err := z.ZammadFindRecentOpenPhoneTicket(call, windowMinutes, time.Now())
		if err != nil {
			log.Warn().Err(err).Str("call_id", call.CallUID).Msg("Dedup lookup failed; creating a new ticket")
		} else if found {
			if appendErr := z.ZammadAppendCallArticle(ticketID, call, cause); appendErr != nil {
				log.Error().Err(appendErr).Int("ticket_id", ticketID).Str("call_id", call.CallUID).
					Msg("Failed to append repeat call; creating a new ticket")
			} else {
				log.Info().Int("ticket_id", ticketID).Str("call_id", call.CallUID).Bool("appended", true).
					Msg("Repeat call consolidated into existing open ticket")
				return
			}
		}
	}

	if ticketErr := z.ZammadCreateTicket(call, cause); ticketErr != nil {
		log.Error().Err(ticketErr).Str("call_id", call.CallUID).Msg("Failed to create Zammad ticket")
	}
}
```

(Remove the old `if z.ShouldAutoCreate(call) { ticketErr := z.ZammadCreateTicket(...) ... }` create lines — they now live in `autoCreateOrAppend`.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./... -run TestZammadHangup -v`
Expected: PASS (both)

- [ ] **Step 5: Commit**

```bash
git add zammad.go zammad_test.go
git commit -m "feat: consolidate repeat calls into recent open ticket on hangup"
```

---

### Task 6: Admin UI field

**Files:**
- Modify: `admin.go` (imports; `adminTmpl`; `adminView`; `viewFromSettings`; `adminSaveHandler`; add `validDedupWindow` helper)
- Test: `admin_test.go`

- [ ] **Step 1: Write the failing test**

Add to `admin_test.go` (imports already include `strconv`? no — add `"strconv"` is not needed in the test; uses existing imports):

```go
func TestAdminSaveDedupWindowPersists(t *testing.T) {
	path := writeTmpConfig(t, "Zammad: {}\n")
	bridge := testBridgeFromFile(t, path)

	form := url.Values{}
	form.Set("inbound", "on")
	form.Set("extension_filter_mode", "all")
	form.Set("dedup_window_minutes", "10")

	req := httptest.NewRequest("POST", "/save", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	adminSaveHandler(bridge, path)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := bridge.GetAutoCreateSettings().DedupWindowMinutes; got != 10 {
		t.Errorf("in-memory DedupWindowMinutes = %d, want 10", got)
	}
	reloaded, err := LoadConfigFromYaml(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Zammad.AutoCreateDedupWindowMinutes != 10 {
		t.Errorf("disk DedupWindowMinutes = %d, want 10", reloaded.Zammad.AutoCreateDedupWindowMinutes)
	}
}

func TestAdminSaveRejectsBadDedupWindow(t *testing.T) {
	for _, bad := range []string{"-5", "abc"} {
		path := writeTmpConfig(t, "Zammad: {}\n")
		bridge := testBridgeFromFile(t, path)
		form := url.Values{}
		form.Set("inbound", "on")
		form.Set("extension_filter_mode", "all")
		form.Set("dedup_window_minutes", bad)
		req := httptest.NewRequest("POST", "/save", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		adminSaveHandler(bridge, path)(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("dedup_window_minutes=%q: expected 400, got %d", bad, rec.Code)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run 'TestAdminSaveDedupWindowPersists|TestAdminSaveRejectsBadDedupWindow' -v`
Expected: FAIL — field not parsed/persisted (window stays 0; invalid values still return 200).

- [ ] **Step 3: Add `strconv` import + view field + template + handler**

In `admin.go` imports, add `"strconv"`:

```go
import (
	"crypto/subtle"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v2"
)
```

Add to `adminView`:

```go
	ConfigPath         string
	Message            string
	MessageKind        string
	DedupWindowMinutes int
```

In `viewFromSettings`, add to the returned `adminView{...}`:

```go
		Message:            message,
		MessageKind:        kind,
		DedupWindowMinutes: s.DedupWindowMinutes,
```

In `adminTmpl`, add a new section immediately after the "Ticket auto-creation" `</div>` section and before the "Extension filter" section:

```html
  <div class="section">
    <div class="section-title">Repeat-call consolidation</div>
    <div class="switch-text" style="margin-bottom:.4rem">
      Consolidate repeat calls from the same number within
      <input type="text" name="dedup_window_minutes" value="{{.DedupWindowMinutes}}"
             inputmode="numeric" style="width:80px;display:inline-block;text-align:center"> minutes
    </div>
    <div class="hint">When a caller who already has a new/open ticket in the phone group calls again within this many minutes, the call is appended to that ticket instead of opening a new one. <code>0</code> turns consolidation off.</div>
  </div>
```

In `adminSaveHandler`, after the `extList` parsing block and before building `newSettings`, parse + validate the window:

```go
		dedupWindow, ok := parseDedupWindow(r.FormValue("dedup_window_minutes"))
		if !ok {
			writeError(w, tmpl, bridge, configPath, "Invalid repeat-call window — enter a whole number of minutes (0 turns it off).")
			return
		}
```

Add `DedupWindowMinutes` to the `newSettings` literal:

```go
		newSettings := AutoCreateSettings{
			Enabled:            inboundOn || outboundOn,
			Directions:         directions,
			ExtMode:            strings.ToLower(strings.TrimSpace(r.FormValue("extension_filter_mode"))),
			ExtList:            extList,
			DedupWindowMinutes: dedupWindow,
		}
```

Persist it alongside the other fields (after `fileCfg.Zammad.ExtensionFilter = newSettings.ExtList`):

```go
		fileCfg.Zammad.AutoCreateDedupWindowMinutes = newSettings.DedupWindowMinutes
```

Add the parse helper near `validExtMode`:

```go
// parseDedupWindow parses the admin form's dedup-window field. Empty means 0
// (off). Returns ok=false for non-numeric or negative input.
func parseDedupWindow(raw string) (int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}
```

(Optional, for log parity) add to the `log.Info()` call in `adminSaveHandler`:

```go
			Int("dedup_window_min", newSettings.DedupWindowMinutes).
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./... -run 'TestAdminSaveDedupWindowPersists|TestAdminSaveRejectsBadDedupWindow' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add admin.go admin_test.go
git commit -m "feat: admin UI field for repeat-call consolidation window"
```

---

### Task 7: Document the field

**Files:**
- Modify: `config.yaml.dist`
- Modify: `README.md`

- [ ] **Step 1: Update `config.yaml.dist`**

Under the `Zammad:` section, after the extension-filter keys, add:

```yaml
  # Consolidate repeat calls from the same external number into an existing
  # new/open ticket in ticket_group created within this many minutes, instead
  # of opening a duplicate. 0 disables consolidation (always create).
  auto_create_dedup_window_minutes: 0
```

- [ ] **Step 2: Update `README.md`**

In the auto-create section, add a short paragraph:

```markdown
### Repeat-call consolidation

`auto_create_dedup_window_minutes` (default `0`, off) consolidates repeat calls.
When a caller who already has a new/open ticket in `ticket_group` calls again
within the configured number of minutes, the bridge appends the call to that
ticket instead of creating a new one. Editable live in the admin UI. Lookups
fail open — if Zammad cannot be queried, a normal ticket is created.
```

- [ ] **Step 3: Commit**

```bash
git add config.yaml.dist README.md
git commit -m "docs: document auto_create_dedup_window_minutes"
```

---

### Task 8: Full build, vet, and test gate

**Files:** none (verification only)

- [ ] **Step 1: Vet**

Run: `go vet ./...`
Expected: no output (clean).

- [ ] **Step 2: Full test suite**

Run: `go test ./...`
Expected: all packages `ok`.

- [ ] **Step 3: Build the binary**

Run: `go build ./...`
Expected: builds clean.

- [ ] **Step 4: Confirm working tree committed**

Run: `git status -s`
Expected: empty (all changes committed across Tasks 1-7).

---

## Self-Review

**Spec coverage:**
- 10-min window → configurable `auto_create_dedup_window_minutes` (Task 1), default deployed value set in config (Task 7). ✓
- Append repeat call as article → `ZammadAppendCallArticle` + routing (Tasks 4-5). ✓
- Match rule (external number + phone group + new/open + within window) → `ZammadFindRecentOpenPhoneTicket` (Task 4). ✓
- config.yaml + admin UI in this PR → Tasks 6-7. ✓
- Disabled with `0` / backward-compatible default → `withinDedupWindow` short-circuit (Task 2) + default 0 (Task 1). ✓
- Fail-open on error → `autoCreateOrAppend` (Task 5). ✓
- Pure, unit-tested window helper → Task 2. ✓
- Known ES-lag limitation → documented in spec; not engineered around (no task needed). ✓

**Placeholder scan:** none — every step has concrete code/commands.

**Type consistency:** `AutoCreateSettings.DedupWindowMinutes` (Task 1) used identically in Tasks 5/6; `withinDedupWindow(createdAt, now, minutes)` signature consistent across Tasks 2/4; `callTypeFor`/`buildCallBody` defined in Task 3 and reused in Task 4; `ZammadFindRecentOpenPhoneTicket(call, windowMinutes, now)` and `ZammadAppendCallArticle(ticketID, call, cause)` signatures consistent Tasks 4/5.

**Note on Zammad search shape:** uses the two-step `/tickets/search` (id list) → `/tickets/{id}` (created_at) to avoid depending on the `assets`/`expand` response variant across Zammad versions.
