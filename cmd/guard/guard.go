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

	// Create validator watcher
	w, err := watcher.NewWatcher(config)
	if err != nil {
		panic(err)
	}

	// Run created validator watcher
	err = w.Run()
	if err != nil {
		panic(err)
	}
}
