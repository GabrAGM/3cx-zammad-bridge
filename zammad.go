package zammadbridge

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

type ZammadTicketRequest struct {
	Title      string              `json:"title"`
	Group      string              `json:"group"`
	CustomerID int                 `json:"customer_id,omitempty"`
	Customer   string              `json:"customer,omitempty"`
	Article    ZammadArticleCreate `json:"article"`
}

type ZammadArticleCreate struct {
	Subject  string `json:"subject"`
	Body     string `json:"body"`
	Type     string `json:"type"`
	Internal bool   `json:"internal"`
}

type ZammadUserSearchResult struct {
	ID        int    `json:"id"`
	Firstname string `json:"firstname"`
	Lastname  string `json:"lastname"`
}

type ZammadTicketResponse struct {
	ID int `json:"id"`
}

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

type ZammadApiRequest struct {
	Event           string `json:"event"`
	From            string `json:"from"`
	To              string `json:"to"`
	Direction       string `json:"direction"`
	CallId          string `json:"call_id"`
	CallIdDuplicate string `json:"callid"`
	Cause           string `json:"cause,omitempty"`
	AnsweringNumber string `json:"answeringNumber,omitempty"`
	User            string `json:"user,omitempty"`
}

// ZammadNewCall notifies Zammad that a new call came in. This is the
// first call required to process calls using Zammad.
func (z *ZammadBridge) ZammadNewCall(call *CallInformation) error {
	err := z.ZammadPost(ZammadApiRequest{
		Event:           "newCall",
		From:            call.CallFrom,
		To:              call.CallTo,
		Direction:       call.Direction,
		CallId:          call.CallUID,
		AnsweringNumber: call.AgentNumber,
		User:            call.AgentName,
	})
	call.ZammadInitialized = true
	if err != nil {
		return err
	}

	return nil
}

// ZammadAnswer notifies Zammad that the existing call was now answered by
// an agent.
func (z *ZammadBridge) ZammadAnswer(call *CallInformation) error {
	var user string
	if call.Direction == "Inbound" {
		user = call.AgentName
	}

	if !call.ZammadInitialized {
		err := z.ZammadNewCall(call)
		if err != nil {
			return fmt.Errorf("unable to initialize call with Zammad: %w", err)
		}
	}

	if call.ZammadAnswered {
		return nil // Nothing to do - TODO: can we redirect the call in Zammad?
	}

	err := z.ZammadPost(ZammadApiRequest{
		Event:           "answer",
		From:            call.CallFrom,
		To:              call.CallTo,
		Direction:       call.Direction,
		CallId:          call.CallUID,
		AnsweringNumber: call.AgentNumber,
		User:            user,
	})
	call.ZammadAnswered = true

	if err != nil {
		return err
	}

	return nil
}

// ZammadHangup notifies Zammad that the call was finished with a given cause.
// Possible values for `cause` are: "cancel", "normalClearing"
func (z *ZammadBridge) ZammadHangup(call *CallInformation, cause string) error {
	if !call.ZammadInitialized {
		err := z.ZammadNewCall(call)
		if err != nil {
			return fmt.Errorf("unable to initialize call with Zammad: %w", err)
		}
	}

	err := z.ZammadPost(ZammadApiRequest{
		Event:           "hangup",
		From:            call.CallFrom,
		To:              call.CallTo,
		Direction:       call.Direction,
		CallId:          call.CallUID,
		Cause:           cause,
		AnsweringNumber: call.AgentNumber,
	})
	if err != nil {
		return err
	}

	// Auto-create ticket if enabled and the call passes direction+extension filters
	settings := z.GetAutoCreateSettings()
	if settings.Enabled && z.Config.Zammad.ApiUrl != "" && z.Config.Zammad.ApiToken != "" {
		if z.ShouldAutoCreate(call) {
			ticketErr := z.ZammadCreateTicket(call, cause)
			if ticketErr != nil {
				log.Error().Err(ticketErr).Str("call_id", call.CallUID).Msg("Failed to create Zammad ticket")
			}
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

// ZammadLookupUser searches for a Zammad user by phone number
func (z *ZammadBridge) ZammadLookupUser(phone string) (int, error) {
	url := fmt.Sprintf("%s/api/v1/users/search?query=phone:%s&limit=1", z.Config.Zammad.ApiUrl, phone)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Token token="+z.Config.Zammad.ApiToken)

	resp, err := z.ClientZammad.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var users []ZammadUserSearchResult
	if err := json.Unmarshal(body, &users); err != nil {
		return 0, err
	}
	if len(users) > 0 {
		return users[0].ID, nil
	}
	return 0, nil
}

// ZammadCreateUser creates a new user in Zammad with the given phone number
func (z *ZammadBridge) ZammadCreateUser(phone string) (int, error) {
	user := map[string]interface{}{
		"firstname": phone,
		"lastname":  "3CX",
		"phone":     phone,
	}
	body, _ := json.Marshal(user)

	req, err := http.NewRequest("POST", z.Config.Zammad.ApiUrl+"/api/v1/users", bytes.NewBuffer(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Token token="+z.Config.Zammad.ApiToken)

	resp, err := z.ClientZammad.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return 0, fmt.Errorf("user creation failed (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var result ZammadUserSearchResult
	json.Unmarshal(respBody, &result)
	log.Info().Int("user_id", result.ID).Str("phone", phone).Msg("Created Zammad user")
	return result.ID, nil
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
	group = strings.ReplaceAll(group, `"`, `\"`)

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
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("GET %s: JSON decode failed: %w", rawURL, err)
	}
	return nil
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

// ZammadCreateTicket creates a ticket in Zammad for the completed call
func (z *ZammadBridge) ZammadCreateTicket(call *CallInformation, cause string) error {
	group := z.Config.Zammad.TicketGroup
	if group == "" {
		group = "Users"
	}

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

	if customerID > 0 {
		ticket.CustomerID = customerID
	} else {
		// Create customer first
		newID, createErr := z.ZammadCreateUser(call.CallFrom)
		if createErr != nil {
			log.Warn().Err(createErr).Str("phone", call.CallFrom).Msg("Failed to create Zammad user, using default")
			ticket.CustomerID = 1
		} else {
			ticket.CustomerID = newID
		}
	}

	requestBody, err := json.Marshal(ticket)
	if err != nil {
		return fmt.Errorf("unable to serialize ticket JSON: %w", err)
	}

	req, err := http.NewRequest("POST", z.Config.Zammad.ApiUrl+"/api/v1/tickets", bytes.NewBuffer(requestBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Token token="+z.Config.Zammad.ApiToken)

	resp, err := z.ClientZammad.Do(req)
	if err != nil {
		return fmt.Errorf("unable to create ticket: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ticket creation failed (HTTP %d): %s", resp.StatusCode, string(data))
	}

	var ticketResp ZammadTicketResponse
	respBody, _ := io.ReadAll(resp.Body)
	json.Unmarshal(respBody, &ticketResp)

	log.Info().
		Str("call_id", call.CallUID).
		Str("from", call.CallFrom).
		Str("call_type", callType).
		Int("ticket_id", ticketResp.ID).
		Msg("Zammad ticket created")

	return nil
}

// ZammadPost makes a POST Request to Zammad with the given payload
func (z *ZammadBridge) ZammadPost(payload ZammadApiRequest) error {
	// Processing
	if payload.Direction == "Inbound" {
		payload.Direction = "in"
	}
	if payload.Direction == "Outbound" {
		payload.Direction = "out"
	}
	payload.CallIdDuplicate = payload.CallId

	// Actual request
	requestBody, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("unable to serialize JSON request body: %w", err)
	}

	log.Trace().Str("call_id", payload.CallId).Str("event", payload.Event).Str("from", payload.From).Str("to", payload.To).Msg("Zammad request (POST)")
	resp, err := z.ClientZammad.Post(z.Config.Zammad.Endpoint, "application/json", bytes.NewBuffer(requestBody))
	if err != nil {
		return fmt.Errorf("unable to make request: %w", err)
	}

	log.Trace().Str("call_id", payload.CallId).Str("event", payload.Event).Str("from", payload.From).Str("to", payload.To).Int("status", resp.StatusCode).Msg("Zammad response (POST)")

	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected response from Zammad (HTTP %d): %s", resp.StatusCode, string(data))
	}

	return nil
}
