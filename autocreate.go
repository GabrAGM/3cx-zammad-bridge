package zammadbridge

import (
	"strings"
	"time"
)

// ShouldAutoCreate decides whether the bridge should auto-create a Zammad
// ticket (and, if necessary, a Zammad user) for the given call based on the
// configured direction and extension filters.
//
// The master toggle `auto_create_ticket` is NOT checked here — callers should
// gate on it separately. This function only evaluates the per-call filters so
// it stays pure and easy to unit test.
func (z *ZammadBridge) ShouldAutoCreate(call *CallInformation) bool {
	if call == nil {
		return false
	}
	s := z.GetAutoCreateSettings()
	if !matchesDirection(s.Directions, call.Direction) {
		return false
	}
	if !matchesExtension(s.ExtMode, s.ExtList, call.AgentNumber) {
		return false
	}
	return true
}

// matchesDirection returns true when the configured direction selector permits
// a call with the given direction. Accepts "all" / "" / "inbound" / "outbound"
// / "none" (case-insensitive). Call direction is compared loosely so that
// "Inbound", "in", "Outbound", "out" all work.
func matchesDirection(configured, callDirection string) bool {
	mode := strings.ToLower(strings.TrimSpace(configured))
	if mode == "" || mode == "all" || mode == "both" {
		return true
	}
	if mode == "none" {
		return false
	}

	dir := strings.ToLower(strings.TrimSpace(callDirection))
	switch mode {
	case "inbound", "in":
		return dir == "inbound" || dir == "in"
	case "outbound", "out":
		return dir == "outbound" || dir == "out"
	}
	// Unknown mode -> fail closed so misconfiguration doesn't silently
	// auto-create tickets for every call.
	return false
}

// matchesExtension returns true when the configured extension filter permits
// the given agent extension. An empty list with mode "include" blocks all
// calls; with mode "exclude" allows all calls. An unset mode fails closed.
func matchesExtension(mode string, list []string, agentNumber string) bool {
	m := strings.ToLower(strings.TrimSpace(mode))
	if m == "all" {
		return true
	}
	if m == "" {
		// No decision was ever recorded about which extensions may open
		// tickets. Fail closed: on a PBX shared with other business lines a
		// permissive default tickets every answered call, whoever answered it.
		// Set the mode explicitly to "all" to opt back into that.
		return false
	}

	agent := strings.TrimSpace(agentNumber)
	inList := false
	for _, ext := range list {
		if strings.TrimSpace(ext) == agent && agent != "" {
			inList = true
			break
		}
	}

	switch m {
	case "include", "allow", "whitelist":
		return inList
	case "exclude", "deny", "blacklist":
		return !inList
	}
	// Unknown mode -> fail closed.
	return false
}

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
