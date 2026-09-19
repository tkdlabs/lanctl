// Package client is a small, typed HTTP client for the lanctl REST API.
//
// It owns no configuration and performs no SSH or local operations: every
// privileged action happens on the server. The client is transport-only, so it
// is safe to use from scripts and easy to test against httptest.Server.
package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is used when no server address is provided.
const DefaultBaseURL = "http://localhost:8003"

// DefaultTimeout bounds non-streaming requests.
const DefaultTimeout = 15 * time.Second

// Client talks to a lanctl server.
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

// Option customizes a Client.
type Option func(*Client)

// WithHTTPClient replaces the underlying HTTP client.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.HTTP = h }
}

// WithTimeout sets the timeout for non-streaming requests.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) { c.HTTP.Timeout = d }
}

// New returns a Client for baseURL. An empty baseURL uses DefaultBaseURL, and a
// baseURL without a scheme (for example "rpi.local:8004") defaults to http.
// Surrounding whitespace and trailing slashes are trimmed.
func New(baseURL string, opts ...Option) *Client {
	c := &Client{
		BaseURL: normalizeBaseURL(baseURL),
		HTTP:    &http.Client{Timeout: DefaultTimeout},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// normalizeBaseURL turns user input such as "host:8004" or "http://host:8004/"
// into a clean "scheme://host[:port]" prefix.
func normalizeBaseURL(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return DefaultBaseURL
	}
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	return strings.TrimRight(s, "/")
}

// APIError is returned for non-2xx responses. Detail carries the server's
// human-readable message (the API's {"detail": ...} error shape).
type APIError struct {
	Status int
	Detail string
}

func (e *APIError) Error() string {
	if e.Detail != "" {
		return e.Detail
	}
	return fmt.Sprintf("HTTP %d", e.Status)
}

// ── API types ────────────────────────────────────────────────────────────────

// VM is a virtual machine on a Proxmox host.
type VM struct {
	Name            string            `json:"name"`
	VMID            int               `json:"vmid,omitempty"`
	IP              string            `json:"ip"`
	Online          bool              `json:"online"`
	Services        []string          `json:"services"`
	UserServices    []string          `json:"user_services"`
	ServiceStatuses map[string]string `json:"service_statuses"`
	VPNHostname     *string           `json:"vpn_hostname"`
	VPNReachable    *bool             `json:"vpn_reachable"`
}

// Host is a managed host. VMs is only populated for Proxmox hosts.
type Host struct {
	Name            string            `json:"name"`
	Type            string            `json:"type"`
	IP              string            `json:"ip"`
	MAC             string            `json:"mac"`
	Online          bool              `json:"online"`
	Local           bool              `json:"local"`
	Services        []string          `json:"services"`
	UserServices    []string          `json:"user_services"`
	ServiceStatuses map[string]string `json:"service_statuses"`
	VPNHostname     *string           `json:"vpn_hostname"`
	VPNReachable    *bool             `json:"vpn_reachable"`
	VMs             *[]VM             `json:"vms,omitempty"`
}

// ── Core methods ─────────────────────────────────────────────────────────────

// Version returns the server build version.
func (c *Client) Version(ctx context.Context) (string, error) {
	var out struct {
		Version string `json:"version"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/version", nil, &out); err != nil {
		return "", err
	}
	return out.Version, nil
}

// Hosts returns all hosts with their current status.
func (c *Client) Hosts(ctx context.Context) ([]Host, error) {
	var out []Host
	if err := c.do(ctx, http.MethodGet, "/api/hosts", nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []Host{}
	}
	return out, nil
}

// Wake sends a Wake-on-LAN magic packet to host and returns the server status
// message.
func (c *Client) Wake(ctx context.Context, host string) (string, error) {
	var out struct {
		Status string `json:"status"`
	}
	err := c.do(ctx, http.MethodPost, hostBase(host, "")+"/wake", nil, &out)
	return out.Status, err
}

// Shutdown shuts down a host, or a VM on that host when vm is non-empty.
func (c *Client) Shutdown(ctx context.Context, host, vm string) error {
	return c.do(ctx, http.MethodPost, hostBase(host, vm)+"/shutdown", nil, nil)
}

// ServiceControl runs start, stop, or restart for a service on a host or VM.
func (c *Client) ServiceControl(ctx context.Context, host, vm, service, action string) error {
	path := hostBase(host, vm) + "/services/" + esc(service) + "/" + esc(action)
	return c.do(ctx, http.MethodPost, path, nil, nil)
}

// Logs returns up to lines journal entries for a service. A non-positive lines
// leaves the choice to the server (its default is 100).
func (c *Client) Logs(ctx context.Context, host, vm, service string, lines int) ([]string, error) {
	q := url.Values{}
	if lines > 0 {
		q.Set("lines", strconv.Itoa(lines))
	}
	path := hostBase(host, vm) + "/services/" + esc(service) + "/logs"

	var out struct {
		Lines []string `json:"lines"`
	}
	if err := c.do(ctx, http.MethodGet, path, q, &out); err != nil {
		return nil, err
	}
	if out.Lines == nil {
		out.Lines = []string{}
	}
	return out.Lines, nil
}

// ── Internals ────────────────────────────────────────────────────────────────

// hostBase builds the API path prefix for a host, or a VM when vm is set.
func hostBase(host, vm string) string {
	if vm == "" {
		return "/api/hosts/" + esc(host)
	}
	return "/api/hosts/" + esc(host) + "/vms/" + esc(vm)
}

// esc escapes a path segment. The server matches names exactly, so this is
// required for correctness with names containing reserved characters.
func esc(s string) string { return url.PathEscape(s) }

// do performs a request with no body and decodes a JSON response into out.
// A nil out discards the response body.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, out any) error {
	u := c.BaseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return apiError(resp.StatusCode, body)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}
	return nil
}

// apiError converts a non-2xx response body into an *APIError.
func apiError(status int, body []byte) error {
	var e struct {
		Detail string `json:"detail"`
	}
	_ = json.Unmarshal(body, &e)
	return &APIError{Status: status, Detail: e.Detail}
}
