package watcher

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
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

type eventBase struct {
	endpoint       string
	lastKnownBlock int64
}

type eventMissedBlock struct {
	eventBase
	missedBlocks int
}

type eventNoBlock struct {
	eventBase
	lastBlock int64
}

// Watcher is an object implementing necessary validator watcher functions.
type Watcher struct {
	config               *Config
	endpoint             string
	client               *client.HTTP
	logger               log.Logger
	chanEventMissedBlock chan<- eventMissedBlock
	chanEventNoBlock     chan<- eventNoBlock
	chanDisconnect       chan error
	waitGroup            sync.WaitGroup
}

// NewWatcher creates new Watcher instance.
func NewWatcher(
	config *Config,
	endpoint string,
	chanEventMissedBlock chan<- eventMissedBlock,
	chanEventNoBlock chan<- eventNoBlock,
) (*Watcher, error) {
	client, err := client.NewHTTP(endpoint, "/websocket")
	if err != nil {
		return nil, err
	}
	watcher := &Watcher{
		config:               config,
		endpoint:             endpoint,
		client:               client,
		chanEventMissedBlock: chanEventMissedBlock,
		chanEventNoBlock:     chanEventNoBlock,
		chanDisconnect:       make(chan error, 1),
		logger:               log.NewTMLogger(log.NewSyncWriter(os.Stdout)),
	}
	return watcher, nil
}

// Start connects validator watcher to the node and starts listening to blocks.
func (w *Watcher) Start() (err error) {
	if w.client.IsRunning() {
		return
	}

	ctx := context.Background()
	subscriber := "watcher"
	capacity := 1000000

	fmt.Printf("[%s] Starting...\n", w.endpoint)

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

	// Lock the wait group
	w.waitGroup.Add(1)
	defer w.waitGroup.Done()

	// Main loop
	for {
		select {
		case result := <-chanBlocks:
			event, ok := result.Data.(types.EventDataNewBlock)
			if !ok {
				err = errors.New("unable to cast received event to struct types.EventDataNewBlock")
				return
			}
			w.logger.Info(fmt.Sprintf("[%s] Received new block #%d", w.endpoint, event.Block.Height))
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
				missedBlocksCountPrev := countMissedBlocks()
				missedBlocks[i] = !signed
				missedBlocksCount := countMissedBlocks()
				if missedBlocksCountPrev != missedBlocksCount {
					w.logger.Info(fmt.Sprintf(
						"[%s] Missed to sign %d blocks of last %d blocks",
						w.endpoint, missedBlocksCount, w.config.MissedBlocksWindow,
					))
					blockchainInfo, _ := w.client.BlockchainInfo(0, 0)
					lastKnownBlock := int64(0)
					if blockchainInfo != nil {
						lastKnownBlock = blockchainInfo.LastHeight
					}
					w.chanEventMissedBlock <- eventMissedBlock{
						eventBase: eventBase{
							endpoint:       w.endpoint,
							lastKnownBlock: lastKnownBlock,
						},
						missedBlocks: missedBlocksCount,
					}
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
			w.logger.Info("[%s] Received new validator set updates", w.endpoint)
		case <-w.chanDisconnect:
			return
		case <-timeout:
			blockchainInfo, _ := w.client.BlockchainInfo(0, 0)
			lastKnownBlock := int64(0)
			if blockchainInfo != nil {
				lastKnownBlock = blockchainInfo.LastHeight
			}
			w.chanEventNoBlock <- eventNoBlock{
				eventBase: eventBase{
					endpoint:       w.endpoint,
					lastKnownBlock: lastKnownBlock,
				},
				lastBlock: lastKnownBlock,
			}
			return
		}
	}
}

// Stop closes existing connection to the node.
func (w *Watcher) Stop(err error) {
	if w.chanDisconnect == nil {
		return
	}
	w.chanDisconnect <- err
	w.waitGroup.Wait()
	close(w.chanDisconnect)
	w.chanDisconnect = nil
}

// setOffline broadcasts `validator/set_offline` transaction from validator operator account.
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
