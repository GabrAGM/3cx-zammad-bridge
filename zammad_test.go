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
