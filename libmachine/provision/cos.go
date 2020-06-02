package provision

import (
	"fmt"

	"github.com/docker/machine/libmachine/auth"
	"github.com/docker/machine/libmachine/drivers"
	"github.com/docker/machine/libmachine/engine"
	"github.com/docker/machine/libmachine/log"
	"github.com/docker/machine/libmachine/provision/pkgaction"
	"github.com/docker/machine/libmachine/swarm"
)

func init() {
	Register("Container Optimized OS", &RegisteredProvisioner{
		New: NewCOSProvisioner,
	})
}

func NewCOSProvisioner(d drivers.Driver) Provisioner {
	return &COSProvisioner{
		SystemdProvisioner{
			GenericProvisioner{
				SSHCommander:      GenericSSHCommander{Driver: d},
				DockerOptionsDir:  "/etc/docker",
				DaemonOptionsFile: "/etc/default/docker",
				OsReleaseID:       "cos",
				Packages: []string{
					"curl",
				},
				Driver: d,
			},
		},
	}
}

type COSProvisioner struct {
	SystemdProvisioner
}

func (provisioner *COSProvisioner) String() string {
	return "cos"
}

func (provisioner *COSProvisioner) Package(name string, action pkgaction.PackageAction) error {
	return nil
}

func (provisioner *COSProvisioner) Provision(swarmOptions swarm.Options, authOptions auth.Options, engineOptions engine.Options) error {
	provisioner.SwarmOptions = swarmOptions
	provisioner.AuthOptions = authOptions
	provisioner.EngineOptions = engineOptions

	if err := provisioner.SetHostname(provisioner.Driver.GetMachineName()); err != nil {
		return err
	}

	if err := makeDockerOptionsDir(provisioner); err != nil {
		return err
	}

	log.Debugf("Preparing certificates")
	provisioner.AuthOptions = setRemoteAuthOptions(provisioner)

	log.Debugf("Setting up certificates")
	if err := ConfigureAuth(provisioner); err != nil {
		return err
	}

	log.Debug("Configuring swarm")
	if err := configureSwarm(provisioner, swarmOptions, provisioner.AuthOptions); err != nil {
		return err
	}

	cmd := fmt.Sprintf("sudo iptables -w -A INPUT -p tcp --dport %d -j ACCEPT", // TODO: source IP
		engine.DefaultPort,
	)
	if out, err := provisioner.SSHCommand(cmd); err != nil {
		log.Warnf("Error configuring iptables: %s", err)
		log.Debugf("'sudo iptables' output:\n%s", out)
		return err
	}

	return nil
}

func (provisioner *COSProvisioner) GenerateDockerOptions(dockerPort int) (*DockerOptions, error) {
	// use /etc/default/docker, not a systemd override
	return provisioner.SystemdProvisioner.GenericProvisioner.GenerateDockerOptions(dockerPort)
}
