package api

import (
	"net/http"
	"time"

	"github.com/tom/ai-dev/frontends/lanctl-go/internal/localops"
	"github.com/tom/ai-dev/frontends/lanctl-go/internal/network"
	"github.com/tom/ai-dev/frontends/lanctl-go/internal/sshops"
)

// operations is the seam through which handlers reach the outside world
// (network, SSH, local systemctl/journalctl). Tests swap the package-level
// `ops` for a fake so no real host or service is ever touched.
type operations interface {
	SendMagicPacket(mac, broadcast string, port int) error
	CheckSSHPort(host string, timeout time.Duration) bool

	Shutdown() error
	ServiceStatuses(services []string) (map[string]string, error)
	ServiceControl(service, action string) error
	JournalLines(service string, n int) (string, error)
	StreamJournal(service string, w http.ResponseWriter, r *http.Request)

	SSHShutdown(ip, user, keyPath string) error
	SSHServiceStatuses(ip, user, keyPath string, services []string) (map[string]string, error)
	SSHServiceControl(ip, user, keyPath, service, action string) error
	SSHJournalLines(ip, user, keyPath, service string, n int) (string, error)
	SSHStreamJournal(ip, user, keyPath, service string, w http.ResponseWriter, r *http.Request)
	StreamVPNRepair(ip, user, keyPath, token string, w http.ResponseWriter, r *http.Request)
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

func (realOperations) ServiceStatuses(services []string) (map[string]string, error) {
	return localops.ServiceStatuses(services)
}

func (realOperations) ServiceControl(service, action string) error {
	return localops.ServiceControl(service, action)
}

func (realOperations) JournalLines(service string, n int) (string, error) {
	return localops.JournalLines(service, n)
}

func (realOperations) StreamJournal(service string, w http.ResponseWriter, r *http.Request) {
	localops.StreamJournal(service, w, r)
}

func (realOperations) SSHShutdown(ip, user, keyPath string) error {
	return sshops.Shutdown(ip, user, keyPath)
}

func (realOperations) SSHServiceStatuses(ip, user, keyPath string, services []string) (map[string]string, error) {
	return sshops.ServiceStatuses(ip, user, keyPath, services)
}

func (realOperations) SSHServiceControl(ip, user, keyPath, service, action string) error {
	return sshops.ServiceControl(ip, user, keyPath, service, action)
}

func (realOperations) SSHJournalLines(ip, user, keyPath, service string, n int) (string, error) {
	return sshops.JournalLines(ip, user, keyPath, service, n)
}

func (realOperations) SSHStreamJournal(ip, user, keyPath, service string, w http.ResponseWriter, r *http.Request) {
	sshops.StreamJournal(ip, user, keyPath, service, w, r)
}

func (realOperations) StreamVPNRepair(ip, user, keyPath, token string, w http.ResponseWriter, r *http.Request) {
	sshops.StreamVPNRepair(ip, user, keyPath, token, w, r)
}
