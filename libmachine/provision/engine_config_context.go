package provision

import (
	"github.com/classmarkets/docker-machine/libmachine/auth"
	"github.com/classmarkets/docker-machine/libmachine/engine"
)

type EngineConfigContext struct {
	DockerPort       int
	AuthOptions      auth.Options
	EngineOptions    engine.Options
	DockerOptionsDir string
}
