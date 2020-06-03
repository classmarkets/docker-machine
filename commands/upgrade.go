package commands

import "github.com/classmarkets/docker-machine/libmachine"

func cmdUpgrade(c CommandLine, api libmachine.API) error {
	return runAction("upgrade", c, api)
}
