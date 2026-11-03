package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/tkdlabs/lanctl/internal/hubspace"
	"github.com/tkdlabs/lanctl/internal/localops"
	"github.com/tkdlabs/lanctl/internal/network"
	"github.com/tkdlabs/lanctl/internal/sshops"
)

// plugRef identifies a configured smart plug. Host is the lanctl host name and
// is used as the result key in HubspacePlugStates.
type plugRef struct {
	Host       string
	DeviceID   string
	DeviceName string
}

// operations is the seam through which handlers reach the outside world
// (network, SSH, local systemctl/journalctl). Tests swap the package-level
// `ops` for a fake so no real host or service is ever touched.
type operations interface {
	SendMagicPacket(mac, broadcast string, port int) error
	CheckSSHPort(host string, timeout time.Duration) bool

	Shutdown() error
	ServiceStatuses(services []string, userScope bool) (map[string]string, error)
	ServiceControl(service, action string, userScope bool) error
	JournalLines(service string, n int, userScope bool) (string, error)
	StreamJournal(service string, userScope bool, w http.ResponseWriter, r *http.Request)

	SSHShutdown(ip, user, keyPath string) error
	SSHServiceStatuses(ip, user, keyPath string, services []string, userScope bool) (map[string]string, error)
	SSHServiceControl(ip, user, keyPath, service, action string, userScope bool) error
	SSHJournalLines(ip, user, keyPath, service string, n int, userScope bool) (string, error)
	SSHStreamJournal(ip, user, keyPath, service string, userScope bool, w http.ResponseWriter, r *http.Request)
	StreamVPNRepair(ip, user, keyPath, token string, w http.ResponseWriter, r *http.Request)

	HubspaceDevices(ctx context.Context, username, password string) ([]hubspace.Device, error)
	HubspacePlugStates(ctx context.Context, username, password string, refs []plugRef) (map[string]bool, error)
	HubspaceSetPlug(ctx context.Context, username, password string, ref plugRef, on bool) error
	HubspaceCyclePlug(ctx context.Context, username, password string, ref plugRef, off time.Duration) error
}

// ops is the active operations implementation. Swapped in tests.
var ops operations = realOperations{}

type realOperations struct{}

func (realOperations) SendMagicPacket(mac, broadcast string, port int) error {
	return network.SendMagicPacket(mac, broadcast, port)
}

func (realOperations) CheckSSHPort(host string, timeout time.Duration) bool {
	return network.CheckSSHPort(host, timeout)
}

func (realOperations) Shutdown() error {
	return localops.Shutdown()
}

func (realOperations) ServiceStatuses(services []string, userScope bool) (map[string]string, error) {
	return localops.ServiceStatuses(services, userScope)
}

func (realOperations) ServiceControl(service, action string, userScope bool) error {
	return localops.ServiceControl(service, action, userScope)
}

func (realOperations) JournalLines(service string, n int, userScope bool) (string, error) {
	return localops.JournalLines(service, n, userScope)
}

func (realOperations) StreamJournal(service string, userScope bool, w http.ResponseWriter, r *http.Request) {
	localops.StreamJournal(service, userScope, w, r)
}

func (realOperations) SSHShutdown(ip, user, keyPath string) error {
	return sshops.Shutdown(ip, user, keyPath)
}

func (realOperations) SSHServiceStatuses(ip, user, keyPath string, services []string, userScope bool) (map[string]string, error) {
	return sshops.ServiceStatuses(ip, user, keyPath, services, userScope)
}

func (realOperations) SSHServiceControl(ip, user, keyPath, service, action string, userScope bool) error {
	return sshops.ServiceControl(ip, user, keyPath, service, action, userScope)
}

func (realOperations) SSHJournalLines(ip, user, keyPath, service string, n int, userScope bool) (string, error) {
	return sshops.JournalLines(ip, user, keyPath, service, n, userScope)
}

func (realOperations) SSHStreamJournal(ip, user, keyPath, service string, userScope bool, w http.ResponseWriter, r *http.Request) {
	sshops.StreamJournal(ip, user, keyPath, service, userScope, w, r)
}

func (realOperations) StreamVPNRepair(ip, user, keyPath, token string, w http.ResponseWriter, r *http.Request) {
	sshops.StreamVPNRepair(ip, user, keyPath, token, w, r)
}

func (realOperations) HubspaceDevices(ctx context.Context, username, password string) ([]hubspace.Device, error) {
	return hubspace.New(username, password).Devices(ctx)
}

func (realOperations) HubspacePlugStates(ctx context.Context, username, password string, refs []plugRef) (map[string]bool, error) {
	states := make(map[string]bool, len(refs))
	if len(refs) == 0 {
		return states, nil
	}
	client := hubspace.New(username, password)
	outlets, err := client.PowerOutlets(ctx)
	if err != nil {
		return nil, err
	}
	for _, ref := range refs {
		device, err := hubspace.ResolveDevice(outlets, ref.DeviceID, ref.DeviceName)
		if err != nil {
			return nil, fmt.Errorf("host %s: %w", ref.Host, err)
		}
		on, err := client.State(ctx, device)
		if err != nil {
			return nil, fmt.Errorf("host %s: %w", ref.Host, err)
		}
		states[ref.Host] = on
	}
	return states, nil
}

func (realOperations) HubspaceSetPlug(ctx context.Context, username, password string, ref plugRef, on bool) error {
	client := hubspace.New(username, password)
	device, err := client.FindPowerOutlet(ctx, ref.DeviceID, ref.DeviceName)
	if err != nil {
		return fmt.Errorf("host %s: %w", ref.Host, err)
	}
	if err := client.SetPower(ctx, device, on); err != nil {
		return fmt.Errorf("host %s: %w", ref.Host, err)
	}
	return nil
}

func (realOperations) HubspaceCyclePlug(ctx context.Context, username, password string, ref plugRef, off time.Duration) error {
	client := hubspace.New(username, password)
	device, err := client.FindPowerOutlet(ctx, ref.DeviceID, ref.DeviceName)
	if err != nil {
		return fmt.Errorf("host %s: %w", ref.Host, err)
	}
	if err := client.Cycle(ctx, device, off); err != nil {
		return fmt.Errorf("host %s: %w", ref.Host, err)
	}
	return nil
}
