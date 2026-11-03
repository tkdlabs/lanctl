package hubspace

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testUser     = "user@example.com"
	testPassword = "secret"
	testToken    = "access-token"
)

// fakeHubspace serves the minimum Keycloak + Afero surface the client uses.
type fakeHubspace struct {
	t      *testing.T
	server *httptest.Server

	mu                 sync.Mutex
	state              string
	stateMode          string
	loginCount         int
	unauthorizedOnce   bool
	alwaysUnauthorized bool
	unconfirmed        bool
	failOff            bool
	failOn             bool
	setMismatch        bool
	authPageStatus     int
	authPageNoForm     bool
	authActionURL      string
	loginStatus        int
	loginNoCode        bool
	tokenStatus        int
	badTokenJSON       bool
	missingToken       bool
	failDevices        bool
	emptyDevices       bool
	badDevicesJSON     bool
	failState          bool
	setCalls           []string
	lastSet            setStateRequest

	offOnce sync.Once
	onOnce  sync.Once
	offCh   chan struct{}
	onCh    chan struct{}
}

func newFakeHubspace(t *testing.T) *fakeHubspace {
	t.Helper()
	f := &fakeHubspace{
		t:     t,
		state: "on",
		offCh: make(chan struct{}),
		onCh:  make(chan struct{}),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/realms/thd/protocol/openid-connect/auth", f.authPage)
	mux.HandleFunc("/auth/realms/thd/login-actions/authenticate", f.login)
	mux.HandleFunc("/auth/realms/thd/protocol/openid-connect/token", f.token)
	mux.HandleFunc("/v1/users/me", f.usersMe)
	mux.HandleFunc("/v1/accounts/acct/metadevices", f.metadevices)
	mux.HandleFunc("/v1/accounts/acct/metadevices/dev-plug/state", f.plugState)
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeHubspace) client(username, password string) *Client {
	c := New(username, password)
	c.authBaseURL = f.server.URL + "/auth/realms/thd"
	c.apiBaseURL = f.server.URL + "/v1"
	return c
}

func (f *fakeHubspace) authPage(w http.ResponseWriter, r *http.Request) {
	if f.authPageStatus != 0 {
		http.Error(w, "auth page failed", f.authPageStatus)
		return
	}
	if got := r.URL.Query().Get("client_id"); got != oauthClientID {
		f.t.Errorf("auth client_id = %q, want %q", got, oauthClientID)
	}
	if got := r.URL.Query().Get("code_challenge_method"); got != "S256" {
		f.t.Errorf("code_challenge_method = %q, want S256", got)
	}
	if r.URL.Query().Get("code_challenge") == "" {
		f.t.Error("auth request has no code_challenge")
	}
	http.SetCookie(w, &http.Cookie{Name: "AUTH", Value: "1", Path: "/"})
	w.Header().Set("Content-Type", "text/html")
	if f.authPageNoForm {
		fmt.Fprint(w, `<html><body>no login form</body></html>`)
		return
	}
	action := "/auth/realms/thd/login-actions/authenticate?session_code=abc&execution=def&client_id=hubspace_android&tab_id=ghi"
	if f.authActionURL != "" {
		action = f.authActionURL
	}
	fmt.Fprintf(w, `<html><body>
<form action="%s" method="post">
<input name="username"><input name="password"><input name="credentialId">
</form></body></html>`, action)
}

func (f *fakeHubspace) login(w http.ResponseWriter, r *http.Request) {
	if f.loginStatus != 0 {
		http.Error(w, "login failed", f.loginStatus)
		return
	}
	if err := r.ParseForm(); err != nil {
		f.t.Errorf("login ParseForm: %v", err)
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if r.Form.Get("session_code") != "abc" || r.Form.Get("execution") != "def" {
		f.t.Errorf("login form params missing: %v", r.Form)
	}
	if r.Form.Get("username") != testUser || r.Form.Get("password") != testPassword {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "invalid credentials")
		return
	}
	if _, err := r.Cookie("AUTH"); err != nil {
		f.t.Errorf("login request did not carry the auth cookie: %v", err)
		http.Error(w, "no cookie", http.StatusBadRequest)
		return
	}
	location := "hubspace-app://loginredirect?code=test-code"
	if f.loginNoCode {
		location = "hubspace-app://loginredirect"
	}
	w.Header().Set("Location", location)
	w.WriteHeader(http.StatusFound)
}

func (f *fakeHubspace) token(w http.ResponseWriter, r *http.Request) {
	if f.tokenStatus != 0 {
		http.Error(w, "token failed", f.tokenStatus)
		return
	}
	if err := r.ParseForm(); err != nil {
		f.t.Errorf("token ParseForm: %v", err)
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if r.Form.Get("code") != "test-code" || r.Form.Get("code_verifier") == "" {
		f.t.Errorf("token form params missing: %v", r.Form)
	}
	f.mu.Lock()
	f.loginCount++
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if f.badTokenJSON {
		fmt.Fprint(w, `{not json`)
		return
	}
	if f.missingToken {
		fmt.Fprint(w, `{"token_type":"Bearer","expires_in":120}`)
		return
	}
	fmt.Fprintf(w, `{"access_token":%q,"token_type":"Bearer","expires_in":120}`, testToken)
}

func (f *fakeHubspace) authorized(r *http.Request) bool {
	return r.Header.Get("Authorization") == "Bearer "+testToken
}

func (f *fakeHubspace) usersMe(w http.ResponseWriter, r *http.Request) {
	if !f.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	f.mu.Lock()
	if f.alwaysUnauthorized {
		f.mu.Unlock()
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	once := f.unauthorizedOnce
	f.unauthorizedOnce = false
	f.mu.Unlock()
	if once {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{"accountAccess":[{"account":{"accountId":"acct"}}]}`)
}

const devicesJSON = `[
  {"id":"dev-plug","typeId":"metadevice.device","friendlyName":"Plug One","description":{"device":{"deviceClass":"power-outlet"},"functions":[{"functionClass":"power","type":"category"}]},"children":[]},
  {"id":"dev-light","typeId":"metadevice.device","friendlyName":"Light One","description":{"device":{"deviceClass":"light"},"functions":[{"functionClass":"power","type":"category"}]},"children":[]},
  {"id":"dev-combo","typeId":"metadevice.device","friendlyName":"Combo One","description":{"device":{"deviceClass":"power-outlet"},"functions":[{"functionClass":"power","type":"category"}]},"children":[{"id":"child"}]}
]`

func (f *fakeHubspace) metadevices(w http.ResponseWriter, r *http.Request) {
	if !f.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if r.Host != defaultMetadataHost {
		f.t.Errorf("metadata Host = %q, want %q", r.Host, defaultMetadataHost)
	}
	if f.failDevices {
		http.Error(w, "devices failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if f.badDevicesJSON {
		fmt.Fprint(w, `{not json`)
		return
	}
	if f.emptyDevices {
		fmt.Fprint(w, `[]`)
		return
	}
	fmt.Fprint(w, devicesJSON)
}

func (f *fakeHubspace) plugState(w http.ResponseWriter, r *http.Request) {
	if !f.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	f.mu.Lock()
	switch r.Method {
	case http.MethodGet:
		state := f.state
		mode := f.stateMode
		fail := f.failState
		f.mu.Unlock()
		if fail {
			http.Error(w, "state failed", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch mode {
		case "no-power":
			fmt.Fprint(w, `{"values":[{"functionClass":"wifi-rssi","value":-30}]}`)
		case "weird":
			fmt.Fprint(w, `{"values":[{"functionClass":"power","value":"unknown"}]}`)
		default:
			fmt.Fprintf(w, `{"values":[{"functionClass":"power","value":%q},{"functionClass":"wifi-rssi","value":-30}]}`, state)
		}
		return
	case http.MethodPut:
		f.mu.Unlock()
	default:
		f.mu.Unlock()
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}

	var req setStateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		f.t.Errorf("decode setStateRequest: %v", err)
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if len(req.Values) != 1 {
		f.t.Errorf("set request values = %d, want 1", len(req.Values))
		http.Error(w, "bad values", http.StatusBadRequest)
		return
	}
	value := req.Values[0]
	if req.MetadeviceID != "dev-plug" || value.FunctionClass != "power" || value.LastUpdateTime <= 0 {
		f.t.Errorf("unexpected set request: %+v", req)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	f.mu.Lock()
	f.setCalls = append(f.setCalls, value.Value)
	f.lastSet = req
	if value.Value == "off" && f.failOff {
		f.mu.Unlock()
		http.Error(w, "off failed", http.StatusBadGateway)
		return
	}
	if value.Value == "on" && f.failOn {
		f.mu.Unlock()
		http.Error(w, "on failed", http.StatusBadGateway)
		return
	}
	if f.unconfirmed {
		f.state = value.Value
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"values":[{"functionClass":"wifi-rssi","value":-30}]}`)
		return
	}
	f.state = value.Value
	reported := value.Value
	if f.setMismatch {
		if reported == "on" {
			reported = "off"
		} else {
			reported = "on"
		}
	}
	f.mu.Unlock()

	if value.Value == "off" {
		f.offOnce.Do(func() { close(f.offCh) })
	}
	if value.Value == "on" {
		f.onOnce.Do(func() { close(f.onCh) })
	}

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"values":[{"functionClass":"power","value":%q}]}`, reported)
}

func TestParseFormAction(t *testing.T) {
	got, err := parseFormAction([]byte(`<form action="/login?session_code=a&amp;execution=b">`))
	if err != nil {
		t.Fatal(err)
	}
	if got != "/login?session_code=a&execution=b" {
		t.Errorf("action = %q", got)
	}

	got, err = parseFormAction([]byte(`<form action='/login?x=1'>`))
	if err != nil {
		t.Fatal(err)
	}
	if got != "/login?x=1" {
		t.Errorf("single-quoted action = %q", got)
	}

	if _, err := parseFormAction([]byte(`<html>no form</html>`)); err == nil {
		t.Error("expected an error for a page without a form")
	}
}

func TestGeneratePKCE(t *testing.T) {
	verifier, challenge, err := generatePKCE()
	if err != nil {
		t.Fatal(err)
	}
	if len(verifier) != 43 {
		t.Errorf("verifier length = %d, want 43", len(verifier))
	}
	if len(challenge) != 43 {
		t.Errorf("challenge length = %d, want 43", len(challenge))
	}
	verifier2, challenge2, err := generatePKCE()
	if err != nil {
		t.Fatal(err)
	}
	if verifier == verifier2 || challenge == challenge2 {
		t.Error("PKCE values are not random")
	}
}

func TestLoginDevicesAndPowerOutlets(t *testing.T) {
	f := newFakeHubspace(t)
	c := f.client(testUser, testPassword)

	outlets, err := c.PowerOutlets(context.Background())
	if err != nil {
		t.Fatalf("PowerOutlets: %v", err)
	}
	if len(outlets) != 1 {
		t.Fatalf("outlets = %d, want 1: %+v", len(outlets), outlets)
	}
	d := outlets[0]
	if d.ID != "dev-plug" || d.Name != "Plug One" || d.Class != "power-outlet" || !d.HasPower || d.AccountID != "acct" {
		t.Errorf("unexpected device: %+v", d)
	}
	if f.loginCount != 1 {
		t.Errorf("loginCount = %d, want 1", f.loginCount)
	}
}

func TestStateAndSetPower(t *testing.T) {
	f := newFakeHubspace(t)
	c := f.client(testUser, testPassword)
	d := Device{ID: "dev-plug", Name: "Plug One", Class: "power-outlet", AccountID: "acct", HasPower: true}

	on, err := c.State(context.Background(), d)
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if !on {
		t.Error("initial state = off, want on")
	}

	if err := c.SetPower(context.Background(), d, false); err != nil {
		t.Fatalf("SetPower(off): %v", err)
	}
	if len(f.setCalls) != 1 || f.setCalls[0] != "off" {
		t.Errorf("setCalls = %v, want [off]", f.setCalls)
	}
	if f.lastSet.MetadeviceID != "dev-plug" || f.lastSet.Values[0].LastUpdateTime <= 0 {
		t.Errorf("unexpected set payload: %+v", f.lastSet)
	}

	on, err = c.State(context.Background(), d)
	if err != nil {
		t.Fatalf("State after off: %v", err)
	}
	if on {
		t.Error("state after off = on, want off")
	}

	if err := c.SetPower(context.Background(), d, true); err != nil {
		t.Fatalf("SetPower(on): %v", err)
	}
	if len(f.setCalls) != 2 || f.setCalls[1] != "on" {
		t.Errorf("setCalls = %v, want [off on]", f.setCalls)
	}
}

func TestSetPowerUnconfirmed(t *testing.T) {
	f := newFakeHubspace(t)
	f.unconfirmed = true
	c := f.client(testUser, testPassword)
	d := Device{ID: "dev-plug", Name: "Plug One", AccountID: "acct", HasPower: true}

	err := c.SetPower(context.Background(), d, true)
	if err == nil {
		t.Fatal("expected unconfirmed-state error, got nil")
	}
	if !strings.Contains(err.Error(), "did not confirm") {
		t.Errorf("error %q should mention confirmation", err)
	}
}

func TestResolveDevice(t *testing.T) {
	devices := []Device{
		{ID: "id-a", Name: "Same Name"},
		{ID: "id-b", Name: "same name"},
		{ID: "id-c", Name: "Other"},
	}
	if d, err := ResolveDevice(devices, "id-c", ""); err != nil || d.ID != "id-c" {
		t.Errorf("ID lookup = %+v, %v", d, err)
	}
	if d, err := ResolveDevice(devices, "", "other"); err != nil || d.ID != "id-c" {
		t.Errorf("name lookup = %+v, %v", d, err)
	}
	if _, err := ResolveDevice(devices, "", "missing"); err == nil || !strings.Contains(err.Error(), "available") {
		t.Errorf("missing name error = %v", err)
	}
	if _, err := ResolveDevice(devices, "", "Same Name"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("duplicate name error = %v", err)
	}
	if _, err := ResolveDevice(devices, "missing", ""); err == nil || !strings.Contains(err.Error(), "not a controllable") {
		t.Errorf("missing ID error = %v", err)
	}
}

func TestPowerValue(t *testing.T) {
	values := []stateValue{
		{FunctionClass: "wifi-rssi", Value: json.RawMessage(`-30`)},
		{FunctionClass: "power", FunctionInstance: "secondary", Value: json.RawMessage(`"on"`)},
		{FunctionClass: "power", Value: json.RawMessage(`"off"`)},
	}
	if got, ok := powerValue(values, ""); !ok || got != "off" {
		t.Errorf("empty instance = %q/%v, want off/true", got, ok)
	}
	if got, ok := powerValue(values, "secondary"); !ok || got != "on" {
		t.Errorf("secondary instance = %q/%v, want on/true", got, ok)
	}
	if _, ok := powerValue(values, "missing"); ok {
		t.Error("missing instance should not match")
	}
}

func TestFindPowerOutlet(t *testing.T) {
	f := newFakeHubspace(t)
	c := f.client(testUser, testPassword)

	d, err := c.FindPowerOutlet(context.Background(), "", "Plug One")
	if err != nil {
		t.Fatalf("FindPowerOutlet by name: %v", err)
	}
	if d.ID != "dev-plug" {
		t.Errorf("device = %+v, want dev-plug", d)
	}
	if d, err := c.FindPowerOutlet(context.Background(), "dev-plug", ""); err != nil || d.ID != "dev-plug" {
		t.Errorf("FindPowerOutlet by ID = %+v, %v", d, err)
	}
	if _, err := c.FindPowerOutlet(context.Background(), "", "missing"); err == nil {
		t.Error("expected an error for a missing outlet")
	}
}

func TestStateAPIError(t *testing.T) {
	f := newFakeHubspace(t)
	f.failState = true
	c := f.client(testUser, testPassword)
	d := Device{ID: "dev-plug", Name: "Plug One", AccountID: "acct", HasPower: true}
	if _, err := c.State(context.Background(), d); err == nil || !strings.Contains(err.Error(), "read plug state") {
		t.Errorf("State error = %v", err)
	}
}

func TestDoAPINilOut(t *testing.T) {
	f := newFakeHubspace(t)
	c := f.client(testUser, testPassword)
	if err := c.doAPI(context.Background(), http.MethodGet, "/users/me", "", nil, nil); err != nil {
		t.Fatalf("doAPI with nil out: %v", err)
	}
}

func TestStateErrors(t *testing.T) {
	d := Device{ID: "dev-plug", Name: "Plug One", AccountID: "acct", HasPower: true}
	for _, tc := range []struct {
		mode string
		want string
	}{
		{"no-power", "no power state"},
		{"weird", "unexpected"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			f := newFakeHubspace(t)
			f.stateMode = tc.mode
			c := f.client(testUser, testPassword)
			_, err := c.State(context.Background(), d)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("State error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestAPIErrorFormatting(t *testing.T) {
	if got := (&APIError{Status: 500}).Error(); got != "Hubspace API HTTP 500" {
		t.Errorf("empty-body error = %q", got)
	}
	if got := (&APIError{Status: 502, Body: "bad gateway"}).Error(); got != "Hubspace API HTTP 502: bad gateway" {
		t.Errorf("body error = %q", got)
	}
	if got := truncate("abcdef", 3); got != "abc…" {
		t.Errorf("truncate = %q", got)
	}
	if got := truncate("abc", 10); got != "abc" {
		t.Errorf("short truncate = %q", got)
	}
	if got := rawString(json.RawMessage(`123`)); got != "" {
		t.Errorf("rawString(non-string) = %q, want empty", got)
	}
}

func TestInvalidDeviceOperations(t *testing.T) {
	c := New(testUser, testPassword)
	if _, err := c.State(context.Background(), Device{}); err == nil {
		t.Error("State with an empty device should fail")
	}
	if err := c.SetPower(context.Background(), Device{}, true); err == nil {
		t.Error("SetPower with an empty device should fail")
	}
}

func TestSetPowerMismatch(t *testing.T) {
	f := newFakeHubspace(t)
	f.setMismatch = true
	c := f.client(testUser, testPassword)
	d := Device{ID: "dev-plug", Name: "Plug One", AccountID: "acct", HasPower: true}
	err := c.SetPower(context.Background(), d, true)
	if err == nil || !strings.Contains(err.Error(), "returned plug state") {
		t.Errorf("SetPower mismatch error = %v", err)
	}
}

func TestDevicesErrors(t *testing.T) {
	t.Run("api error", func(t *testing.T) {
		f := newFakeHubspace(t)
		f.failDevices = true
		c := f.client(testUser, testPassword)
		if _, err := c.Devices(context.Background()); err == nil || !strings.Contains(err.Error(), "HTTP 500") {
			t.Errorf("Devices error = %v", err)
		}
		if _, err := c.FindPowerOutlet(context.Background(), "", "Plug One"); err == nil {
			t.Error("FindPowerOutlet should propagate a device-list error")
		}
	})
	t.Run("bad json", func(t *testing.T) {
		f := newFakeHubspace(t)
		f.badDevicesJSON = true
		c := f.client(testUser, testPassword)
		if _, err := c.Devices(context.Background()); err == nil || !strings.Contains(err.Error(), "decode Hubspace response") {
			t.Errorf("Devices JSON error = %v", err)
		}
	})
}

func TestLoginErrors(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*fakeHubspace)
		want  string
	}{
		{"auth page status", func(f *fakeHubspace) { f.authPageStatus = 500 }, "login page: HTTP 500"},
		{"auth page no form", func(f *fakeHubspace) { f.authPageNoForm = true }, "no form action"},
		{"auth action host", func(f *fakeHubspace) { f.authActionURL = "https://evil.example/login" }, "unexpected host"},
		{"login status", func(f *fakeHubspace) { f.loginStatus = 400 }, "login: HTTP 400"},
		{"login no code", func(f *fakeHubspace) { f.loginNoCode = true }, "did not contain a code"},
		{"token status", func(f *fakeHubspace) { f.tokenStatus = 500 }, "token exchange: HTTP 500"},
		{"bad token json", func(f *fakeHubspace) { f.badTokenJSON = true }, "decode Hubspace token response"},
		{"missing token", func(f *fakeHubspace) { f.missingToken = true }, "did not contain an access token"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeHubspace(t)
			tc.setup(f)
			c := f.client(testUser, testPassword)
			_, err := c.Devices(context.Background())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Devices error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestDoAPIUnauthorizedTwice(t *testing.T) {
	f := newFakeHubspace(t)
	f.alwaysUnauthorized = true
	c := f.client(testUser, testPassword)
	_, err := c.Devices(context.Background())
	if err == nil || !strings.Contains(err.Error(), "authentication failed after retry") {
		t.Errorf("Devices error = %v, want retry failure", err)
	}
}

func TestDoAPINetworkError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()

	c := New(testUser, testPassword)
	c.authBaseURL = url + "/auth/realms/thd"
	c.apiBaseURL = url + "/v1"
	if _, err := c.Devices(context.Background()); err == nil {
		t.Error("Devices against a closed server should fail")
	}
}

func TestCycleTurnsOnAfterCancel(t *testing.T) {
	f := newFakeHubspace(t)
	c := f.client(testUser, testPassword)
	d := Device{ID: "dev-plug", Name: "Plug One", AccountID: "acct", HasPower: true}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.Cycle(ctx, d, time.Hour) }()

	select {
	case <-f.offCh:
		// Let SetPower consume the off response before canceling; Cycle must
		// still use a detached context for the turn-on.
		time.Sleep(50 * time.Millisecond)
		cancel()
	case <-time.After(2 * time.Second):
		t.Fatal("plug was not turned off")
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Cycle: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Cycle did not return after cancellation")
	}

	select {
	case <-f.onCh:
	default:
		t.Fatal("plug was not turned back on")
	}
}

func TestCycleRestoresPowerWhenOffFails(t *testing.T) {
	f := newFakeHubspace(t)
	f.failOff = true
	c := f.client(testUser, testPassword)
	d := Device{ID: "dev-plug", Name: "Plug One", AccountID: "acct", HasPower: true}

	err := c.Cycle(context.Background(), d, time.Millisecond)
	if err == nil {
		t.Fatal("expected an off error, got nil")
	}
	if !strings.Contains(err.Error(), "power off failed") {
		t.Errorf("error %q should mention the off failure", err)
	}
	select {
	case <-f.onCh:
	default:
		t.Fatal("best-effort power-on was not attempted")
	}
}

func TestCycleZeroDelay(t *testing.T) {
	f := newFakeHubspace(t)
	c := f.client(testUser, testPassword)
	d := Device{ID: "dev-plug", Name: "Plug One", AccountID: "acct", HasPower: true}
	if err := c.Cycle(context.Background(), d, 0); err != nil {
		t.Fatalf("Cycle(0): %v", err)
	}
	if len(f.setCalls) != 2 || f.setCalls[0] != "off" || f.setCalls[1] != "on" {
		t.Errorf("setCalls = %v, want [off on]", f.setCalls)
	}
}

func TestFindPowerOutletEmptyAccount(t *testing.T) {
	f := newFakeHubspace(t)
	f.emptyDevices = true
	c := f.client(testUser, testPassword)
	if _, err := c.FindPowerOutlet(context.Background(), "", "plug"); err == nil || !strings.Contains(err.Error(), "no controllable") {
		t.Errorf("FindPowerOutlet error = %v", err)
	}
}

func TestResolveDeviceEmptyList(t *testing.T) {
	if _, err := ResolveDevice(nil, "", "plug"); err == nil || !strings.Contains(err.Error(), "no controllable") {
		t.Errorf("empty-list error = %v", err)
	}
}

func TestCycleReportsOnFailure(t *testing.T) {
	f := newFakeHubspace(t)
	f.failOn = true
	c := f.client(testUser, testPassword)
	d := Device{ID: "dev-plug", Name: "Plug One", AccountID: "acct", HasPower: true}

	err := c.Cycle(context.Background(), d, time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "plug may be off") {
		t.Errorf("Cycle error = %v, want a may-be-off warning", err)
	}
}

func TestCycleReportsRestoreFailure(t *testing.T) {
	f := newFakeHubspace(t)
	f.failOff = true
	f.failOn = true
	c := f.client(testUser, testPassword)
	d := Device{ID: "dev-plug", Name: "Plug One", AccountID: "acct", HasPower: true}

	err := c.Cycle(context.Background(), d, time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "power-on also failed") {
		t.Errorf("Cycle error = %v, want a restore-failure warning", err)
	}
}

func TestParseFormActionEmpty(t *testing.T) {
	if _, err := parseFormAction([]byte(`<form action="">`)); err == nil {
		t.Error("expected an error for an empty form action")
	}
}

func TestLoginInvalidCredentials(t *testing.T) {
	f := newFakeHubspace(t)
	c := f.client(testUser, "wrong-password")

	_, err := c.PowerOutlets(context.Background())
	if err == nil {
		t.Fatal("expected a login error, got nil")
	}
	if !strings.Contains(err.Error(), "login failed") {
		t.Errorf("error %q should mention login failure", err)
	}
	if strings.Contains(err.Error(), "wrong-password") {
		t.Errorf("error leaked the password: %q", err)
	}
}

func TestReauthOn401(t *testing.T) {
	f := newFakeHubspace(t)
	f.unauthorizedOnce = true
	c := f.client(testUser, testPassword)

	outlets, err := c.PowerOutlets(context.Background())
	if err != nil {
		t.Fatalf("PowerOutlets after 401: %v", err)
	}
	if len(outlets) != 1 {
		t.Errorf("outlets = %d, want 1", len(outlets))
	}
	if f.loginCount != 2 {
		t.Errorf("loginCount = %d, want 2 after re-auth", f.loginCount)
	}
}

func TestFlexInt(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int
	}{
		{`120`, 120},
		{`"300"`, 300},
		{`null`, 0},
	} {
		var got flexInt
		if err := json.Unmarshal([]byte(tc.in), &got); err != nil {
			t.Errorf("unmarshal %s: %v", tc.in, err)
			continue
		}
		if int(got) != tc.want {
			t.Errorf("unmarshal %s = %d, want %d", tc.in, int(got), tc.want)
		}
	}
	if err := json.Unmarshal([]byte(`"abc"`), new(flexInt)); err == nil {
		t.Error("expected an error for a non-numeric string")
	}
}
