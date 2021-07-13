package guard

import (
	decapi "bitbucket.org/decimalteam/decimal-go-sdk/api"
	"encoding/hex"
	"github.com/cosmos/cosmos-sdk/x/auth"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestEncodeTx(t *testing.T) {
	data, err := hex.DecodeString("ab01282816a90a1a51f5833b0a144740839c1ecc67c565c5772e9654e39875462290120310be031a6a0a26eb5ae9872102242b37f2a60f4e0998ed23b9d4927f6a8a0c0191eb41b1db4a3323c2e782ba7712400befa6c32538ac807f85244b9846ddffd4138654e66a76d42e2c8391fedee03f2460b5f8ce581fc1c1b4b50dbbd6ed3fdec6c2797bd43081e99a65f29cee30482218446563696d616c2047756172642074726967676572726564")
	require.NoError(t, err)

	api := decapi.NewAPI("http://localhost:26657")

	var tx auth.StdTx
	api.Codec().MustUnmarshalBinaryLengthPrefixed(data, &tx)

	t.Log(tx)

	msg := api.Codec().MustMarshalJSON(tx)
	t.Log(string(msg))
}
