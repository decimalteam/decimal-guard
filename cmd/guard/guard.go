package main

import (
	"fmt"
	"os"

	"github.com/mkideal/cli"

	"bitbucket.org/decimalteam/decimal-guard/guard"
	"bitbucket.org/decimalteam/decimal-guard/utils"
	"bitbucket.org/decimalteam/decimal-guard/version"
)

type argT struct {
	cli.Helper
	Version bool `cli:"v,version" usage:"view version of guard"`
}

func main() {
	cli.Run(new(argT), func(ctx *cli.Context) error {
		argv := ctx.Argv().(*argT)

		if argv.Version {
			fmt.Println(version.GuardVersion)
			os.Exit(0)
		}

		startGuard()
		return nil
	})
}

func startGuard() {
	// Load validator guard configuration
	config := &guard.Config{}
	err := utils.LoadConfig(config)
	if err != nil {
		panic(err)
	}

	// Create validator guard
	g, err := guard.NewGuard(*config)
	if err != nil {
		panic(err)
	}

	// Run created validator guard
	err = g.Run()
	if err != nil {
		panic(err)
	}
}
