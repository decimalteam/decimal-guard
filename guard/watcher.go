package guard

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
	ctypes "github.com/tendermint/tendermint/rpc/core/types"
	"github.com/tendermint/tendermint/types"
)

// Watcher is an object implementing necessary validator watcher functions.
type Watcher struct {
	config   Config
	endpoint string

	client         *client.HTTP
	connectingTime time.Time

	status          *ctypes.ResultStatus
	network         string
	latestBlock     int64
	latestBlockTime time.Time

	missedBlocks        []bool
	signatureExpected   bool
	validatorsRetrieved bool

	chanEventStatus   chan<- eventStatus
	chanEventNewBlock chan<- eventNewBlock
	chanEventNoBlock  chan<- eventNoBlock
	chanDisconnect    chan error

	logger    log.Logger
	waitGroup sync.WaitGroup
	mtx       sync.Mutex
}

// NewWatcher creates new Watcher instance.
func NewWatcher(
	config Config,
	endpoint string,
	chanEventStatus chan<- eventStatus,
	chanEventNewBlock chan<- eventNewBlock,
	chanEventNoBlock chan<- eventNoBlock,
) (*Watcher, error) {
	// Create Tendermint c instance and connect it to the node
	c, err := client.NewHTTP(endpoint, "/websocket")
	if err != nil {
		return nil, err
	}
	return &Watcher{
		config:            config,
		endpoint:          endpoint,
		client:            c,
		missedBlocks:      make([]bool, config.MissedBlocksWindow),
		chanEventStatus:   chanEventStatus,
		chanEventNewBlock: chanEventNewBlock,
		chanEventNoBlock:  chanEventNoBlock,
		chanDisconnect:    make(chan error, 1),
		logger:            log.NewTMLogger(log.NewSyncWriter(os.Stdout)),
		mtx:               sync.Mutex{},
	}, nil
}

// Start connects validator watcher to the node and starts listening to blocks.
func (w *Watcher) Start() (err error) {
	if w.IsRunning() {
		return
	}
	if !w.connectingTime.IsZero() {
		reconnectingTime := w.connectingTime.Add(time.Duration(w.config.NewBlockTimeout) * time.Second)
		if reconnectingTime.After(time.Now()) {
			return
		}
	}
	w.connectingTime = time.Now()

	ctx := context.Background()
	subscriber := "watcher"
	capacity := 1_000_000

	// Logs
	w.logger.Info(fmt.Sprintf("[%s] Connecting to the node...", w.endpoint))

	// Lock the wait group
	w.waitGroup.Add(1)
	defer w.waitGroup.Done()

	// Start Tendermint HTTP client
	err = w.client.Start()
	if err != nil {
		return
	}
	defer w.client.Stop()

	// Retrieve blockchain info
	w.updateCommon()

	// Subscribe to new block events
	const queryNewBlock = "tm.event = 'NewBlock'"
	chanBlocks, err := w.client.Subscribe(ctx, subscriber, queryNewBlock, capacity)
	if err != nil {
		return
	}

	// Subscribe to validator set updates
	const queryValidatorSetUpdates = "tm.event = 'ValidatorSetUpdates'"
	chanValidatorSetUpdates, err := w.client.Subscribe(ctx, subscriber, queryValidatorSetUpdates, capacity)
	if err != nil {
		return
	}

	// Main loop
	for {
		select {
		case result := <-chanBlocks:
			// Handle received event
			err = w.handleEventNewBlock(result)
			if err != nil {
				return
			}
		case result := <-chanValidatorSetUpdates:
			// Handle received event
			err = w.handleEventValidatorSetUpdates(result)
			if err != nil {
				return
			}
		case <-time.After(time.Duration(w.config.NewBlockTimeout) * time.Second):
			// Force validators set retrieving on next new block when reconnected
			w.validatorsRetrieved = false
			// Emit no block event to the guard
			w.chanEventNoBlock <- w.onNoBlock(w.latestBlock)
		case <-w.chanDisconnect:
			return
		}
	}
}

// Stop closes existing connection to the node.
func (w *Watcher) Stop(err error) {
	if !w.IsRunning() {
		return
	}

	w.connectingTime = time.Time{}
	w.validatorsRetrieved = false

	w.chanDisconnect <- err
	w.waitGroup.Wait()

	// Logs
	w.logger.Info(fmt.Sprintf("[%s] Disconnected from the node", w.endpoint))
}

// IsRunning returns true if the watcher connected to the node.
func (w *Watcher) IsRunning() bool {
	return w.client.IsRunning()
}

////////////////////////////////////////////////////////////////////////////////
// Setting validator offline
////////////////////////////////////////////////////////////////////////////////

// broadcastSetOfflineTx broadcasts `validator/set_offline` transaction from validator operator account.
func (w *Watcher) broadcastSetOfflineTx(
	chanTxHash chan *ctypes.ResultBroadcastTx,
	chanTxResult chan *ctypes.ResultTx,
	chanStop chan interface{},
) (err error) {
	data, err := hex.DecodeString(w.config.SetOfflineTx)
	if err != nil {
		return err
	}
	var resultBroadcast *ctypes.ResultBroadcastTx
	var resultTx *ctypes.ResultTx
	// Broadcast transaction synchronously
	for {
		resultBroadcast, err = w.client.BroadcastTxSync(data)
		if err != nil {
			w.logger.Info(fmt.Sprintf("[%s] WARNING: Unable to broadcast set-offline tx: %s", w.endpoint, err))
			time.Sleep(time.Second)
			continue
		}
		chanTxHash <- resultBroadcast
		break
	}
	// Wait until transaction is done
	for {
		resultTx, err = w.client.Tx(resultBroadcast.Hash.Bytes(), false)
		if err != nil {
			w.logger.Info(fmt.Sprintf("[%s] WARNING: Unable to retrieve set-offline tx info: %s", w.endpoint, err))
			time.Sleep(time.Second)
			continue
		}
		chanTxResult <- resultTx
		break
	}
	return
}

////////////////////////////////////////////////////////////////////////////////
// Handling events from Tendermint client
////////////////////////////////////////////////////////////////////////////////

// handleEventNewBlock handles new block events received from watchers.
func (w *Watcher) handleEventNewBlock(result ctypes.ResultEvent) (err error) {
	event, ok := result.Data.(types.EventDataNewBlock)
	if !ok {
		err = errors.New("unable to cast received event to struct types.EventDataNewBlock")
		return
	}

	w.logger.Info(fmt.Sprintf("[%s] Received new block %d", w.endpoint, event.Block.Height))

	// Save latest received block
	w.latestBlock = event.Block.Height
	w.latestBlockTime = event.Block.Time

	if !w.validatorsRetrieved {

		w.logger.Info(fmt.Sprintf("[%s] Retrieving set of validators for block %d", w.endpoint, event.Block.Height))

		// Retrieve set of validators expected in the block
		validators, e := w.client.Validators(&event.Block.Height, 0, 1000)
		if e != nil {
			err = e
			return
		}

		// Check if it is expected that block is signed by guarded validator's node
		w.signatureExpected = false
		for _, v := range validators.Validators {
			if strings.EqualFold(v.Address.String(), w.config.ValidatorAddress) {
				w.signatureExpected = true
				break
			}
		}

		w.validatorsRetrieved = true
	}

	// Check if the block is signed by guarded validator's node
	signed := true
	if w.signatureExpected {
		signed = false
		for _, s := range event.Block.LastCommit.Signatures {
			if strings.EqualFold(s.ValidatorAddress.String(), w.config.ValidatorAddress) {
				signed = len(s.Signature) > 0
				break
			}
		}
	}

	// Update missed blocks container
	w.mtx.Lock()
	w.missedBlocks[int(w.latestBlock)%len(w.missedBlocks)] = !signed
	w.mtx.Unlock()

	// Emit new block event to the guard
	w.chanEventNewBlock <- w.onNewBlock(event.Block.Height, w.countMissedBlocks())

	return
}

// handleEventValidatorSetUpdates handles validator set updates events received from watchers.
func (w *Watcher) handleEventValidatorSetUpdates(result ctypes.ResultEvent) (err error) {
	event, ok := result.Data.(types.EventDataValidatorSetUpdates)
	if !ok {
		err = errors.New("unable to cast received event to struct types.EventDataValidatorSetUpdates")
		return
	}

	w.logger.Info(fmt.Sprintf("[%s] Received new validator set updates", w.endpoint))

	w.signatureExpected = false
	for _, validator := range event.ValidatorUpdates {
		if strings.EqualFold(hex.EncodeToString(validator.Address.Bytes()), w.config.ValidatorAddress) {
			w.signatureExpected = true
			break
		}

	}

	return
}

////////////////////////////////////////////////////////////////////////////////
// Internal functions
////////////////////////////////////////////////////////////////////////////////

// onNewBlock creates and returns new block event.
func (w *Watcher) onNewBlock(newBlock int64, missedBlocks int) eventNewBlock {
	return eventNewBlock{
		eventBase: eventBase{
			endpoint:        w.endpoint,
			network:         w.network,
			latestBlock:     w.latestBlock,
			latestBlockTime: w.latestBlockTime,
		},
		newBlock:     newBlock,
		missedBlocks: missedBlocks,
	}
}

// onNoBlock creates and returns no block event.
func (w *Watcher) onNoBlock(latestBlock int64) eventNoBlock {
	return eventNoBlock{
		eventBase: eventBase{
			endpoint:        w.endpoint,
			network:         w.network,
			latestBlock:     w.latestBlock,
			latestBlockTime: w.latestBlockTime,
		},
	}
}

// updateCommon requests blockchain info from the connected node synchronously
// and updates some common blockchain information.
func (w *Watcher) updateCommon() {
	if status, err := w.client.Status(); err == nil && status != nil {
		w.status = status
		w.network = status.NodeInfo.Network
		w.latestBlock = status.SyncInfo.LatestBlockHeight
		w.latestBlockTime = status.SyncInfo.LatestBlockTime
	}
}

// countMissedBlocks counts how many blocks are missed to sign in the watching blocks windows.
func (w *Watcher) countMissedBlocks() int {
	result := 0
	w.mtx.Lock()
	for _, missed := range w.missedBlocks {
		if missed {
			result++
		}
	}
	w.mtx.Unlock()
	return result
}
