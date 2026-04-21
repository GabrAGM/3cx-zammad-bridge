package zammadbridge

import "strings"

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
	if !matchesDirection(z.Config.Zammad.AutoCreateDirections, call.Direction) {
		return false
	}
	if !matchesExtension(z.Config.Zammad.ExtensionFilterMode, z.Config.Zammad.ExtensionFilter, call.AgentNumber) {
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
// calls; with mode "exclude" allows all calls.
func matchesExtension(mode string, list []string, agentNumber string) bool {
	m := strings.ToLower(strings.TrimSpace(mode))
	if m == "" || m == "all" {
		return true
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
