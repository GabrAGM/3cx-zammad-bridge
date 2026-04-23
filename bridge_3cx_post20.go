package zammadbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/rs/zerolog/log"
)

type (
	WebsocketEventType int
	WebsocketResponse  struct {
		Sequence int `json:"sequence"`
		Event    struct {
			EventType    WebsocketEventType `json:"event_type"`
			Entity       string             `json:"entity"`
			AttachedData *json.RawMessage   `json:"attached_data"`
		} `json:"event"`
	}
)

const (
	WebsocketEventTypeUpsert     WebsocketEventType = 0
	WebsocketEventTypeDelete     WebsocketEventType = 1
	WebsocketEventTypeDTMFstring WebsocketEventType = 2
	WebsocketEventTypeResponse   WebsocketEventType = 4
)

type CallParticipant struct {
	ID int `json:"id"`

	// Status is the status of the call. Possible values include: "Dialing", "Ringing", "Connected"
	Status string `json:"status"`

	// DN is the extension number of the participant.
	DN string `json:"dn"`

	// PartyCallerName is the name of the caller or callee. Can be empty.
	PartyCallerName string `json:"party_caller_name"`

	// PartyDN is the extension number of the caller. E.g. 10007
	PartyDN string `json:"party_dn"`

	// PartyCallerID is the caller ID of the caller. E.g. 0123456789
	PartyCallerID string `json:"party_caller_id"`

	// PartyDID is the DID of the caller. Can be empty.
	PartyDID string `json:"party_did"`

	// CallID is the unique ID of the call.
	CallID int `json:"callid"`
}

type CallControlResponse []CallControlResponseEntry

type CallControlResponseEntry struct {
	DN           string            `json:"dn"`
	Participants []CallParticipant `json:"participants"`
}

type Client3CXPost20 struct {
	Config *Config

	client http.Client

	// accessToken is a Bearer-token retrieved after a valid Authentication call. It will expire automatically.
	accessToken string
}

func (z *Client3CXPost20) FetchCalls() ([]CallInformation, error) {
	// Use XAPI ActiveCalls endpoint which shows ALL system calls (requires admin auth)
	req, err := http.NewRequest(http.MethodGet, z.Config.Phone3CX.Host+"/xapi/v1/ActiveCalls", nil)
	if err != nil {
		return nil, fmt.Errorf("unable to prepare HTTP request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+z.accessToken)

	resp, err := z.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("unable to request active calls: %w", err)
	}

	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		log.Debug().
			Str("response", string(data)).
			Interface("headers", resp.Header).
			Msg("Received active calls response")
		return nil, fmt.Errorf("unexpected response fetching active calls (HTTP %d): %s", resp.StatusCode, string(data))
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("unable to read response body: %w", err)
	}

	var activeCallsResponse struct {
		Value []CallInformation `json:"value"`
	}
	err = json.Unmarshal(respBody, &activeCallsResponse)
	if err != nil {
		return nil, fmt.Errorf("unable to parse response JSON: %w", err)
	}

	// Parse Caller/Callee strings to extract numbers and names
	// Format: "extension Name (phone)" or "10000 Sip Trunk WE (phone)"
	for i := range activeCallsResponse.Value {
		call := &activeCallsResponse.Value[i]
		call.CallerName, call.CallerNumber = parseCallParty(call.Caller)
		call.CalleeName, call.CalleeNumber = parseCallParty(call.Callee)
	}

	log.Trace().
		Interface("response", activeCallsResponse.Value).
		Msg("Received active calls response")

	return activeCallsResponse.Value, nil
}

// parseCallParty extracts name and phone number from "ext Name (phone)" format
func parseCallParty(party string) (string, string) {
	if party == "" {
		return "", ""
	}
	// Look for pattern: "something (number)"
	openParen := strings.LastIndex(party, "(")
	closeParen := strings.LastIndex(party, ")")
	if openParen > 0 && closeParen > openParen {
		number := party[openParen+1 : closeParen]
		name := strings.TrimSpace(party[:openParen])
		// Remove leading extension number from name (e.g., "134 Maya Mohamed" -> "Maya Mohamed")
		parts := strings.SplitN(name, " ", 2)
		if len(parts) == 2 {
			// Check if first part is a number (extension)
			if _, err := strconv.Atoi(parts[0]); err == nil {
				name = parts[1]
			}
		}
		return name, number
	}
	// No parentheses - might be just "ext Name"
	parts := strings.SplitN(party, " ", 2)
	if len(parts) == 2 {
		return parts[1], parts[0]
	}
	return party, party
}

// convertParticipant and aggregateCallResponse removed - now using XAPI ActiveCalls

// listenWS makes a Websocket connection to 3CX to "immediately" get updates on calls. This is a blocking function.
//
// Currently not used because the "immediate" updates aren't yet compatible with the current implementation.
//
// nolint:unused
func (z *Client3CXPost20) listenWS() {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt)
	defer signal.Stop(sigs)
	defer close(sigs)

	// Start a WS connection
	ctx := context.Background()

	c, _, err := websocket.Dial(ctx, z.Config.Phone3CX.Host+"/callcontrol/ws", &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Authorization": []string{"Bearer " + z.accessToken},
		},
	})
	if err != nil {
		log.Error().Err(err).Msg("Unable to connect to 3CX WS")
		return
	}

	defer c.Close(websocket.StatusNormalClosure, "")

	log.Debug().Msg("Connected to 3CX WS")

	go func() {
		for {
			select {
			case <-sigs:
				log.Debug().Msg("Received interrupt signal")
				_ = c.Close(websocket.StatusNormalClosure, "")
				os.Exit(0)
				return
			case <-ctx.Done():
				log.Debug().Msg("Context done")
				return
			}
		}
	}()

	// Make the initial request
	payload, err := json.Marshal(map[string]interface{}{
		"RequestID":   "123",
		"Path":        "/callcontrol",
		"RequestData": "",
	})
	if err != nil {
		log.Error().Err(err).Msg("Error marshalling initial request")
		return
	}

	err = c.Write(ctx, websocket.MessageText, payload)
	if err != nil {
		log.Error().Err(err).Msg("Error writing to 3CX WS")
		return
	}

	for {
		log.Trace().Msg("Waiting for data from 3CX WS...")
		_, data, err := c.Read(ctx)
		if err != nil {
			if errors.Is(err, io.EOF) {
				// Somehow the connection was closed
				log.Debug().Msg("WS Connection closed - received EOF")
				return
			}

			log.Error().Err(err).Msg("Error reading from 3CX WS")
			return
		}

		err = z.processWSMessage(data)
		if err != nil {
			log.Error().Err(err).Msg("Error processing WS message")
			continue
		}

		// TODO: Remove this
		//_ = z.fetchExtensions()
	}
}

//nolint:unused
func (z *Client3CXPost20) processWSMessage(msg []byte) error {
	var response WebsocketResponse
	err := json.Unmarshal(msg, &response)
	if err != nil {
		return fmt.Errorf("unable to parse WS message: %w", err)
	}

	if response.Event.EventType == WebsocketEventTypeDelete {
		log.Debug().
			Int("sequence", response.Sequence).
			Int("event_type", int(response.Event.EventType)).
			Msg("Received delete event from 3CX WS")
		// TODO: Delete from map
		return nil
	}

	// Fetch the entity data which includes current Status
	entityData, err := httpGET3CX[CallParticipant](z, z.Config.Phone3CX.Host+response.Event.Entity)
	if err != nil {
		return fmt.Errorf("unable to fetch entity data: %w", err)
	}

	log.Debug().
		Interface("entity", entityData).
		Int("sequence", response.Sequence).
		Int("event_type", int(response.Event.EventType)).
		Msg("Received from 3CX WS")

	// TODO: Store call in local map?

	return nil
}

//nolint:unused
func httpGET3CX[T any](z *Client3CXPost20, url string) (*T, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("unable to prepare HTTP request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+z.accessToken)

	resp, err := z.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("unable to perform HTTP request: %w", err)
	}

	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected response fetching 3CX info (HTTP %d): %s", resp.StatusCode, string(data))
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("unable to read response body: %w", err)
	}

	o := new(T)
	err = json.Unmarshal(respBody, o)
	if err != nil {
		return nil, fmt.Errorf("unable to parse response JSON: %w", err)
	}

	return o, nil
}

func (z *Client3CXPost20) getLoginValues() (url.Values, *http.Cookie, error) {
	if z.Config.Phone3CX.ClientID != "" && z.Config.Phone3CX.ClientSecret != "" {
		log.Debug().
			Str("client_id", z.Config.Phone3CX.ClientID).
			Msgf("Authenticating to 3CX...")
		return url.Values{
			"grant_type":    {"client_credentials"},
			"client_id":     {z.Config.Phone3CX.ClientID},
			"client_secret": {z.Config.Phone3CX.ClientSecret},
		}, nil, nil
	}

	requestBody := struct {
		Username     string `json:"Username"`
		Password     string `json:"Password"`
		SecurityCode string `json:"SecurityCode"`
	}{
		z.Config.Phone3CX.User,
		z.Config.Phone3CX.Pass,
		"",
	}

	log.Debug().
		Str("host", z.Config.Phone3CX.Host).
		Str("user", z.Config.Phone3CX.User).
		Msgf("Authenticating to 3CX...")

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to serialize JSON request body: %w", err)
	}

	resp, err := z.client.Post(z.Config.Phone3CX.Host+"/webclient/api/Login/GetAccessToken", "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, nil, fmt.Errorf("unable to make login request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return nil, nil, fmt.Errorf("unexpected response authenticating 3CX (HTTP %d): %s", resp.StatusCode, string(data))
	}

	var loginResponse struct {
		Status string `json:"Status"`
		Token  struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
		} `json:"Token"`
	}

	// Decode the response body into the loginResponse struct
	err = json.NewDecoder(resp.Body).Decode(&loginResponse)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to decode login response: %w", err)
	}

	if loginResponse.Status != "AuthSuccess" {
		return nil, nil, fmt.Errorf("login failed: %s", loginResponse.Status)
	}
	values := url.Values{}
	values.Add("grant_type", "refresh_token")
	values.Add("client_id", "Webclient")

	return values, &http.Cookie{
		Name:     "RefreshTokenCookie",
		Value:    loginResponse.Token.RefreshToken,
		Secure:   true,
		HttpOnly: true,
	}, nil
}

// Authenticate attempts to login to 3CX and stores a token for future API calls. It then loads
// all extensions we are configured to monitor.
func (z *Client3CXPost20) Authenticate() error {
	values, cookie, err := z.getLoginValues()
	if err != nil {
		return fmt.Errorf("unable to prepare login request: %w", err)
	}

	encodedPayload := values.Encode()

	req, err := http.NewRequest(http.MethodPost, z.Config.Phone3CX.Host+"/connect/token?"+values.Encode(), bytes.NewReader([]byte(encodedPayload)))
	if err != nil {
		return fmt.Errorf("unable to prepare HTTP request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")

	if cookie != nil {
		req.AddCookie(cookie)
	}

	resp2, err := z.client.Do(req)
	if err != nil {
		return fmt.Errorf("unable to perform HTTP request: %w", err)
	}

	var tokenResponse struct {
		AccessToken string `json:"access_token"`
	}

	respBody, err := io.ReadAll(resp2.Body)
	if err != nil {
		return fmt.Errorf("unable to read response body: %w", err)
	}

	if resp2.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("HTTP 401: using wrong client_id or client_secret?")
	}

	log.Trace().
		Int("http_status", resp2.StatusCode).
		Int("response_length", len(respBody)).
		Msg("Received response from 3CX")

	// Decode the response body into the tokenResponse struct
	err = json.Unmarshal(respBody, &tokenResponse)
	if err != nil {
		return fmt.Errorf("unable to unmarshal access token: %w", err)
	}

	z.accessToken = tokenResponse.AccessToken

	log.Debug().Msg("Successfully authenticated to 3CX")

	return nil
}

// AuthenticateRetry retries logging in a while (defined in maxOffline).
// It waits five seconds for every failed attempt.
func (z *Client3CXPost20) AuthenticateRetry(maxOffline time.Duration) error {
	var downSince time.Time

	for err := z.Authenticate(); err != nil; err = z.Authenticate() {
		// If we received a HTTP 404 error, the server is probably not online and might be a pre-v20 version.
		// Retrying will not help in such scenarios.
		if strings.Contains(err.Error(), "404") {
			return err
		}

		// Write down the start time
		if downSince.IsZero() {
			downSince = time.Now()
		}

		if time.Since(downSince) > maxOffline {
			return fmt.Errorf("unable to authenticate to 3CX: %w", err)
		}

		log.Warn().
			Err(err).
			Msg("Unable to authenticate to 3CX - retrying in 5 seconds...")
		time.Sleep(time.Second * 5)
	}

	return nil
}

// FetchExtensions returns the full extension directory from 3CX v20's XAPI.
// It covers users, queues and ring groups — anything that can be the target
// of a call. Names are "First Last" when available, with a fallback to the
// raw Number.
func (z *Client3CXPost20) FetchExtensions() ([]Extension, error) {
	seen := map[string]bool{}
	out, err := z.fetchUsers(seen)
	if err != nil {
		return nil, err
	}

	// Queues (e.g. LiveOps Cairo Queue, 908) and ring groups are separate
	// endpoints in v20 XAPI. Best-effort — a failure here shouldn't block
	// showing the Users list.
	if queues, qerr := z.fetchQueues(seen); qerr == nil {
		out = append(out, queues...)
	} else {
		log.Warn().Err(qerr).Msg("Could not fetch 3CX queues for admin picker")
	}
	if groups, gerr := z.fetchRingGroups(seen); gerr == nil {
		out = append(out, groups...)
	} else {
		log.Warn().Err(gerr).Msg("Could not fetch 3CX ring groups for admin picker")
	}

	return out, nil
}

// fetchUsers pages through /xapi/v1/Users.
func (z *Client3CXPost20) fetchUsers(seen map[string]bool) ([]Extension, error) {
	// 3CX v20 XAPI caps $top at 100 per page, so we paginate with $skip until
	// a page returns fewer than pageSize items. Bounded at 20 pages (2,000
	// extensions) to protect against misconfigured dial-plans.
	var out []Extension
	const pageSize = 100
	for page := 0; page < 20; page++ {
		url := fmt.Sprintf("%s/xapi/v1/Users?$select=Number,FirstName,LastName&$top=%d&$skip=%d",
			z.Config.Phone3CX.Host, pageSize, page*pageSize)
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("unable to prepare HTTP request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+z.accessToken)

		resp, err := z.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("unable to fetch extensions (page %d): %w", page, err)
		}

		if resp.StatusCode >= 300 {
			data, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("unexpected response fetching extensions (HTTP %d): %s", resp.StatusCode, string(data))
		}

		var payload struct {
			Value []struct {
				Number    string `json:"Number"`
				FirstName string `json:"FirstName"`
				LastName  string `json:"LastName"`
			} `json:"value"`
		}
		err = json.NewDecoder(resp.Body).Decode(&payload)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("unable to parse extensions JSON: %w", err)
		}

		for _, u := range payload.Value {
			if u.Number == "" || seen[u.Number] {
				continue
			}
			name := strings.TrimSpace(u.FirstName + " " + u.LastName)
			if name == "" {
				name = u.Number
			}
			seen[u.Number] = true
			out = append(out, Extension{Number: u.Number, Name: name})
		}

		if len(payload.Value) < pageSize {
			break
		}
	}
	return out, nil
}

// fetchQueues pulls /xapi/v1/Queues (call queues — aka hunt groups). Names
// come back in the Name field as a plain string.
func (z *Client3CXPost20) fetchQueues(seen map[string]bool) ([]Extension, error) {
	return z.fetchNamedGroup("/xapi/v1/Queues", "queue", seen)
}

// fetchRingGroups pulls /xapi/v1/RingGroups.
func (z *Client3CXPost20) fetchRingGroups(seen map[string]bool) ([]Extension, error) {
	return z.fetchNamedGroup("/xapi/v1/RingGroups", "ring group", seen)
}

// fetchNamedGroup is the shared pagination loop for 3CX collections that
// expose a bare Name + Number (queues, ring groups).
func (z *Client3CXPost20) fetchNamedGroup(pathSuffix, label string, seen map[string]bool) ([]Extension, error) {
	var out []Extension
	const pageSize = 100
	for page := 0; page < 5; page++ {
		url := fmt.Sprintf("%s%s?$select=Number,Name&$top=%d&$skip=%d",
			z.Config.Phone3CX.Host, pathSuffix, pageSize, page*pageSize)
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+z.accessToken)

		resp, err := z.client.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode >= 300 {
			data, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("%s fetch HTTP %d: %s", label, resp.StatusCode, string(data))
		}

		var payload struct {
			Value []struct {
				Number string `json:"Number"`
				Name   string `json:"Name"`
			} `json:"value"`
		}
		err = json.NewDecoder(resp.Body).Decode(&payload)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}

		for _, q := range payload.Value {
			if q.Number == "" || seen[q.Number] {
				continue
			}
			name := strings.TrimSpace(q.Name)
			if name == "" {
				name = q.Number
			}
			seen[q.Number] = true
			out = append(out, Extension{Number: q.Number, Name: name + " (" + label + ")"})
		}
		if len(payload.Value) < pageSize {
			break
		}
	}
	return out, nil
}

func (z *Client3CXPost20) IsExtension(_ string) bool {
	// In v20 and above, we are only shown the extensions we are monitoring.
	// Therefore, we can assume that if we are monitoring an extension, it is valid.
	return true
}
