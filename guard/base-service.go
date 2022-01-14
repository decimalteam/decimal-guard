package guard

import (
	"github.com/tendermint/tendermint/libs/service"
	"github.com/tendermint/tendermint/rpc/client"
)

type BaseService struct {
	*client.WSEvents
}

func NewBaseService(name string, wse *client.WSEvents) service.BaseService {
	return *service.NewBaseService(nil, name, BaseService{
		wse,
	})
}

func (fs BaseService) OnReset() error {
	return nil
}
