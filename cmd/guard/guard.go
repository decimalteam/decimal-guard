package main

import (
	"bitbucket.org/decimalteam/decimal-guard/utils"
	"bitbucket.org/decimalteam/decimal-guard/watcher"
)

func main() {

	// Load validator watcher configuration
	config := &watcher.Config{}
	err := utils.LoadConfig(config)
	if err != nil {
		panic(err)
	}

	// Create validator guard
	guard, err := watcher.NewGuard(config)
	if err != nil {
		panic(err)
	}

	// Run created validator guard
	err = guard.Run()
	if err != nil {
		panic(err)
	}
}
