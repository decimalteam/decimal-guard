package main

import (
	"fmt"

	"bitbucket.org/decimalteam/decimal-guard/utils"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/auth"
	"github.com/tendermint/tendermint/libs/bech32"

	decapi "bitbucket.org/decimalteam/decimal-go-sdk/api"
	"bitbucket.org/decimalteam/decimal-go-sdk/wallet"
)

// Config is an object containing validator guard configuration.
type Config struct {
	APIURL         string `env:"API_URL" mandatory:"true"`
	BaseCoinSymbol string `env:"BASE_COIN_SYMBOL" mandatory:"true"` // TODO: Retrieve from the chain?
	Mnemonic       string `env:"MNEMONIC" mandatory:"true"`
}

func main() {

	// Load configuration
	config := &Config{}
	err := utils.LoadConfig(config)
	if err != nil {
		panic(err)
	}

	// Create Decimal API instance
	api := decapi.NewAPI(config.APIURL)

	// Create account from mnemonic
	account, err := wallet.NewAccountFromMnemonicWords(config.Mnemonic, "")
	if err != nil {
		panic(err)
	}

	// Request chain ID
	if chainID, err := api.ChainID(); err == nil {
		account = account.WithChainID(chainID)
		fmt.Printf("\nChain ID:\n%s\n", chainID)
	} else {
		panic(err)
	}

	// Request account number and sequence and update with received values
	if an, s, err := api.AccountNumberAndSequence(account.Address()); err == nil {
		account = account.WithAccountNumber(an).WithSequence(s)
		fmt.Printf("\nAccount:\n%s (number: %d, sequence: %d)\n", account.Address(), an, s)
	} else {
		panic(err)
	}

	// Prepare message arguments
	_, senderData, err := bech32.DecodeAndConvert(account.Address())
	if err != nil {
		panic(err)
	}

	// Prepare message
	msg := decapi.NewMsgSetOffline(senderData)

	// Prepare transaction arguments
	msgs := []sdk.Msg{msg}
	feeCoins := sdk.NewCoins(sdk.NewCoin(config.BaseCoinSymbol, sdk.NewInt(0)))
	memo := "Decimal Guard triggerred"

	// Declare transaction variable
	var tx auth.StdTx

	// Adjust gas until it is equal to gasEstimated
	for gas, gasEstimated := uint64(16*1024), uint64(0); gas != gasEstimated; {
		if gasEstimated != 0 {
			gas = gasEstimated
		}

		// Create and sign transaction
		fee := auth.NewStdFee(gas, feeCoins)
		tx = account.CreateTransaction(msgs, fee, memo)
		tx, err = account.SignTransaction(tx)
		if err != nil {
			panic(err)
		}

		// Estimate and adjust amount of gas wanted for the transaction
		gasEstimated, err = api.EstimateTransactionGasWanted(tx)
		if err != nil {
			panic(err)
		}
	}

	// Marshal generated transaction to binary
	txData, err := api.Codec().MarshalBinaryLengthPrefixed(tx)
	if err != nil {
		panic(err)
	}

	// Print validator operator address to a terminal
	sender, err := bech32.ConvertAndEncode("dxvaloper", senderData)
	if err != nil {
		panic(err)
	}
	fmt.Printf("\nOperator address:\n%s\n", sender)

	// Print generated transaction to a terminal
	fmt.Printf("\nTransaction (hex):\n%x\n", txData)
}
