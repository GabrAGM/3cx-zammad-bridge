package zammadbridge

import "testing"

func newBridgeWithZammad(directions, extMode string, extList []string) *ZammadBridge {
	cfg := &Config{}
	cfg.Zammad.AutoCreateDirections = directions
	cfg.Zammad.ExtensionFilterMode = extMode
	cfg.Zammad.ExtensionFilter = extList
	return &ZammadBridge{Config: cfg}
}

func TestShouldAutoCreate_NilCall(t *testing.T) {
	z := newBridgeWithZammad("all", "all", nil)
	if z.ShouldAutoCreate(nil) {
		t.Fatalf("nil call must not trigger auto-create")
	}
}

func TestShouldAutoCreate_DirectionMatrix(t *testing.T) {
	cases := []struct {
		name       string
		configured string
		direction  string
		want       bool
	}{
		{"default empty == all (inbound)", "", "Inbound", true},
		{"default empty == all (outbound)", "", "Outbound", true},
		{"all (inbound)", "all", "Inbound", true},
		{"all (outbound)", "all", "Outbound", true},
		{"both synonym (inbound)", "both", "Inbound", true},
		{"inbound allows inbound", "inbound", "Inbound", true},
		{"inbound allows in-shorthand", "inbound", "in", true},
		{"inbound blocks outbound", "inbound", "Outbound", false},
		{"outbound allows outbound", "outbound", "Outbound", true},
		{"outbound allows out-shorthand", "outbound", "out", true},
		{"outbound blocks inbound", "outbound", "Inbound", false},
		{"none blocks all", "none", "Inbound", false},
		{"case insensitive mode", "INBOUND", "Inbound", true},
		{"unknown mode fails closed", "garbage", "Inbound", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			z := newBridgeWithZammad(tc.configured, "all", nil)
			call := &CallInformation{Direction: tc.direction, AgentNumber: "100"}
			got := z.ShouldAutoCreate(call)
			if got != tc.want {
				t.Fatalf("direction=%q call=%q: got %v, want %v", tc.configured, tc.direction, got, tc.want)
			}
		})
	}
}

func TestShouldAutoCreate_ExtensionInclude(t *testing.T) {
	z := newBridgeWithZammad("all", "include", []string{"100", "101"})

	if !z.ShouldAutoCreate(&CallInformation{Direction: "Inbound", AgentNumber: "100"}) {
		t.Fatalf("extension 100 must be allowed under include-list")
	}
	if !z.ShouldAutoCreate(&CallInformation{Direction: "Inbound", AgentNumber: "101"}) {
		t.Fatalf("extension 101 must be allowed under include-list")
	}
	if z.ShouldAutoCreate(&CallInformation{Direction: "Inbound", AgentNumber: "200"}) {
		t.Fatalf("extension 200 must NOT be allowed under include-list")
	}
	if z.ShouldAutoCreate(&CallInformation{Direction: "Inbound", AgentNumber: ""}) {
		t.Fatalf("empty agent must NOT match include-list")
	}
}

func TestShouldAutoCreate_ExtensionExclude(t *testing.T) {
	z := newBridgeWithZammad("all", "exclude", []string{"100", "101"})

	if z.ShouldAutoCreate(&CallInformation{Direction: "Inbound", AgentNumber: "100"}) {
		t.Fatalf("extension 100 must be blocked under exclude-list")
	}
	if !z.ShouldAutoCreate(&CallInformation{Direction: "Inbound", AgentNumber: "200"}) {
		t.Fatalf("extension 200 must be allowed under exclude-list")
	}
	if !z.ShouldAutoCreate(&CallInformation{Direction: "Inbound", AgentNumber: ""}) {
		t.Fatalf("empty agent must pass exclude-list (not in list)")
	}
}

func TestShouldAutoCreate_ExtensionAllByDefault(t *testing.T) {
	z := newBridgeWithZammad("all", "", nil)
	if !z.ShouldAutoCreate(&CallInformation{Direction: "Inbound", AgentNumber: "anything"}) {
		t.Fatalf("empty extension mode must allow all")
	}
	z2 := newBridgeWithZammad("all", "all", []string{"100"})
	if !z2.ShouldAutoCreate(&CallInformation{Direction: "Inbound", AgentNumber: "999"}) {
		t.Fatalf("mode=all must ignore the list")
	}
}

func TestShouldAutoCreate_ExtensionUnknownModeFailsClosed(t *testing.T) {
	z := newBridgeWithZammad("all", "garbage", []string{"100"})
	if z.ShouldAutoCreate(&CallInformation{Direction: "Inbound", AgentNumber: "100"}) {
		t.Fatalf("unknown extension mode must fail closed")
	}
}

func TestShouldAutoCreate_CombinedFilters(t *testing.T) {
	// Only inbound calls to extensions 100/101.
	z := newBridgeWithZammad("inbound", "include", []string{"100", "101"})

	cases := []struct {
		direction, agent string
		want             bool
	}{
		{"Inbound", "100", true},
		{"Inbound", "102", false}, // right direction, wrong extension
		{"Outbound", "100", false}, // right extension, wrong direction
		{"Outbound", "999", false},
	}
	for _, tc := range cases {
		got := z.ShouldAutoCreate(&CallInformation{Direction: tc.direction, AgentNumber: tc.agent})
		if got != tc.want {
			t.Errorf("dir=%s agent=%s: got %v want %v", tc.direction, tc.agent, got, tc.want)
		}
	}
}

func TestShouldAutoCreate_ExtensionListWhitespace(t *testing.T) {
	z := newBridgeWithZammad("all", "include", []string{" 100 ", "101"})
	if !z.ShouldAutoCreate(&CallInformation{Direction: "Inbound", AgentNumber: "100"}) {
		t.Fatalf("whitespace in configured list must be trimmed")
	}
}
