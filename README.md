# Decimal Guard

Decimal Guard is a tool helping to monitor validator node and set it offline if node stops to sign blocks by any reason.

### Check version

Make sure you are using the latest version of Decimal Guard (v1.1.5)

To check `guard` version use following commands:

```bash
(cd ./cmd/guard && go run guard.go -v)
```

## Generating set-offline transaction

Before starting Decimal Guard it is necessary create and sign offline transaction with message `validator/set_offline` which will be used to turn off validator when it does not sign blocks.

### Generator configuration

To configure `gentx` tool you should create file `.env` at directory `cmd/gentx`. Example of the configuration:

```bash
API_URL="https://testnet-gate.decimalchain.com/api"
BASE_COIN_SYMBOL="tdel"
MNEMONIC="bulb raw claw magnet romance jaguar life cluster solve random laptop salmon pottery subject country aware actual hope wedding hawk amused cage secret network"
```

### Generator usage

To run `gentx` tool use following commands:

```bash
(cd ./cmd/gentx && go run gentx.go)
```

If everything is OK in terminal your will see output like this:

```txt
Chain ID:
decimal-testnet-07-28-18-30

Account:
dx16rr3cvdgj8jsywhx8lfteunn9uz0xg2c7ua9nl (number: 2, sequence: 1)

Operator address:
dxvaloper16rr3cvdgj8jsywhx8lfteunn9uz0xg2czw6gx5

Transaction (hex):
ab01282816a90a1a51f5833b0a14d0c71c31a891e5023ae63fd2bcf2732f04f32158120310be031a6a0a26eb5ae987210279f7e074d08a23e2fc7b7fd9e49a0d6570a28bf6c9cb988e92f678c32935097412407979e0cc483f241e48ed3c371d9d668a5b978fb474afc5fea5803c89bd2a2dac3db15eb84fef1fce25e783e279a33bac7b96bbe6786c9608d52c69baecacf9d02218446563696d616c2047756172642074726967676572726564
```

Please, confirm that chain ID, account and operator addresses are correct. The last line is a generated transaction in hex format. You should use it as value for `SET_OFFLINE_TX` environment variable (by specifying in `cmd/guard/.env`) to run Decimal Guard.

## Guard a validator node

Before starting Decimal Guard it is necessary to configure it's parameters.

### Guard configuration

To configure `guard` tool you should create file `.env` at directory `cmd/guard`. Example of the configuration:

```bash
NODES_ENDPOINTS="tcp://localhost:26657"
MISSED_BLOCKS_LIMIT=8
MISSED_BLOCKS_WINDOW=24
NEW_BLOCK_TIMEOUT=10
VALIDATOR_ADDRESS="1A42FDF9FC98931A4BB59EF571D61BB70417657D"
SET_OFFLINE_TX="ab01282816a90a1a51f5833b0a14d0c71c31a891e5023ae63fd2bcf2732f04f32158120310be031a6a0a26eb5ae987210279f7e074d08a23e2fc7b7fd9e49a0d6570a28bf6c9cb988e92f678c32935097412407979e0cc483f241e48ed3c371d9d668a5b978fb474afc5fea5803c89bd2a2dac3db15eb84fef1fce25e783e279a33bac7b96bbe6786c9608d52c69baecacf9d02218446563696d616c2047756172642074726967676572726564"
ENABLE_GRACE_PERIOD=true
GRACE_PERIOD_DURATION=15840
```

Where:

- `NODES_ENDPOINTS` - list of Decimal Node RPC endpoints which should be used to listen new blocks (can be specified several endpoints separated by `,`)
- `MISSED_BLOCKS_LIMIT` and `MISSED_BLOCKS_WINDOW` - when at least `MISSED_BLOCKS_LIMIT` blocks of last `MISSED_BLOCKS_WINDOW` blocks are missed to sign by monitoring validator `set_offline` transaction will be send to all connected nodes to turn of validator
- `NEW_BLOCK_TIMEOUT` - timeout of receiving new block in seconds (if no new blocks are received during this duration then assumed node is disconnected)
- `VALIDATOR_ADDRESS` - validator address in hex format which should be monitored by the guard. Validator address can be found in file `$HOME/.decimal/daemon/config/priv_validator_key.json`
- `SET_OFFLINE_TX` - signed tx (ready to broadcast) in hex format which will be used to turn off validator when too many blocks are missed to sign
- `ENABLE_GRACE_PERIOD` - checked tx "software_upgrade" and set grace period = \[update_block ; update_block+GRACE_PERIOD_DURATION\]
- `GRACE_PERIOD_DURATION` - duration of the grace period in blocks. At the moment, the duration of the grace period in decimal-go-node is 15840 blocks (~ 24 hours). It is recommended to set the value for the guard the same as for the node - 15840.

### Guard usage

To run `guard` tool use following commands:

```bash
(cd ./cmd/guard && go run guard.go)
```
