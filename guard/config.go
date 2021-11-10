package guard

// Config is an object containing validator guard configuration.
type Config struct {
	NodesEndpoints      string `env:"NODES_ENDPOINTS" mandatory:"true" default:"tcp://localhost:26657"`
	MissedBlocksLimit   int    `env:"MISSED_BLOCKS_LIMIT" mandatory:"true" default:"8"`
	MissedBlocksWindow  int    `env:"MISSED_BLOCKS_WINDOW" mandatory:"true" default:"24"`
	NewBlockTimeout     int    `env:"NEW_BLOCK_TIMEOUT" mandatory:"true" default:"10"`
	ValidatorAddress    string `env:"VALIDATOR_ADDRESS" mandatory:"true"`
	SetOfflineTx        string `env:"SET_OFFLINE_TX" mandatory:"true"`
	EnableGracePeriod   bool   `env:"ENABLE_GRACE_PERIOD" mandatory:"true"`
	GracePeriodDuration int    `env:"GRACE_PERIOD_DURATION" mandatory:"true" default:"15840"`
}
