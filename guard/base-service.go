package guard

import (
	"github.com/tendermint/tendermint/libs/service"
	client "github.com/tendermint/tendermint/rpc/client/http"
)

type BaseService struct {
	*client.WSEvents
}

func NewTendermintHTTPClient(endpoint string) (*client.HTTP, error) {
	// Create Tendermint c instance and connect it to the node
	c, err := client.New(endpoint, "/websocket")
	if err != nil {
		return nil, err
	}

	// It's necessary to override OnReset method to allow OnStart/OnStop to be called again and restart service
	// https://github.com/tendermint/tendermint/blob/master/libs/service/service.go#L66
	c.WSEvents.BaseService = NewBaseService("WSEvents", c.WSEvents)

	return c, err
}

func NewBaseService(name string, wse *client.WSEvents) service.BaseService {
	return *service.NewBaseService(nil, name, BaseService{
		wse,
	})
}

func (bs BaseService) OnReset() error {
	return nil
}
