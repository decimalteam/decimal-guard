package watcher

import (
	"fmt"
	"os"
	"os/signal"
	"strings"

	"github.com/tendermint/tendermint/libs/log"
)

// Config is an object containing validator guard and watchers configuration.
type Config struct {
	NodesEndpoints     string `env:"NODES_ENDPOINTS" mandatory:"true" default:"tcp://localhost:26657"`
	MissedBlocksLimit  int    `env:"MISSED_BLOCKS_LIMIT" mandatory:"true" default:"8"`
	MissedBlocksWindow int    `env:"MISSED_BLOCKS_WINDOW" mandatory:"true" default:"24"`
	NewBlockTimeout    int    `env:"NEW_BLOCK_TIMEOUT" mandatory:"true" default:"0"`
	ValidatorAddress   string `env:"VALIDATOR_ADDRESS" mandatory:"true"`
	SetOfflineTx       string `env:"SET_OFFLINE_TX" mandatory:"true"`
}

// Guard is an object managing set of watchers connected to different nodes.
type Guard struct {
	config               *Config
	watchers             []*Watcher
	chanEventMissedBlock chan eventMissedBlock
	chanEventNoBlock     chan eventNoBlock
	logger               log.Logger
}

// NewGuard creates new Guard instance.
func NewGuard(config *Config) (*Guard, error) {
	var err error
	// Prepare channels for events
	chanEventMissedBlock := make(chan eventMissedBlock, 1024)
	chanEventNoBlock := make(chan eventNoBlock, 1024)
	// Split node endpoints and create necessary watchers
	endpoints := strings.Split(config.NodesEndpoints, ",")
	watchers := make([]*Watcher, len(endpoints))
	for i, endpoint := range endpoints {
		watchers[i], err = NewWatcher(config, endpoint, chanEventMissedBlock, chanEventNoBlock)
		if err != nil {
			// TODO: Reconnect later instead?
			return nil, err
		}
	}
	guard := &Guard{
		config:               config,
		watchers:             watchers,
		chanEventMissedBlock: chanEventMissedBlock,
		chanEventNoBlock:     chanEventNoBlock,
		logger:               log.NewTMLogger(log.NewSyncWriter(os.Stdout)),
	}
	return guard, nil
}

// Run starts watchers and stops only when application is closed.
func (guard *Guard) Run() (err error) {

	// Subscribe to interrupt signal to normally exit on Ctrl+C
	chanInterrupt := make(chan os.Signal, 1)
	signal.Notify(chanInterrupt, os.Interrupt, os.Kill)

	// Start watchers
	for _, w := range guard.watchers {
		go w.Start()
	}
	defer func() {
		// Stop watchers
		for _, w := range guard.watchers {
			w.Stop(err)
		}
	}()

	// Main loop
	for {
		select {
		case e := <-guard.chanEventMissedBlock:
			if e.missedBlocks >= guard.config.MissedBlocksLimit {
				guard.logger.Info(fmt.Sprintf(
					"WARNING: There are too many blocks missed to sign (%d of last %d)",
					e.missedBlocks, guard.config.MissedBlocksWindow,
				))
				for _, w := range guard.watchers {
					err := w.setOffline()
					if err != nil {
						guard.logger.Info(fmt.Sprintf(
							"WARNING: Unable to broadcast validator/set_offline transaction: %s", err.Error(),
						))
					}
				}
				return
			}
		case e := <-guard.chanEventNoBlock:
			guard.logger.Info(fmt.Sprintf(
				"WARNING: There are no new blocks during %d seconds (last block: %d)! Disconnecting...",
				guard.config.NewBlockTimeout, e.lastBlock,
			))
		case v := <-chanInterrupt:
			switch v {
			case os.Interrupt:
				guard.logger.Info("Interrupt signal received. Shutting down...")
			case os.Kill:
				guard.logger.Info("Kill signal received. Shutting down...")
			}
			return
		}
	}
}
