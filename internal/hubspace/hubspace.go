// Package hubspace implements the small, unofficial subset of the Hubspace
// (Afero) cloud API needed to read and switch power outlets.
//
// The API is not documented. Authentication is a Keycloak OIDC login using the
// Android client; device state is read and written through Afero's metadata
// API. The package is deliberately isolated from lanctl config and HTTP types
// so it can be tested against a fake server and extracted later if needed.
package hubspace

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultAuthBaseURL  = "https://accounts.hubspaceconnect.com/auth/realms/thd"
	defaultAPIBaseURL   = "https://api2.afero.net/v1"
	defaultMetadataHost = "semantics2.afero.net"
	defaultUserAgent    = "Dart/2.15 (dart:io)"

	oauthClientID = "hubspace_android"
	oauthRedirect = "hubspace-app://loginredirect"
	oauthScope    = "openid offline_access"

	defaultRequestTimeout = 20 * time.Second
	defaultTokenLifetime  = 120 * time.Second
	turnOnTimeout         = 30 * time.Second
	maxResponseBytes      = 4 << 20
	maxLoginPageBytes     = 1 << 20
)

// Client talks to one Hubspace account. It is safe for concurrent use, though
// requests are serialized because the upstream API dislikes bursts.
type Client struct {
	Username string
	Password string
	HTTP     *http.Client

	authBaseURL  string
	apiBaseURL   string
	metadataHost string
	userAgent    string

	tokenMu     sync.Mutex
	token       string
	tokenExpiry time.Time

	apiMu sync.Mutex
}

// Device is a Hubspace metadevice with a power function.
type Device struct {
	ID            string
	Name          string
	Class         string
	AccountID     string
	PowerInstance string
	HasPower      bool
}

// New returns a client for username/password. No network call is made until a
// method is called.
func New(username, password string) *Client {
	jar, _ := cookiejar.New(nil)
	return &Client{
		Username:     username,
		Password:     password,
		authBaseURL:  defaultAuthBaseURL,
		apiBaseURL:   defaultAPIBaseURL,
		metadataHost: defaultMetadataHost,
		userAgent:    defaultUserAgent,
		HTTP: &http.Client{
			Timeout: defaultRequestTimeout,
			Jar:     jar,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// APIError is a non-2xx response from the Hubspace API.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	body := strings.TrimSpace(e.Body)
	if body == "" {
		return fmt.Sprintf("Hubspace API HTTP %d", e.Status)
	}
	return fmt.Sprintf("Hubspace API HTTP %d: %s", e.Status, body)
}

// Devices lists simple Hubspace devices across all accounts, including devices
// without a power function. Combo devices are skipped in phase 1.
func (c *Client) Devices(ctx context.Context) ([]Device, error) {
	var me struct {
		AccountAccess []struct {
			Account struct {
				AccountID string `json:"accountId"`
			} `json:"account"`
		} `json:"accountAccess"`
	}
	if err := c.doAPI(ctx, http.MethodGet, "/users/me", "", nil, &me); err != nil {
		return nil, fmt.Errorf("list Hubspace accounts: %w", err)
	}

	var out []Device
	seen := map[string]bool{}
	for _, access := range me.AccountAccess {
		accountID := access.Account.AccountID
		if accountID == "" {
			continue
		}
		var raw []rawDevice
		path := "/accounts/" + url.PathEscape(accountID) + "/metadevices?expansions=state"
		if err := c.doAPI(ctx, http.MethodGet, path, c.metadataHost, nil, &raw); err != nil {
			return nil, fmt.Errorf("list Hubspace devices: %w", err)
		}
		for _, rd := range raw {
			if rd.TypeID != "metadevice.device" || len(rd.Children) > 0 || rd.ID == "" || seen[rd.ID] {
				continue
			}
			dev := Device{
				ID:        rd.ID,
				Name:      rd.FriendlyName,
				Class:     rd.Description.Device.DeviceClass,
				AccountID: accountID,
			}
			for _, fn := range rd.Description.Functions {
				if fn.FunctionClass == "power" && fn.Type == "category" {
					dev.HasPower = true
					dev.PowerInstance = fn.FunctionInstance
					break
				}
			}
			seen[dev.ID] = true
			out = append(out, dev)
		}
	}
	return out, nil
}

// PowerOutlets returns only controllable power outlets.
func (c *Client) PowerOutlets(ctx context.Context) ([]Device, error) {
	devices, err := c.Devices(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Device, 0, len(devices))
	for _, d := range devices {
		if d.HasPower && strings.EqualFold(d.Class, "power-outlet") {
			out = append(out, d)
		}
	}
	return out, nil
}

// FindPowerOutlet resolves a configured device ID or name to a controllable
// outlet. The ID wins when both are set; names are matched case-insensitively.
func (c *Client) FindPowerOutlet(ctx context.Context, deviceID, deviceName string) (Device, error) {
	outlets, err := c.PowerOutlets(ctx)
	if err != nil {
		return Device{}, err
	}
	return ResolveDevice(outlets, deviceID, deviceName)
}

// ResolveDevice resolves id/name against a device list. It is exported for the
// API layer and tests.
func ResolveDevice(devices []Device, deviceID, deviceName string) (Device, error) {
	if id := strings.TrimSpace(deviceID); id != "" {
		for _, d := range devices {
			if d.ID == id {
				return d, nil
			}
		}
		return Device{}, fmt.Errorf("Hubspace device ID %q is not a controllable power outlet", id)
	}

	name := strings.TrimSpace(deviceName)
	if name == "" {
		return Device{}, errors.New("no Hubspace device_id or device_name configured")
	}

	var matches []Device
	for _, d := range devices {
		if strings.EqualFold(d.Name, name) {
			matches = append(matches, d)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		names := deviceNames(devices)
		if len(names) == 0 {
			return Device{}, fmt.Errorf("Hubspace device %q not found; no controllable power outlets are available", name)
		}
		return Device{}, fmt.Errorf("Hubspace device %q not found; available: %s", name, strings.Join(names, ", "))
	default:
		ids := make([]string, len(matches))
		for i, d := range matches {
			ids[i] = d.ID
		}
		return Device{}, fmt.Errorf("Hubspace device name %q is ambiguous; use device_id (%s)", name, strings.Join(ids, ", "))
	}
}

func deviceNames(devices []Device) []string {
	names := make([]string, 0, len(devices))
	for _, d := range devices {
		if d.Name != "" {
			names = append(names, d.Name)
		}
	}
	return names
}

// State reads the current on/off state of a device.
func (c *Client) State(ctx context.Context, d Device) (bool, error) {
	if d.ID == "" || d.AccountID == "" {
		return false, errors.New("invalid Hubspace device")
	}
	var resp stateResponse
	if err := c.doAPI(ctx, http.MethodGet, statePath(d), c.metadataHost, nil, &resp); err != nil {
		return false, fmt.Errorf("read plug state: %w", err)
	}
	value, ok := powerValue(resp.Values, d.PowerInstance)
	if !ok {
		return false, fmt.Errorf("Hubspace device %q has no power state", d.Name)
	}
	switch strings.ToLower(value) {
	case "on":
		return true, nil
	case "off":
		return false, nil
	default:
		return false, fmt.Errorf("unexpected Hubspace power state %q", value)
	}
}

// SetPower turns a device on or off and verifies the state echoed by Hubspace.
func (c *Client) SetPower(ctx context.Context, d Device, on bool) error {
	if d.ID == "" || d.AccountID == "" {
		return errors.New("invalid Hubspace device")
	}
	value := "off"
	if on {
		value = "on"
	}
	payload := setStateRequest{
		MetadeviceID: d.ID,
		Values: []setStateValue{{
			FunctionClass:    "power",
			FunctionInstance: d.PowerInstance,
			LastUpdateTime:   time.Now().UnixMilli(),
			Value:            value,
		}},
	}
	var resp stateResponse
	if err := c.doAPI(ctx, http.MethodPut, statePath(d), c.metadataHost, payload, &resp); err != nil {
		return fmt.Errorf("turn plug %s: %w", value, err)
	}
	got, ok := powerValue(resp.Values, d.PowerInstance)
	if !ok {
		return errors.New("Hubspace did not confirm the plug state")
	}
	if !strings.EqualFold(got, value) {
		return fmt.Errorf("Hubspace returned plug state %q, want %q", got, value)
	}
	return nil
}

// Cycle turns a device off, waits off, then turns it on. The turn-on uses a
// detached context so a canceled request cannot leave the plug off. If the
// off request fails ambiguously, a best-effort turn-on is made.
func (c *Client) Cycle(ctx context.Context, d Device, off time.Duration) error {
	if err := c.SetPower(ctx, d, false); err != nil {
		restoreCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), turnOnTimeout)
		defer cancel()
		if restoreErr := c.SetPower(restoreCtx, d, true); restoreErr != nil {
			return fmt.Errorf("power off failed: %v; plug may be off and power-on also failed: %w", err, restoreErr)
		}
		return fmt.Errorf("power off failed: %w", err)
	}

	if off > 0 {
		timer := time.NewTimer(off)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
		}
	}

	onCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), turnOnTimeout)
	defer cancel()
	if err := c.SetPower(onCtx, d, true); err != nil {
		return fmt.Errorf("plug may be off: power on failed: %w", err)
	}
	return nil
}

// ── Authentication ───────────────────────────────────────────────────────────

func (c *Client) ensureToken(ctx context.Context) error {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	if c.token != "" && time.Now().Before(c.tokenExpiry) {
		return nil
	}
	return c.loginLocked(ctx)
}

func (c *Client) clearToken() {
	c.tokenMu.Lock()
	c.token = ""
	c.tokenExpiry = time.Time{}
	c.tokenMu.Unlock()
}

func (c *Client) authHeader() string {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	return c.token
}

func (c *Client) loginLocked(ctx context.Context) error {
	verifier, challenge, err := generatePKCE()
	if err != nil {
		return fmt.Errorf("generate PKCE: %w", err)
	}

	authURL, err := url.Parse(c.authBaseURL + "/protocol/openid-connect/auth")
	if err != nil {
		return err
	}
	q := authURL.Query()
	q.Set("client_id", oauthClientID)
	q.Set("redirect_uri", oauthRedirect)
	q.Set("response_type", "code")
	q.Set("scope", oauthScope)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	authURL.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, authURL.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("Hubspace login page: %w", err)
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxLoginPageBytes))
	resp.Body.Close()
	if readErr != nil {
		return fmt.Errorf("read Hubspace login page: %w", readErr)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Hubspace login page: HTTP %d", resp.StatusCode)
	}

	action, err := parseFormAction(body)
	if err != nil {
		return err
	}
	actionURL, err := url.Parse(action)
	if err != nil {
		return fmt.Errorf("parse Hubspace login form action: %w", err)
	}
	if !actionURL.IsAbs() {
		actionURL = authURL.ResolveReference(actionURL)
	}
	if actionURL.Host != authURL.Host {
		return errors.New("Hubspace login form points at an unexpected host")
	}

	form := url.Values{
		"username":     {c.Username},
		"password":     {c.Password},
		"credentialId": {""},
	}
	req, err = http.NewRequestWithContext(ctx, http.MethodPost, actionURL.String(), strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	resp, err = c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("Hubspace login: %w", err)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxLoginPageBytes))
	resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return errors.New("Hubspace login failed (check username/password; the login flow may have changed)")
	case http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
	default:
		return fmt.Errorf("Hubspace login: HTTP %d", resp.StatusCode)
	}

	location, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		return fmt.Errorf("parse Hubspace login redirect: %w", err)
	}
	code := location.Query().Get("code")
	if code == "" {
		return errors.New("Hubspace login redirect did not contain a code")
	}

	form = url.Values{
		"client_id":     {oauthClientID},
		"grant_type":    {"authorization_code"},
		"redirect_uri":  {oauthRedirect},
		"code":          {code},
		"code_verifier": {verifier},
	}
	req, err = http.NewRequestWithContext(ctx, http.MethodPost, c.authBaseURL+"/protocol/openid-connect/token", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")
	resp, err = c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("Hubspace token exchange: %w", err)
	}
	body, readErr = io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	resp.Body.Close()
	if readErr != nil {
		return fmt.Errorf("read Hubspace token response: %w", readErr)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Hubspace token exchange: HTTP %d", resp.StatusCode)
	}
	var tokenResp struct {
		AccessToken string  `json:"access_token"`
		TokenType   string  `json:"token_type"`
		ExpiresIn   flexInt `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return fmt.Errorf("decode Hubspace token response: %w", err)
	}
	if tokenResp.AccessToken == "" {
		return errors.New("Hubspace token response did not contain an access token")
	}

	expires := time.Duration(tokenResp.ExpiresIn) * time.Second
	if expires <= 0 {
		expires = defaultTokenLifetime
	}
	refresh := expires / 2
	if expires > 60*time.Second {
		refresh = expires - 15*time.Second
	}
	if refresh < 5*time.Second {
		refresh = 5 * time.Second
	}

	tokenType := strings.TrimSpace(tokenResp.TokenType)
	if tokenType == "" {
		tokenType = "Bearer"
	}
	c.token = tokenType + " " + tokenResp.AccessToken
	c.tokenExpiry = time.Now().Add(refresh)
	return nil
}

var formActionRe = regexp.MustCompile(`(?is)<form\b[^>]*\baction\s*=\s*(?:"([^"]*)"|'([^']*)')`)

// parseFormAction extracts and HTML-unescapes the first form action.
func parseFormAction(body []byte) (string, error) {
	m := formActionRe.FindSubmatch(body)
	if m == nil {
		return "", errors.New("Hubspace login page has no form action")
	}
	action := string(m[1])
	if action == "" {
		action = string(m[2])
	}
	action = html.UnescapeString(action)
	if strings.TrimSpace(action) == "" {
		return "", errors.New("Hubspace login form action is empty")
	}
	return action, nil
}

func generatePKCE() (verifier, challenge string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(buf)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

// flexInt accepts a JSON number or a quoted number.
type flexInt int

func (f *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	s = strings.Trim(s, `"`)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return fmt.Errorf("invalid number %q", s)
	}
	*f = flexInt(n)
	return nil
}

// ── API plumbing ─────────────────────────────────────────────────────────────

func (c *Client) doAPI(ctx context.Context, method, path, host string, body, out any) error {
	c.apiMu.Lock()
	defer c.apiMu.Unlock()

	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}

	for attempt := 0; attempt < 2; attempt++ {
		if err := c.ensureToken(ctx); err != nil {
			return err
		}

		req, err := http.NewRequestWithContext(ctx, method, c.apiBaseURL+path, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", c.userAgent)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Authorization", c.authHeader())
		if payload != nil {
			req.Header.Set("Content-Type", "application/json; charset=utf-8")
		}
		if host != "" {
			req.Host = host
		}

		resp, err := c.HTTP.Do(req)
		if err != nil {
			return err
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
		resp.Body.Close()
		if readErr != nil {
			return readErr
		}

		if resp.StatusCode == http.StatusUnauthorized {
			if attempt == 0 {
				c.clearToken()
				continue
			}
			return errors.New("Hubspace authentication failed after retry")
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return &APIError{Status: resp.StatusCode, Body: truncate(string(data), 200)}
		}
		if out == nil {
			return nil
		}
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("decode Hubspace response: %w", err)
		}
		return nil
	}
	return errors.New("Hubspace authentication failed after retry")
}

func statePath(d Device) string {
	return "/accounts/" + url.PathEscape(d.AccountID) + "/metadevices/" + url.PathEscape(d.ID) + "/state"
}

type rawDevice struct {
	ID           string `json:"id"`
	TypeID       string `json:"typeId"`
	FriendlyName string `json:"friendlyName"`
	Description  struct {
		Device struct {
			DeviceClass string `json:"deviceClass"`
		} `json:"device"`
		Functions []struct {
			FunctionClass    string `json:"functionClass"`
			Type             string `json:"type"`
			FunctionInstance string `json:"functionInstance"`
		} `json:"functions"`
	} `json:"description"`
	Children []json.RawMessage `json:"children"`
}

type stateResponse struct {
	Values []stateValue `json:"values"`
}

type stateValue struct {
	FunctionClass    string          `json:"functionClass"`
	FunctionInstance string          `json:"functionInstance"`
	Value            json.RawMessage `json:"value"`
}

type setStateRequest struct {
	MetadeviceID string          `json:"metadeviceId"`
	Values       []setStateValue `json:"values"`
}

type setStateValue struct {
	FunctionClass    string `json:"functionClass"`
	FunctionInstance string `json:"functionInstance,omitempty"`
	LastUpdateTime   int64  `json:"lastUpdateTime"`
	Value            string `json:"value"`
}

func powerValue(values []stateValue, instance string) (string, bool) {
	var fallback string
	haveFallback := false
	for _, v := range values {
		if v.FunctionClass != "power" {
			continue
		}
		if instance != "" {
			if v.FunctionInstance != instance {
				continue
			}
			return rawString(v.Value), true
		}
		if v.FunctionInstance != "" {
			if !haveFallback {
				fallback = rawString(v.Value)
				haveFallback = true
			}
			continue
		}
		return rawString(v.Value), true
	}
	if haveFallback {
		return fallback, true
	}
	return "", false
}

func rawString(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
