package zammadbridge

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTmpConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write tmp config: %v", err)
	}
	return path
}

func TestValidDirection(t *testing.T) {
	for _, d := range []string{"all", "inbound", "outbound", "none"} {
		if !validDirection(d) {
			t.Errorf("%q should be valid", d)
		}
	}
	for _, d := range []string{"", "both", "random", "IN"} {
		if validDirection(d) {
			t.Errorf("%q should be invalid", d)
		}
	}
}

func TestValidExtMode(t *testing.T) {
	for _, m := range []string{"all", "include", "exclude"} {
		if !validExtMode(m) {
			t.Errorf("%q should be valid", m)
		}
	}
	for _, m := range []string{"", "allow", "deny", "whitelist"} {
		if validExtMode(m) {
			t.Errorf("%q should be invalid", m)
		}
	}
}

func TestParseExtList(t *testing.T) {
	got := parseExtList("100\n\n 101 \n\t102\n")
	want := []string{"100", "101", "102"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("index %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestWriteConfigYAMLAtomic(t *testing.T) {
	path := writeTmpConfig(t, "Zammad:\n  auto_create_ticket: false\n")
	cfg, err := LoadConfigFromYaml(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	cfg.Zammad.AutoCreateTicket = true
	cfg.Zammad.AutoCreateDirections = "inbound"
	cfg.Zammad.ExtensionFilterMode = "exclude"
	cfg.Zammad.ExtensionFilter = []string{"908"}

	if err := writeConfigYAML(path, cfg); err != nil {
		t.Fatalf("write: %v", err)
	}
	reloaded, err := LoadConfigFromYaml(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !reloaded.Zammad.AutoCreateTicket ||
		reloaded.Zammad.AutoCreateDirections != "inbound" ||
		reloaded.Zammad.ExtensionFilterMode != "exclude" ||
		len(reloaded.Zammad.ExtensionFilter) != 1 ||
		reloaded.Zammad.ExtensionFilter[0] != "908" {
		t.Fatalf("round-trip mismatch: %+v", reloaded.Zammad)
	}

	// No stray .tmp files left behind in the directory
	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("leftover temp file after atomic write: %s", e.Name())
		}
	}
}

func TestBasicAuthBlocksWithoutCreds(t *testing.T) {
	cfg := &Config{}
	cfg.Admin.User = "mowafy"
	cfg.Admin.Pass = "s3cret"

	called := false
	handler := basicAuth(cfg, func(w http.ResponseWriter, r *http.Request) { called = true })

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	if called {
		t.Fatalf("inner handler must not be called without creds")
	}
	if rec.Header().Get("WWW-Authenticate") == "" {
		t.Errorf("missing WWW-Authenticate header")
	}
}

func TestBasicAuthAllowsWithCreds(t *testing.T) {
	cfg := &Config{}
	cfg.Admin.User = "mowafy"
	cfg.Admin.Pass = "s3cret"

	called := false
	handler := basicAuth(cfg, func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("GET", "/", nil)
	req.SetBasicAuth("mowafy", "s3cret")
	rec := httptest.NewRecorder()
	handler(rec, req)

	if !called {
		t.Fatalf("inner handler must be called with valid creds")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestBasicAuthRejectsWrongPassword(t *testing.T) {
	cfg := &Config{}
	cfg.Admin.User = "mowafy"
	cfg.Admin.Pass = "s3cret"

	handler := basicAuth(cfg, func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("should not be reached")
	})

	req := httptest.NewRequest("GET", "/", nil)
	req.SetBasicAuth("mowafy", "wrong")
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func testBridgeFromFile(t *testing.T, path string) *ZammadBridge {
	t.Helper()
	cfg, err := LoadConfigFromYaml(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	b := &ZammadBridge{Config: cfg}
	b.loadAutoCreateFromConfig()
	return b
}

func TestAdminIndexRenders(t *testing.T) {
	path := writeTmpConfig(t, `Zammad:
  auto_create_ticket: true
  auto_create_directions: inbound
  extension_filter_mode: exclude
  extension_filter:
    - "908"
`)
	bridge := testBridgeFromFile(t, path)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	adminIndexHandler(bridge, path)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// Inbound toggle must be checked (directions=inbound, enabled=true).
	if !strings.Contains(body, `name="inbound"`) {
		t.Errorf("missing inbound checkbox")
	}
	if !strings.Contains(body, `name="outbound"`) {
		t.Errorf("missing outbound checkbox")
	}
	// Crude check: the inbound input must have "checked", outbound must not.
	inboundIdx := strings.Index(body, `name="inbound"`)
	outboundIdx := strings.Index(body, `name="outbound"`)
	if inboundIdx < 0 || outboundIdx < 0 {
		t.Fatalf("checkbox inputs not found")
	}
	inboundTag := body[max(0, inboundIdx-80):inboundIdx+80]
	outboundTag := body[max(0, outboundIdx-80):outboundIdx+80]
	if !strings.Contains(inboundTag, "checked") {
		t.Errorf("inbound toggle should be checked, got: %s", inboundTag)
	}
	if strings.Contains(outboundTag, "checked") {
		t.Errorf("outbound toggle should not be checked, got: %s", outboundTag)
	}
	if !strings.Contains(body, `value="exclude"`) {
		t.Errorf("missing exclude option")
	}
	if !strings.Contains(body, "908") {
		t.Errorf("extension 908 not rendered")
	}
}

func TestAdminSaveRejectsInvalidFilterMode(t *testing.T) {
	path := writeTmpConfig(t, "Zammad:\n  auto_create_ticket: true\n")
	bridge := testBridgeFromFile(t, path)

	form := url.Values{}
	form.Set("inbound", "on")
	form.Set("extension_filter_mode", "garbage")

	req := httptest.NewRequest("POST", "/save", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	adminSaveHandler(bridge, path)(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on bad filter mode, got %d", rec.Code)
	}
}

func TestAdminSaveCheckboxesMapToDirections(t *testing.T) {
	cases := []struct {
		name          string
		inbound, out  bool
		wantEnabled   bool
		wantDirection string
	}{
		{"both on -> all", true, true, true, "all"},
		{"inbound only", true, false, true, "inbound"},
		{"outbound only", false, true, true, "outbound"},
		{"both off -> none + disabled", false, false, false, "none"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTmpConfig(t, "Zammad: {}\n")
			bridge := testBridgeFromFile(t, path)

			form := url.Values{}
			if tc.inbound {
				form.Set("inbound", "on")
			}
			if tc.out {
				form.Set("outbound", "on")
			}
			form.Set("extension_filter_mode", "all")

			req := httptest.NewRequest("POST", "/save", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rec := httptest.NewRecorder()
			adminSaveHandler(bridge, path)(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
			}
			got := bridge.GetAutoCreateSettings()
			if got.Enabled != tc.wantEnabled {
				t.Errorf("Enabled = %v, want %v", got.Enabled, tc.wantEnabled)
			}
			if got.Directions != tc.wantDirection {
				t.Errorf("Directions = %q, want %q", got.Directions, tc.wantDirection)
			}
		})
	}
}

func TestAdminSaveHotSwapsBridge(t *testing.T) {
	path := writeTmpConfig(t, "Zammad:\n  auto_create_ticket: false\n")
	bridge := testBridgeFromFile(t, path)

	// Sanity check: before save, bridge reports defaults.
	before := bridge.GetAutoCreateSettings()
	if before.Enabled || before.Directions != "" || before.ExtMode != "" {
		t.Fatalf("unexpected initial state: %+v", before)
	}

	form := url.Values{}
	form.Set("inbound", "on")
	form.Set("extension_filter_mode", "exclude")
	form.Set("extension_filter", "908\n909\n")

	req := httptest.NewRequest("POST", "/save", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	adminSaveHandler(bridge, path)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on save, got %d: %s", rec.Code, rec.Body.String())
	}

	// In-memory bridge must reflect the new settings without any restart.
	after := bridge.GetAutoCreateSettings()
	if !after.Enabled || after.Directions != "inbound" || after.ExtMode != "exclude" {
		t.Errorf("bridge not hot-swapped: %+v", after)
	}
	if len(after.ExtList) != 2 || after.ExtList[0] != "908" || after.ExtList[1] != "909" {
		t.Errorf("ext list not hot-swapped: %v", after.ExtList)
	}

	// Disk file must also reflect the new settings so container restarts
	// don't revert to old values.
	reloaded, err := LoadConfigFromYaml(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !reloaded.Zammad.AutoCreateTicket ||
		reloaded.Zammad.AutoCreateDirections != "inbound" ||
		reloaded.Zammad.ExtensionFilterMode != "exclude" ||
		len(reloaded.Zammad.ExtensionFilter) != 2 {
		t.Errorf("disk config mismatch: %+v", reloaded.Zammad)
	}
}

func TestShouldAutoCreateReflectsHotSwap(t *testing.T) {
	// Start with defaults: all directions, all extensions allowed.
	bridge := &ZammadBridge{Config: &Config{}}
	call := &CallInformation{Direction: "Outbound", AgentNumber: "908"}
	if !bridge.ShouldAutoCreate(call) {
		t.Fatalf("default config should allow the call")
	}

	// Hot-swap to inbound-only + exclude 908.
	bridge.SetAutoCreateSettings(AutoCreateSettings{
		Enabled:    true,
		Directions: "inbound",
		ExtMode:    "exclude",
		ExtList:    []string{"908"},
	})

	if bridge.ShouldAutoCreate(call) {
		t.Fatalf("after hot-swap, outbound-to-908 must be filtered out")
	}

	inbound := &CallInformation{Direction: "Inbound", AgentNumber: "100"}
	if !bridge.ShouldAutoCreate(inbound) {
		t.Fatalf("after hot-swap, inbound to other extension must still pass")
	}
}
