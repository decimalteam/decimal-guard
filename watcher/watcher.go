package watcher

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/tendermint/tendermint/libs/log"
	"github.com/tendermint/tendermint/rpc/client"
	"github.com/tendermint/tendermint/types"
)

// Tendermint websocket subscription queries.
const (
	QueryNewBlock            = "tm.event = 'NewBlock'"
	QueryValidatorSetUpdates = "tm.event = 'ValidatorSetUpdates'"
)

// Config is an object containing validator watcher configuration.
type Config struct {
	NodeEndpoint       string `env:"NODE_ENDPOINT" mandatory:"true" default:"tcp://localhost:26657"`
	MissedBlocksLimit  int    `env:"MISSED_BLOCKS_LIMIT" mandatory:"true" default:"8"`
	MissedBlocksWindow int    `env:"MISSED_BLOCKS_WINDOW" mandatory:"true" default:"24"`
	NewBlockTimeout    int    `env:"NEW_BLOCK_TIMEOUT" mandatory:"true" default:"0"`
	ValidatorAddress   string `env:"VALIDATOR_ADDRESS" mandatory:"true"`
	SetOfflineTx       string `env:"SET_OFFLINE_TX" mandatory:"true"`
}

// Watcher is an object implementing necessary validator watcher functions.
type Watcher struct {
	config *Config
	client *client.HTTP
	logger log.Logger
}

// NewWatcher creates new Watcher instance.
func NewWatcher(config *Config) (*Watcher, error) {
	client, err := client.NewHTTP(config.NodeEndpoint, "/websocket")
	if err != nil {
		return nil, err
	}
	watcher := &Watcher{
		config: config,
		client: client,
		logger: log.NewTMLogger(log.NewSyncWriter(os.Stdout)),
	}
	return watcher, nil
}

// Run starts watching for the validator health and stops only when application is closed.
func (w *Watcher) Run() (err error) {
	ctx := context.Background()
	subscriber := "watcher"
	capacity := 1000000

	// Subscribe to interrupt signal to normally exit on Ctrl+C
	chanInterrupt := make(chan os.Signal, 1)
	signal.Notify(chanInterrupt, os.Interrupt, os.Kill)

	// Start Tendermint HTTP client
	err = w.client.Start()
	if err != nil {
		return
	}
	defer w.client.Stop()

	// Subscribe to new block events
	chanBlocks, err := w.client.Subscribe(ctx, subscriber, QueryNewBlock, capacity)
	if err != nil {
		return
	}

	// Subscribe to validator set updates
	chanValidatorSetUpdates, err := w.client.Subscribe(ctx, subscriber, QueryValidatorSetUpdates, capacity)
	if err != nil {
		return
	}

	// Prepare array of flags helping to check missed blocks limit
	missedBlocks := make([]bool, w.config.MissedBlocksWindow)
	countMissedBlocks := func() int {
		result := 0
		for _, missed := range missedBlocks {
			if missed {
				result++
			}
		}
		return result
	}

	// Prepare timeout channel if necessary
	var timeout <-chan time.Time
	if w.config.NewBlockTimeout > 0 {
		timeout = time.After(time.Duration(w.config.NewBlockTimeout) * time.Second)
	}

	// Main loop
	for {
		select {
		case result := <-chanBlocks:
			event, ok := result.Data.(types.EventDataNewBlock)
			if !ok {
				err = errors.New("unable to cast received event to struct types.EventDataNewBlock")
				return
			}
			w.logger.Info(fmt.Sprintf("Received new block #%d", event.Block.Height))
			validators, e := w.client.Validators(&event.Block.LastCommit.Height, 0, 1000)
			if e != nil {
				err = e
				return
			}
			signatureExpected := false
			for _, v := range validators.Validators {
				if strings.EqualFold(v.Address.String(), w.config.ValidatorAddress) {
					signatureExpected = true
					break
				}
			}
			if signatureExpected {
				signed := false
				for _, s := range event.Block.LastCommit.Signatures {
					if strings.EqualFold(s.ValidatorAddress.String(), w.config.ValidatorAddress) {
						signed = len(s.Signature) > 0
						break
					}
				}
				i := int(event.Block.LastCommit.Height) % len(missedBlocks)
				missedBlocks[i] = !signed
				w.logger.Info(fmt.Sprintf(
					"Missed to sign %d blocks of last %d blocks",
					countMissedBlocks(), w.config.MissedBlocksWindow,
				))
				if !signed && countMissedBlocks() >= w.config.MissedBlocksLimit {
					w.logger.Info(fmt.Sprintf(
						"WARNING: There are too many blocks missed to sign (%d of last %d)",
						w.config.MissedBlocksLimit, w.config.MissedBlocksWindow,
					))
					err = w.setOffline()
					return
				}
			}
			if timeout != nil {
				timeout = time.After(time.Duration(w.config.NewBlockTimeout) * time.Second)
			}
		case result := <-chanValidatorSetUpdates:
			_, ok := result.Data.(types.EventDataValidatorSetUpdates)
			if !ok {
				err = errors.New("unable to cast received event to struct types.EventDataValidatorSetUpdates")
				return
			}
			w.logger.Info("Received new validator set updates")
		case <-timeout:
			w.logger.Info(fmt.Sprintf("WARNING: There are no new blocks during %d seconds", w.config.NewBlockTimeout))
			err = w.setOffline()
			return
		case v := <-chanInterrupt:
			switch v {
			case os.Interrupt:
				w.logger.Info("Interrupt signal received. Shutting down...")
			case os.Kill:
				w.logger.Info("Kill signal received. Shutting down...")
			}
			return
		}
	}
}

// setOffline creates, signs and broadcasts `validator/set_offline` transaction from validator operator account.
func (w *Watcher) setOffline() error {
	txData, err := hex.DecodeString(w.config.SetOfflineTx)
	if err != nil {
		return err
	}
	txResult, err := w.client.BroadcastTxCommit(txData)
	if err != nil {
		return err
	}
	if txResult.CheckTx.IsErr() {
		return fmt.Errorf("unable to set offline the validator: %s", txResult.CheckTx.GetLog())
	}
	if txResult.DeliverTx.IsErr() {
		return fmt.Errorf("unable to set offline the validator: %s", txResult.DeliverTx.GetLog())
	}
	return nil
}
