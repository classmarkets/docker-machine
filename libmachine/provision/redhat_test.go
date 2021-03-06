package provision

import (
	"testing"

	"github.com/classmarkets/docker-machine/drivers/fakedriver"
	"github.com/classmarkets/docker-machine/libmachine/auth"
	"github.com/classmarkets/docker-machine/libmachine/engine"
	"github.com/classmarkets/docker-machine/libmachine/provision/provisiontest"
	"github.com/classmarkets/docker-machine/libmachine/swarm"
)

func TestRedHatDefaultStorageDriver(t *testing.T) {
	p := NewRedHatProvisioner("", &fakedriver.Driver{})
	p.SSHCommander = provisiontest.NewFakeSSHCommander(provisiontest.FakeSSHCommanderOptions{})
	p.Provision(swarm.Options{}, auth.Options{}, engine.Options{})
	if p.EngineOptions.StorageDriver != "overlay2" {
		t.Fatal("Default storage driver should be overlay2")
	}
}
