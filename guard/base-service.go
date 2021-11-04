package guard

import "github.com/tendermint/tendermint/rpc/client"

type BaseService struct {
	*client.WSEvents
}

func (fs BaseService) OnReset() error {
	return nil
}
