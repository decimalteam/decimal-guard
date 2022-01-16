package guard

import "time"

// eventBase is a base event object containing common for all events fields.
type eventBase struct {
	endpoint        string    // connected node's endpoint
	network         string    // connected node's network string identity
	latestBlock     int64     // number of the latest block in the connected node chain
	latestBlockTime time.Time // timestamp of the latest block in the connected node chain
}

// eventNewBlock is emitted by a watcher when new block is added to the chain.
type eventNewBlock struct {
	eventBase
	newBlock     int64 // number of new block
	missedBlocks int   // count of missed to sign blocks detected in the watching blocks window
}

// eventNoBlock
type eventNoBlock struct {
	eventBase
}
