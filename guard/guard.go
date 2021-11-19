package guard

import (
	"context"
	"errors"
	"fmt"
	ctypes "github.com/tendermint/tendermint/rpc/core/types"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"

	decimal "bitbucket.org/decimalteam/go-node/config"
	"github.com/tendermint/tendermint/libs/log"
)

// Guard is an object managing set of watchers connected to different nodes.
type Guard struct {
	config Config

	// Configured watchers are stored by node connection endpoints
	watchers []*Watcher

	chanEventStatus   chan eventStatus
	chanEventNewBlock chan eventNewBlock
	chanEventNoBlock  chan eventNoBlock

	logger log.Logger
}

// NewGuard creates new Guard instance.
func NewGuard(config Config) (*Guard, error) {
	logger := log.NewTMLogger(log.NewSyncWriter(os.Stdout))

	// Exit if new block timeout is not valid
	if config.NewBlockTimeout < 7 {
		err := "ERROR: Environment variable NEW_BLOCK_TIMEOUT is not set up or set to value less than 7 (seconds)! "
		logger.Error(err)
		return nil, errors.New(err)
	}

	err := UpdateInfo.Check()
	if err != nil {
		return nil, err
	}

	// Prepare channels for events
	chanEventStatus := make(chan eventStatus, 1024)
	chanEventNewBlock := make(chan eventNewBlock, 1024)
	chanEventNoBlock := make(chan eventNoBlock, 1024)

	// Split node endpoints and create necessary watchers
	endpoints := strings.Split(config.NodesEndpoints, ",")
	watchers := make([]*Watcher, 0, len(endpoints))
	for _, endpoint := range endpoints {
		w, err := NewWatcher(config, strings.TrimSpace(endpoint), chanEventStatus, chanEventNewBlock, chanEventNoBlock)
		if err != nil {
			return nil, err
		}
		watchers = append(watchers, w)
	}

	// Create guard instance
	return &Guard{
		config:            config,
		watchers:          watchers,
		chanEventStatus:   chanEventStatus,
		chanEventNewBlock: chanEventNewBlock,
		chanEventNoBlock:  chanEventNoBlock,
		logger:            logger,
	}, nil
}

// Run starts watchers and stops only when application is closed.
func (guard *Guard) Run() (err error) {

	// Subscribe to interrupt signal to normally exit on Ctrl+C
	chanInterrupt := make(chan os.Signal, 1)
	signal.Notify(chanInterrupt, os.Interrupt, os.Kill)

	// Stop watchers when guard is stopped
	defer func() {
		for _, w := range guard.watchers {
			err := w.Stop()
			if err != nil {
				w.logger.Error(fmt.Sprintf(
					"[%s] ERROR: Unable to disconnect from the node: %s",
					w.endpoint,
					err,
				))
			}
		}
	}()

	// Ensure set-offline tx is valid
	err = guard.validateSetOfflineTx()
	if err != nil {
		guard.logger.Error(fmt.Sprintf(
			"ERROR: Unable to start guard because of invalid set-offline tx is provided: %s",
			err,
		))
		return
	}

	consAddress, err := sdk.ConsAddressFromHex(guard.config.ValidatorAddress)
	if err != nil {
		guard.logger.Error(err.Error())
		return
	}

	sdk.GetConfig().SetBech32PrefixForConsensusNode(decimal.DecimalPrefixConsAddr, decimal.DecimalPrefixConsPub)
	guard.logger.Info(fmt.Sprintf("ConsValidatorAddress = %s", consAddress.String()))

	// API = decapi.NewAPI("https://testnet-gate.decimalchain.com/api")

	// Start watchers
	for _, w := range guard.watchers {
		go func(w *Watcher) {
			w.logger.Info(fmt.Sprintf("[%s] Connecting to the node...", w.endpoint))

			if err := w.Start(); err != nil {
				w.logger.Error(fmt.Sprintf("[%s] ERROR: Unable to connect to the node: %s", w.endpoint, err))
			}
		}(w)
	}

	// Prepare tickers
	printTicker := time.NewTicker(time.Minute)
	healthTicker := time.NewTicker(time.Second)

	go func() {
		for _, _ = range []int{1, 2, 3} {
			time.Sleep(30 * time.Second)
			for _, w := range guard.watchers {
				err := w.client.Stop()
				w.logger.Info(fmt.Sprintf("[%s] CLOSED", w.endpoint))
				if err != nil {
					w.logger.Error(fmt.Sprintf("[%s] FFFFF: %s", w.endpoint, err))
				}
			}
		}
	}()

	// Main loop
	for {
		select {
		case v := <-chanInterrupt:
			switch v {
			case os.Interrupt:
				guard.logger.Info("Interrupt signal received. Shutting down...")
			case os.Kill:
				guard.logger.Info("Kill signal received. Shutting down...")
			}
			return
		case <-guard.chanEventStatus:
			// Chain status is retrieved from the node
		case e := <-guard.chanEventNewBlock:
			// Check if count of missed to sign blocks is not critically high yet
			if e.missedBlocks < guard.config.MissedBlocksLimit {
				if e.missedBlocks > 0 {
					// Log warning
					guard.logger.Info(fmt.Sprintf(
						"WARNING: There are some blocks missed to sign (%d of last %d)",
						e.missedBlocks, guard.config.MissedBlocksWindow,
					))
				}
				continue
			}
			// Log error
			guard.logger.Error(fmt.Sprintf(
				"ERROR: There are too many blocks missed to sign (%d of last %d)",
				e.missedBlocks, guard.config.MissedBlocksWindow,
			))
			// Broadcast set-offline transaction using all available nodes and then stop working
			err = guard.setOffline()
			return
		case e := <-guard.chanEventNoBlock:
			// Ensure there is at lease one connected node
			var hasAtLeastOneConnectedNode bool

			for _, w := range guard.watchers {
				if time.Now().Sub(w.latestBlockTime) <= time.Duration(w.config.NewBlockTimeout)*time.Second {
					hasAtLeastOneConnectedNode = true
					break
				}
			}

			if !hasAtLeastOneConnectedNode {
				// Log error
				guard.logger.Error(fmt.Sprintf(
					"ERROR: There are no any new blocks during %d seconds (latest block %d at %s)!",
					guard.config.NewBlockTimeout, e.latestBlock, e.latestBlockTime,
				))
			}
		case <-printTicker.C:
			// Log guard state
			guard.printState()
		case <-healthTicker.C:
			// Ensure there is at lease one connected node and reconnect not connected ones
			connected := false
			for _, w := range guard.watchers {
				if !w.IsRunning() {
					err = w.Restart()
					if err != nil {
						w.logger.Info(fmt.Sprintf("[%s] ERROR: Failed to restart watcher: %s", w.endpoint, err))
					}
				} else {
					connected = true
				}
			}
			if !connected {
				// Log error
				guard.logger.Error(fmt.Sprintf(
					"ERROR: There are no any new blocks during %d seconds!",
					guard.config.NewBlockTimeout,
				))
			}
		}
	}
}

////////////////////////////////////////////////////////////////////////////////
// Internal functions
////////////////////////////////////////////////////////////////////////////////

// validateSetOfflineTx ensure provided set-offline tx is valid at current moment.
func (guard *Guard) validateSetOfflineTx() (err error) {
	// TODO: Ensure provided set-offline tx is valid
	return
}

// setOffline tries to set guarding validator offline using all available nodes.
func (guard *Guard) setOffline() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	// Broadcast set-offline tx to blockchain
	var txAlreadyBroadcast bool

	chanBroadcastTx := make(chan *ctypes.ResultBroadcastTx, len(guard.watchers))

	for _, w := range guard.watchers {
		go func(w *Watcher) {
			for !txAlreadyBroadcast {
				broadcastTx, err := w.broadcastSetOfflineTx()
				if err != nil {
					w.logger.Info(fmt.Sprintf(
						"[%s] WARNING: Unable to broadcast set-offline tx: %s. Retry...", w.endpoint, err,
					))
					time.Sleep(200 * time.Millisecond)
					continue
				}

				if broadcastTx != nil {
					chanBroadcastTx <- broadcastTx

					txAlreadyBroadcast = true
				}
			}
		}(w)
	}

	// Wait until tx is broadcast to blockchain
	var broadcastTx *ctypes.ResultBroadcastTx
	select {
	case <-ctx.Done():
		guard.logger.Error("ERROR: Unable to wait for set-offline tx broadcasting during 15 minutes")
		return ctx.Err()
	case broadcastTx = <-chanBroadcastTx:
		guard.logger.Info(fmt.Sprintf(
			"Set-offline tx broadcast: %s", broadcastTx.Hash.String(),
		))
	}

	// Confirm broadcast set-offline tx
	var wg sync.WaitGroup
	wg.Add(len(guard.watchers))
	chanResultTx := make(chan *ctypes.ResultTx, len(guard.watchers))

	guard.logger.Info("Waiting for set-offline tx confirmation...")

	for _, w := range guard.watchers {
		go func(w *Watcher, broadcastTx *ctypes.ResultBroadcastTx) {
			defer wg.Done()

			childCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
			defer cancel()

			confirmedTx, err := w.confirmSetOfflineTx(childCtx, broadcastTx)
			if err != nil && err != context.Canceled {
				w.logger.Info(fmt.Sprintf(
					"[%s] ERROR: Unable to confirm set-offline tx: %s.", w.endpoint, err,
				))
				return
			}

			chanResultTx <- confirmedTx
		}(w, broadcastTx)
	}

	// Wait until tx execution result is available
	var tx *ctypes.ResultTx
	select {
	case <-ctx.Done():
		guard.logger.Error("ERROR: Unable to wait for set-offline tx confirmation during 15 minutes")
		return ctx.Err()
	case tx = <-chanResultTx:
		cancel()
	}

	// Check if tx was successful and confirmed by the network
	if tx.TxResult.IsErr() {
		guard.logger.Error(fmt.Sprintf(
			"ERROR: Set-offline tx was failed and is not confirmed by the network: %s",
			tx.TxResult.String(),
		))
		return errors.New("tx was failed")
	}

	guard.logger.Info(fmt.Sprintf(
		"Set-offline tx is confirmed: %s", broadcastTx.Hash.String(),
	))

	// Wait until everything is done
	wg.Wait()

	// Log success
	guard.logger.Info("Validator successfully set offline and hopefully prevented stake lost!")

	return nil
}

// printState prints current state of the guard to the standard output.
func (guard *Guard) printState() {
	var sb strings.Builder
	endpointMaxLength := 0
	for _, w := range guard.watchers {
		if endpointMaxLength < len(w.endpoint) {
			endpointMaxLength = len(w.endpoint)
		}
	}
	sb.WriteString("====================================================================================================")
	for i, w := range guard.watchers {
		endpointStr := fmt.Sprintf(fmt.Sprintf("%%-%ds", endpointMaxLength), w.endpoint)
		var statusStr string
		if w.IsRunning() {
			if w.latestBlockTime.Add(time.Minute).Before(time.Now()) {
				statusStr = fmt.Sprintf("%-14s", "syncing")
			} else {
				statusStr = fmt.Sprintf("%-14s", "connected")
			}
		} else if w.connectedAt.IsZero() {
			statusStr = fmt.Sprintf("%-14s", "not connected")
		} else {
			reconnectingTime := w.connectedAt.Add(time.Duration(w.config.NewBlockTimeout) * time.Second)
			if reconnectingTime.After(time.Now()) {
				statusStr = fmt.Sprintf("%-14s", "reconnecting")
			} else {
				statusStr = fmt.Sprintf("%-14s", "disconnected")
			}
		}
		var ageStr string
		if w.latestBlock > 0 && !w.latestBlockTime.IsZero() {
			age := time.Now().Sub(w.latestBlockTime)
			if age < time.Minute {
				ageStr = fmt.Sprintf(" (%d seconds ago)", int(age.Seconds()))
			} else if age < time.Hour {
				ageStr = fmt.Sprintf(" (%d minutes ago)", int(age.Minutes()))
			} else if age < 24*time.Hour {
				ageStr = fmt.Sprintf(" (%d hours ago)", int(age.Hours()))
			} else {
				ageStr = fmt.Sprintf(" (at %s)", w.latestBlockTime)
			}
		}
		sb.WriteString(fmt.Sprintf("\nWatcher#%d: %s  |  state: %s", i, endpointStr, statusStr))
		if w.latestBlock > 0 && !w.latestBlockTime.IsZero() {
			sb.WriteString(fmt.Sprintf("  |  latest block: %d%s", w.latestBlock, ageStr))
		}
	}
	sb.WriteString("\n====================================================================================================")
	fmt.Println(sb.String())
}
