package guard

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/tendermint/tendermint/libs/service"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	ttypes "github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/libs/log"
	client "github.com/tendermint/tendermint/rpc/client/http"
	ctypes "github.com/tendermint/tendermint/rpc/core/types"
	"github.com/tendermint/tendermint/types"
)

// Watcher is an object implementing necessary validator watcher functions.
type Watcher struct {
	config   Config
	endpoint string

	client      *client.HTTP
	connectedAt time.Time

	status          *ctypes.ResultStatus
	network         string
	latestBlock     int64
	latestBlockTime time.Time

	missedBlocks        []bool
	signatureExpected   bool
	validatorsRetrieved bool

	chanEventNewBlock chan<- eventNewBlock
	chanEventNoBlock  chan<- eventNoBlock

	logger    log.Logger
	waitGroup sync.WaitGroup
	mtx       sync.Mutex
}

// NewWatcher creates new Watcher instance.
func NewWatcher(
	config Config,
	endpoint string,
	chanEventNewBlock chan<- eventNewBlock,
	chanEventNoBlock chan<- eventNoBlock,
) (*Watcher, error) {
	c, err := NewTendermintHTTPClient(endpoint)
	if err != nil {
		return nil, err
	}

	return &Watcher{
		config:            config,
		endpoint:          endpoint,
		client:            c,
		missedBlocks:      make([]bool, config.MissedBlocksWindow),
		chanEventNewBlock: chanEventNewBlock,
		chanEventNoBlock:  chanEventNoBlock,
		logger:            log.NewTMLogger(log.NewSyncWriter(os.Stdout)),
		mtx:               sync.Mutex{},
	}, nil
}

// Start connects validator watcher to the node and starts listening to blocks.
func (w *Watcher) Start() (err error) {
	if w.IsRunning() {
		return
	}

	ctx := context.Background()
	subscriber := "watcher"
	capacity := 1_000_000

	// Start Tendermint HTTP client
	err = w.client.Start()
	if err != nil {
		return
	}

	w.logger.Info(fmt.Sprintf("[%s] Successfully connected to the node", w.endpoint))
	w.connectedAt = time.Now()

	defer func() {
		err := w.Stop()
		if err != nil {
			w.logger.Error(err.Error())
			return
		}
	}()

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
			w.chanEventNoBlock <- w.onNoBlock()
		case <-w.client.Quit():
			return
		}
	}
}

// Restart resets http client of validator and start it again.
func (w *Watcher) Restart() error {
	w.mtx.Lock()
	if w.connectedAt.IsZero() {
		w.mtx.Unlock()
		return nil
	}
	w.connectedAt = time.Time{}
	w.mtx.Unlock()

	// It is necessary to wait if the client has started to close, but has not yet closed
	select {
	case <-w.client.Quit():
	case <-time.After(time.Second):
	}

	// Close http connection with tendermint if it is not closed yet
	err := w.Stop()
	switch err {
	case service.ErrAlreadyStopped, nil:
		err = w.client.Reset()
		if err != nil {
			return err
		}
	case service.ErrNotStarted:
	default:
		return err
	}

	// w.client.Reset worked for github.com/tendermint/tendermint v0.33.3, but it doesn't work for v0.33.9
	// Therefore, to start the tendermint http client, we create a new one
	// TODO when upgrading to the next version of tendermint, restart through w.client.Reset again
	w.client, err = NewTendermintHTTPClient(w.endpoint)
	if err != nil {
		return err
	}

	go func(w *Watcher) {
		w.logger.Info(fmt.Sprintf("[%s] Reconnecting to the node...", w.endpoint))
		err = w.Start()
		if err != nil {
			w.logger.Error(fmt.Sprintf(
				"[%s] ERROR: Failed to connect to the node:: [%s]",
				w.endpoint,
				err,
			))
		}
	}(w)

	return nil
}

// Stop closes existing connection to the node.
func (w *Watcher) Stop() error {
	err := w.client.Stop()
	if err != nil {
		return err
	}

	w.validatorsRetrieved = false

	w.logger.Info(fmt.Sprintf("[%s] Disconnected from the node", w.endpoint))

	return nil
}

// IsRunning returns true if the watcher connected to the node.
func (w *Watcher) IsRunning() bool {
	return w.client.IsRunning()
}

////////////////////////////////////////////////////////////////////////////////
// Setting validator offline
////////////////////////////////////////////////////////////////////////////////

// broadcastSetOfflineTx broadcasts `validator/set_offline` transaction from validator operator account.
func (w *Watcher) broadcastSetOfflineTx() (*ctypes.ResultBroadcastTx, error) {
	data, err := hex.DecodeString(w.config.SetOfflineTx)
	if err != nil {
		return nil, err
	}

	return w.client.BroadcastTxSync(data)
}

// confirmSetOfflineTx wait confirmation `validator/set_offline` transaction from validator operator account.
func (w *Watcher) confirmSetOfflineTx(
	ctx context.Context,
	broadcastTx *ctypes.ResultBroadcastTx,
) (*ctypes.ResultTx, error) {
	queryTx := fmt.Sprintf("tm.event = 'Tx' AND tx.hash = '%s'", broadcastTx.Hash.String())

	chanTmEventTx, err := w.client.Subscribe(ctx, w.endpoint, queryTx)
	if err != nil {
		return nil, err
	}

	// Wait until tx is committed
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-chanTmEventTx:
	}

	var resultTx *ctypes.ResultTx
	for ctx.Err() == nil {
		resultTx, err = w.client.Tx(broadcastTx.Hash.Bytes(), false)
		if err == nil {
			break
		}

		w.logger.Info(fmt.Sprintf("[%s] WARNING: Unable to retrieve set-offline tx info: %s. Retry...", w.endpoint, err))
		time.Sleep(time.Second)
	}

	return resultTx, ctx.Err()
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
				w.logger.Info(fmt.Sprintf("Signature length = %d", len(s.Signature)))
				signed = len(s.Signature) > 0
				break
			}
		}
	}

	// Check software_upgrade TX and set grace period
	if w.config.EnableGracePeriod {
		w.checkSoftwareUpgradeTX(event, &signed)
	}

	// Update missed blocks container
	w.mtx.Lock()
	w.missedBlocks[int(w.latestBlock)%w.config.MissedBlocksWindow] = !signed
	w.mtx.Unlock()

	// Emit new block event to the guard
	w.chanEventNewBlock <- w.onNewBlock(event.Block.Height, w.countMissedBlocks())

	return
}

// Get value from event by key.
func getValueInEvent(event ttypes.Event, key string) (value string, ok bool) {
	for _, attr := range event.Attributes {
		if key == string(attr.Key) {
			value = string(attr.Value)
			ok = true
		}
	}
	return
}

// Parse block and find transaction "software_upgrade".
// If transaction exists then saved param "upgrade_height" into file "guard.update_block.json"
// and set grace period = [update_block ; update_block+24hours].
func (w *Watcher) checkSoftwareUpgradeTX(event types.EventDataNewBlock, signed *bool) {
	for _, tx := range event.Block.Txs {
		resultTx, err := w.getTxInfo(tx, 0)
		if err != nil {
			w.logger.Error(fmt.Sprintf("Unable to get tx in block %d by hash: %s", event.Block.Height, err))
			break
		}

		actionUpgradeExist := false

		for i, event := range resultTx.TxResult.Events {
			if event.Type != sdk.EventTypeMessage {
				continue
			}

			if actionUpgradeExist {
				w.logger.Info(fmt.Sprintf("Check upgrade_height attribute in tx event"))

				// "software_upgrade" -> ntypes.AttributeKeyUpgradeHeight
				height, ok := getValueInEvent(event, "upgrade_height")
				if !ok {
					w.logger.
						With("height", fmt.Sprintf("%v", height)).
						Error(fmt.Sprintf("Unable to get value from upgrade_height attribute"))
					continue
				}

				w.logger.Info(fmt.Sprintf("upgrade_height attribute with value %s detected", height))

				res, err := strconv.ParseInt(height, 10, 64)
				if err != nil {
					w.logger.
						With("height", fmt.Sprintf("%v", height)).
						Error(fmt.Sprintf("Unable to parse height of upgrade_height attribute"))
					continue
				}

				w.logger.Info(fmt.Sprintf("Height value of upgrade_height attribute successful parsed %d", res))

				err = UpdateInfo.Push(res)
				if err != nil {
					panic(err)
				}

				w.logger.Info(fmt.Sprintf("upgrade_height stored successful"))

				break
			}

			val, ok := getValueInEvent(event, sdk.AttributeKeyAction)

			// "software_upgrade" -> ntypes.AttributeKeyUpgradeHeight
			if ok && val == "software_upgrade" {
				w.logger.
					With("attr index", fmt.Sprintf("%d", i)).
					With("attr len", fmt.Sprintf("%d", len(resultTx.TxResult.Events))).
					Info(fmt.Sprintf("Action attribute with software_upgrade value detected"))
				actionUpgradeExist = true
			}
		}
	}

	err := UpdateInfo.Load()
	if err != nil {
		panic(err)
	}
	gracePeriodEnd := UpdateInfo.UpdateBlock + int64(w.config.GracePeriodDuration)

	if UpdateInfo.UpdateBlock != -1 && event.Block.Height >= UpdateInfo.UpdateBlock && event.Block.Height <= gracePeriodEnd {
		*signed = true
	}
}

const maxAttempts = 5
const attemptWaitTime = 100 * time.Millisecond

// Tendermint may not find the transaction by hash, although it has already been inserted into the block
// In this case, we wait a bit and try to get it again
func (w *Watcher) getTxInfo(tx types.Tx, attempt int) (*ctypes.ResultTx, error) {
	resultTx, err := w.client.Tx(tx.Hash(), false)
	if err != nil {
		if attempt == maxAttempts {
			return nil, err
		}

		time.Sleep(attemptWaitTime)

		return w.getTxInfo(tx, attempt+1)
	}

	return resultTx, nil
}

// handleEventValidatorSetUpdates handles validator set updates events received from watchers.
func (w *Watcher) handleEventValidatorSetUpdates(result ctypes.ResultEvent) (err error) {
	event, ok := result.Data.(types.EventDataValidatorSetUpdates)
	if !ok {
		err = errors.New("unable to cast received event to struct types.EventDataValidatorSetUpdates")
		return
	}

	w.logger.Info(fmt.Sprintf("[%s] Received new validator set updates", w.endpoint))

	w.logger.Info(fmt.Sprintf("%v+", event.ValidatorUpdates))
	for _, validator := range event.ValidatorUpdates {
		if strings.EqualFold(validator.Address.String(), w.config.ValidatorAddress) {
			if validator.VotingPower == 0 {
				w.signatureExpected = false
			} else {
				w.signatureExpected = true
			}
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
func (w *Watcher) onNoBlock() eventNoBlock {
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
