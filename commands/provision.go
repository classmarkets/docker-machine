package commands

import "github.com/classmarkets/docker-machine/libmachine"

func cmdProvision(c CommandLine, api libmachine.API) error {
	return runAction("provision", c, api)
}
