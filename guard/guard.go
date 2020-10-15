package guard

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	"github.com/tendermint/tendermint/libs/log"
	ctypes "github.com/tendermint/tendermint/rpc/core/types"
)

// Guard is an object managing set of watchers connected to different nodes.
type Guard struct {
	config *Config

	// Configured watchers are stored by node connection endpoints
	watchers []*Watcher

	chanEventStatus   chan eventStatus
	chanEventNewBlock chan eventNewBlock
	chanEventNoBlock  chan eventNoBlock

	logger log.Logger
}

// NewGuard creates new Guard instance.
func NewGuard(config *Config) (guard *Guard, err error) {
	logger := log.NewTMLogger(log.NewSyncWriter(os.Stdout))

	// Exit if new block timeout is not valid
	if config.NewBlockTimeout < 7 {
		logger.Error("ERROR: Environment variable NEW_BLOCK_TIMEOUT is not set up or set to value less than 7 (seconds)!")
		return
	}

	// Prepare channels for events
	chanEventStatus := make(chan eventStatus, 1024)
	chanEventNewBlock := make(chan eventNewBlock, 1024)
	chanEventNoBlock := make(chan eventNoBlock, 1024)

	// Split node endpoints and create necessary watchers
	endpoints := strings.Split(config.NodesEndpoints, ",")
	watchers := make([]*Watcher, 0, len(endpoints))
	for _, endpoint := range endpoints {
		w, e := NewWatcher(config, strings.TrimSpace(endpoint), chanEventStatus, chanEventNewBlock, chanEventNoBlock)
		if e != nil {
			err = e
			return
		}
		watchers = append(watchers, w)
	}

	// Create guard instance
	guard = &Guard{
		config:            config,
		watchers:          watchers,
		chanEventStatus:   chanEventStatus,
		chanEventNewBlock: chanEventNewBlock,
		chanEventNoBlock:  chanEventNoBlock,
		logger:            logger,
	}
	return
}

// Run starts watchers and stops only when application is closed.
func (guard *Guard) Run() (err error) {

	// Subscribe to interrupt signal to normally exit on Ctrl+C
	chanInterrupt := make(chan os.Signal, 1)
	signal.Notify(chanInterrupt, os.Interrupt, os.Kill)

	// Stop watchers when guard is stopped
	defer func() {
		for _, w := range guard.watchers {
			w.Stop(err)
		}
	}()

	// Start watchers
	for _, w := range guard.watchers {
		go func(w *Watcher) {
			if err := w.Start(); err != nil {
				w.logger.Info(fmt.Sprintf("[%s] WARNING: Unable to connect to the node: %s", w.endpoint, err))
			}
		}(w)
	}

	// Ensure set-offline tx is valid
	err = guard.validateSetOfflineTx()
	if err != nil {
		guard.logger.Error(fmt.Sprintf(
			"ERROR: Unable to start guard because of invalid set-offline tx is provided: %s",
			err,
		))
		return
	}

	// Prepare tickers
	printTicker := time.NewTicker(time.Minute)
	healthTicker := time.NewTicker(time.Second)

	// Main loop
	for {
		select {
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
			for _, w := range guard.watchers {
				if time.Now().Sub(w.latestBlockTime) <= time.Duration(w.config.NewBlockTimeout)*time.Second {
					continue
				}
			}
			// Log error
			guard.logger.Error(fmt.Sprintf(
				"ERROR: There are no any new blocks during %d seconds (latest block %d at %s)! Disconnecting...",
				guard.config.NewBlockTimeout, e.latestBlock, e.latestBlockTime,
			))
			return
		case <-printTicker.C:
			guard.printState()
		case <-healthTicker.C:
			// Ensure there is at lease one connected node and reconnect not connected ones
			connected := false
			wg := sync.WaitGroup{}
			wg.Add(len(guard.watchers))
			for _, w := range guard.watchers {
				go func(w *Watcher) {
					defer wg.Done()
					if !w.IsRunning() {
						err := w.Start()
						if err != nil {
							w.logger.Info(fmt.Sprintf("[%s] WARNING: Unable to connect to the node: %s", w.endpoint, err))
						}
					} else {
						connected = true
					}
				}(w)
			}
			wg.Wait()
			if !connected {
				// Log error
				guard.logger.Error(fmt.Sprintf(
					"ERROR: There are no any new blocks during %d seconds! Disconnecting...",
					guard.config.NewBlockTimeout,
				))
				return
			}
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

////////////////////////////////////////////////////////////////////////////////
// Internal functions
////////////////////////////////////////////////////////////////////////////////

// validateSetOfflineTx ensure provided set-offline tx is valid at current moment.
func (guard *Guard) validateSetOfflineTx() (err error) {
	// TODO: Ensure provided set-offline tx is valid
	return
}

// setOffline tries to set guarding validator offline using all available nodes.
func (guard *Guard) setOffline() (err error) {

	type Offliner struct {
		watcher      *Watcher
		endpoint     string
		chanTxHash   chan *ctypes.ResultBroadcastTx
		chanTxResult chan *ctypes.ResultTx
		chanError    chan error
		chanStop     chan interface{}
	}

	wg := sync.WaitGroup{}
	chanTxHash := make(chan *ctypes.ResultBroadcastTx, len(guard.watchers))
	chanTxResult := make(chan *ctypes.ResultTx, len(guard.watchers))

	// Prepare everything for mass tx broadcasting
	offliners := make([]*Offliner, 0, len(guard.watchers))
	for _, watcher := range guard.watchers {
		offliner := &Offliner{
			watcher:      watcher,
			endpoint:     watcher.endpoint,
			chanTxHash:   make(chan *ctypes.ResultBroadcastTx),
			chanTxResult: make(chan *ctypes.ResultTx),
			chanError:    make(chan error),
			chanStop:     make(chan interface{}),
		}
		offliners = append(offliners, offliner)
	}

	// Start trying to broadcast set-offline tx to all available nodes
	wg.Add(len(offliners))
	for _, o := range offliners {
		go func(o *Offliner) {
			defer wg.Done()
			go func() {
				select {
				case txHash := <-o.chanTxHash:
					guard.logger.Info(fmt.Sprintf(
						"[%s] Set-offline tx successfully broadcasted: %s",
						o.endpoint, txHash.Hash.String(),
					))
					chanTxHash <- txHash
				case tx := <-o.chanTxResult:
					guard.logger.Info(fmt.Sprintf(
						"[%s] Set-offline tx confirmed: %s",
						o.endpoint, tx.TxResult.String(),
					))
					chanTxResult <- tx
				case <-o.chanError:
					guard.logger.Error(fmt.Sprintf(
						"[%s] ERROR: Unable to broadcast set-offline tx: %s",
						o.endpoint, err,
					))
				case <-o.chanStop:
					return
				}
			}()
			err := o.watcher.broadcastSetOfflineTx(o.chanTxHash, o.chanTxResult, o.chanStop)
			if err != nil {
				o.chanError <- err
			}
		}(o)
	}

	// Wait until tx hash is received
	var txHash *ctypes.ResultBroadcastTx
	txHashTimeout := time.After(5 * time.Minute)
	select {
	case txHash = <-chanTxHash:
		guard.logger.Info(fmt.Sprintf(
			"Set-offline tx successfully broadcasted: %s",
			txHash.Hash.String(),
		))
	case <-txHashTimeout:
		guard.logger.Error("ERROR: Unable to broadcast set-offline tx during 5 minutes")
		return
	}

	// Wait until tx is confirmed and execution result is available
	var tx *ctypes.ResultTx
	txTimeout := time.After(10 * time.Minute)
	select {
	case tx = <-chanTxResult:
		guard.logger.Info(fmt.Sprintf(
			"Set-offline tx confirmed: %s", tx.TxResult.String(),
		))
	case <-txTimeout:
		guard.logger.Error("ERROR: Unable to wait for set-offline tx confirmation during 10 minutes")
		return
	}

	// Check if tx was successed and confirmed by the network
	if tx.TxResult.IsErr() {
		guard.logger.Error(fmt.Sprintf(
			"ERROR: Set-offline tx was failed and is not confirmed by the network: %s",
			tx.TxResult.String(),
		))
		return
	}

	// Wait until everything is done
	wg.Wait()

	// Log success
	guard.logger.Info("Validator successfully set offline and hopefully prevented stake lost!")

	return
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
	sb.WriteString("Watchers:")
	for i, w := range guard.watchers {
		endpointStr := fmt.Sprintf(fmt.Sprintf("%%-%ds", endpointMaxLength), w.endpoint)
		var statusStr string
		if w.IsRunning() {
			statusStr = fmt.Sprintf("%-14s", "connected")
		} else if w.connectingTime.IsZero() {
			statusStr = fmt.Sprintf("%-14s", "not connected")
		} else {
			reconnectingTime := w.connectingTime.Add(time.Duration(w.config.NewBlockTimeout) * time.Second)
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
		sb.WriteString("\n")
		sb.WriteString(fmt.Sprintf(" %2d.  %s", i+1, endpointStr))
		sb.WriteString(fmt.Sprintf("  |  state: %s", statusStr))
		if w.latestBlock > 0 && !w.latestBlockTime.IsZero() {
			sb.WriteString(fmt.Sprintf("  |  block: %d%s", w.latestBlock, ageStr))
		}
	}
	guard.logger.Info(sb.String())
}
