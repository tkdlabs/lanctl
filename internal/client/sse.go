package client

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ErrVPNRepairFailed is returned by VPNRepair when the server stream reported a
// failure marker ([FAIL] or [SSH error]).
var ErrVPNRepairFailed = errors.New("vpn repair reported a failure")

// StreamLogs follows a service's journal, writing each line to w. It returns
// when the server closes the stream or ctx is cancelled.
func (c *Client) StreamLogs(ctx context.Context, host, vm, service string, w io.Writer) error {
	path := hostBase(host, vm) + "/services/" + esc(service) + "/logs/stream"
	return c.streamSSE(ctx, http.MethodGet, path, func(line string) error {
		_, err := fmt.Fprintln(w, line)
		return err
	})
}

// VPNRepair runs a NordVPN repair, writing each output line to w. It returns
// ErrVPNRepairFailed if any failure marker was seen on the stream.
func (c *Client) VPNRepair(ctx context.Context, host, vm string, w io.Writer) error {
	path := hostBase(host, vm) + "/vpn-repair"

	failed := false
	err := c.streamSSE(ctx, http.MethodPost, path, func(line string) error {
		if strings.HasPrefix(line, "[FAIL]") || strings.HasPrefix(line, "[SSH error]") {
			failed = true
		}
		_, werr := fmt.Fprintln(w, line)
		return werr
	})
	if err != nil {
		return err
	}
	if failed {
		return ErrVPNRepairFailed
	}
	return nil
}

// streamSSE issues a bodyless request and invokes onEvent for each SSE event.
// Streaming ignores the Client's own timeout so long-lived streams are not
// cut off; lifetime is controlled by ctx instead.
func (c *Client) streamSSE(ctx context.Context, method, path string, onEvent func(string) error) error {
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")

	streaming := *c.HTTP
	streaming.Timeout = 0

	resp, err := streaming.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return apiError(resp.StatusCode, body)
	}
	return scanSSE(resp.Body, onEvent)
}

// scanSSE parses an SSE stream, calling onEvent once per event with the
// newline-joined data fields. Comments such as the 15s heartbeat (`: ...`) and
// blank lines are ignored.
func scanSSE(r io.Reader, onEvent func(string) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)

	var data []string
	flush := func() error {
		if len(data) == 0 {
			return nil
		}
		line := strings.Join(data, "\n")
		data = data[:0]
		return onEvent(line)
	}

	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if err := flush(); err != nil {
				return err
			}
		case strings.HasPrefix(line, ":"):
			// Comment / heartbeat.
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		default:
			// Ignore other SSE fields (event:, id:, retry:).
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return flush()
}
