package zammadbridge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

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

	// Auto-create ticket if enabled
	if z.Config.Zammad.AutoCreateTicket && z.Config.Zammad.ApiUrl != "" && z.Config.Zammad.ApiToken != "" {
		ticketErr := z.ZammadCreateTicket(call, cause)
		if ticketErr != nil {
			log.Error().Err(ticketErr).Str("call_id", call.CallUID).Msg("Failed to create Zammad ticket")
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

// ZammadCreateTicket creates a ticket in Zammad for the completed call
func (z *ZammadBridge) ZammadCreateTicket(call *CallInformation, cause string) error {
	group := z.Config.Zammad.TicketGroup
	if group == "" {
		group = "Users"
	}

	callType := "Inbound"
	if call.Direction == "Outbound" || call.Direction == "out" {
		callType = "Outbound"
	}
	if cause == "cancel" || cause == "noAnswer" {
		callType = "Missed"
	}

	// Look up customer
	customerID, _ := z.ZammadLookupUser(call.CallFrom)

	var bodyParts []string
	bodyParts = append(bodyParts, fmt.Sprintf("Caller: %s", call.CallFrom))
	if call.AgentName != "" {
		bodyParts = append(bodyParts, fmt.Sprintf("Agent: %s (%s)", call.AgentName, call.AgentNumber))
	} else if call.AgentNumber != "" {
		bodyParts = append(bodyParts, fmt.Sprintf("Agent: %s", call.AgentNumber))
	}
	bodyParts = append(bodyParts, fmt.Sprintf("Call Type: %s", callType))
	bodyParts = append(bodyParts, fmt.Sprintf("Direction: %s", call.Direction))

	ticket := ZammadTicketRequest{
		Title: fmt.Sprintf("Phone Call from %s (%s)", call.CallFrom, callType),
		Group: group,
		Article: ZammadArticleCreate{
			Subject:  "Phone Call",
			Body:     strings.Join(bodyParts, "\n"),
			Type:     "phone",
			Internal: false,
		},
	}

	if customerID > 0 {
		ticket.CustomerID = customerID
	} else {
		ticket.Customer = call.CallFrom
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
