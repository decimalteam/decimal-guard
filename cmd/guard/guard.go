package main

import (
	"bitbucket.org/decimalteam/decimal-guard/guard"
	"bitbucket.org/decimalteam/decimal-guard/utils"
)

func main() {

	// Load validator guard configuration
	config := &guard.Config{}
	err := utils.LoadConfig(config)
	if err != nil {
		panic(err)
	}

	// Create validator guard
	guard, err := guard.NewGuard(config)
	if err != nil {
		panic(err)
	}

	// Run created validator guard
	err = guard.Run()
	if err != nil {
		panic(err)
	}
}
