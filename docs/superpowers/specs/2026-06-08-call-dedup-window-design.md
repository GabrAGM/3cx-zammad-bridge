# Auto-create de-duplication window — design

**Date:** 2026-06-08
**Branch:** `feature/configurable-auto-create`
**Status:** Approved (design), pending implementation

## Problem

The bridge auto-creates one Zammad ticket per completed call (`ZammadCreateTicket`,
called from `ZammadHangup`). It POSTs a new ticket unconditionally — there is no
lookup for an existing ticket first.

When a customer's call drops and is re-dialled, or is transferred internally,
3CX reports more than one completed call, so the bridge creates more than one
ticket for what the customer experienced as a single contact.

**Observed case:** tickets `8892856` and `8892857` — same caller `01223111842`,
same agent (Ramy Naeem, ext 126), inbound, created **2m41s apart**
(2026-06-02 09:40:24 and 09:43:05 UTC). Reported by the LC team as a duplicate.

## Goal

Before auto-creating a phone ticket, consolidate repeat calls from the same
external number into the existing open ticket instead of opening a new one.

## Non-goals

- Merging existing duplicate tickets already in Zammad (one-off cleanup, not this feature).
- De-duplicating email/chat tickets — phone group only.
- Reworking how the customer/user is matched or created.

## Decisions (agreed)

| Decision | Value |
|----------|-------|
| Dedup window | **10 minutes** |
| On duplicate | **Append the repeat call as an article on the existing open ticket** |
| Config surface | **`config.yaml` field + admin-UI numeric input, both in this PR** |
| Disabled value | `auto_create_dedup_window_minutes: 0` (preserves always-create behaviour) |
| Default when unset | `0` (disabled) — backward-compatible with upstream |

## Approach

**Search-before-create in the bridge.** On hangup, when auto-create passes its
existing direction/extension filters, search Zammad for a recent open phone
ticket from the same external number. If found within the window, append the
call as an article; otherwise create a ticket as today.

Rejected alternatives:
- *In-memory dedup map* — loses state on restart; goes stale if the ticket is
  closed/merged in Zammad (would append to a closed ticket). Fragile.
- *Fix inside Zammad (trigger/scheduler)* — triggers cannot cleanly "merge into
  a recent open ticket by number + time"; not the bridge's concern.

## Matching rule

A new call matches an existing ticket when **all** hold:

- customer = this call's external number (`call.ExternalNumber`),
- group = configured `Zammad.TicketGroup` (e.g. `LC Phone`),
- state ∈ {`new`, `open`},
- `created_at >= now - auto_create_dedup_window_minutes`.

If multiple match, use the most recent. Direction-agnostic: matching on the
external customer number covers inbound redials and outbound re-dials alike.

## Append behaviour

POST a `phone`-type article to the matched ticket containing the same
call-detail body the new ticket would have had, prefixed with a marker such as
`Repeat call (within 10 min)`. The ticket keeps its current state. Log the
matched `ticket_id` with `appended=true`.

## Error handling — fail open

If the ticket search errors or times out, **log and fall through to creating a
normal ticket**. A dedup-lookup failure must never cause a call to be dropped.
This matches the defensive posture used elsewhere in the bridge.

## Code shape

- **`autocreate.go`** — pure, unit-tested helper
  `withinDedupWindow(createdAt, now time.Time, minutes int) bool`. Returns
  `false` when `minutes <= 0` (disabled). No I/O.
- **`zammad.go`**
  - `ZammadFindRecentOpenPhoneTicket(call *CallInformation) (id int, found bool, err error)`
    — mirrors `ZammadLookupUser`; uses `GET /api/v1/tickets/search` filtered by
    customer + group + state, newest first, and applies `withinDedupWindow` to
    the result's `created_at`.
  - `ZammadAppendCallArticle(ticketID int, call *CallInformation, cause string) error`
    — POST `/api/v1/ticket_articles`.
  - `ZammadHangup` chooses append-vs-create based on the lookup.
- **`config.go`** — add `AutoCreateDedupWindowMinutes int` (yaml
  `auto_create_dedup_window_minutes`) to the `Zammad` struct.
- **`bridge.go`** — thread the value through `AutoCreateSettings`,
  `GetAutoCreateSettings`, `SetAutoCreateSettings`, `loadAutoCreateFromConfig`
  (same atomic hot-swap pattern as the other auto-create settings).
- **`admin.go` + template** — one numeric input
  ("Consolidate repeat calls within N minutes (0 = off)") in the auto-create
  section, validated on save via the existing path.
- **Tests** — table tests for `withinDedupWindow` (disabled, inside, outside,
  boundary); extend `autocreate_test.go`.

## Known limitation (accepted)

Zammad's `/tickets/search` is Elasticsearch-backed with ~1s refresh lag, so two
hangups in the *same* poll tick could both create. Irrelevant to the target case
(calls minutes apart); worst case equals current behaviour. Not engineered
around.

## Config example

```yaml
Zammad:
  auto_create_ticket: true
  ticket_group: "LC Phone"
  auto_create_directions: "inbound"
  auto_create_dedup_window_minutes: 10   # 0 disables consolidation
```
