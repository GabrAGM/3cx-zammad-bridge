package zammadbridge

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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
	created := time.Now().UTC().Add(-2 * time.Minute).Format("2006-01-02T15:04:05.000Z07:00")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/v1/users/search"):
			_, _ = w.Write([]byte(`[{"id":42}]`))
		case strings.HasPrefix(r.URL.Path, "/api/v1/tickets/search"):
			q := r.URL.Query().Get("query")
			if !strings.Contains(q, "customer_id:42") || !strings.Contains(q, "group.name:") || !strings.Contains(q, "state.name:new") {
				t.Errorf("search query missing expected fields: %s", q)
			}
			_, _ = w.Write([]byte(`[{"id":100,"created_at":"` + created + `"}]`))
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
	created := time.Now().UTC().Add(-30 * time.Minute).Format("2006-01-02T15:04:05.000Z07:00")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/v1/users/search"):
			_, _ = w.Write([]byte(`[{"id":42}]`))
		case strings.HasPrefix(r.URL.Path, "/api/v1/tickets/search"):
			_, _ = w.Write([]byte(`[{"id":100,"created_at":"` + created + `"}]`))
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

func TestZammadHangup_AppendsOnRecentDuplicate(t *testing.T) {
	recent := time.Now().UTC().Add(-2 * time.Minute).Format("2006-01-02T15:04:05.000Z07:00")
	var createdTicket, appended bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/cti":
			w.WriteHeader(http.StatusOK)
		case strings.HasPrefix(r.URL.Path, "/api/v1/users/search"):
			_, _ = w.Write([]byte(`[{"id":42}]`))
		case strings.HasPrefix(r.URL.Path, "/api/v1/tickets/search"):
			_, _ = w.Write([]byte(`[{"id":100,"created_at":"` + recent + `"}]`))
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
			_, _ = w.Write([]byte(`[]`))
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

func TestZammadHangup_CreatesOnLookupError(t *testing.T) {
	var createdTicket bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/cti":
			w.WriteHeader(http.StatusOK)
		case strings.HasPrefix(r.URL.Path, "/api/v1/users/search"):
			_, _ = w.Write([]byte(`[{"id":42}]`))
		case strings.HasPrefix(r.URL.Path, "/api/v1/tickets/search"):
			w.WriteHeader(http.StatusInternalServerError) // dedup lookup fails
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
		t.Errorf("fail-open: expected a new ticket to be created when the dedup lookup errors")
	}
}
