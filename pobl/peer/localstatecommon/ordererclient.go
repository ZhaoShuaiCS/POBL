package localstatecommon



import (
	"context"
	"crypto/tls"
	"fmt"
	"github.com/hyperledger/fabric/common/flogging"

	"github.com/hyperledger/fabric/core/comm"
	ab "github.com/hyperledger/fabric/protos/orderer"
	"github.com/pkg/errors"
)
var localsclogger = flogging.MustGetLogger("localstatecommon")
// OrdererClient represents a client for communicating with an ordering
// service
type OrdererClient struct {
	commonClient
}

// NewOrdererClientFromEnv creates an instance of an OrdererClient from the
// global Viper instance
func NewOrdererClientFromEnv() (*OrdererClient, error) {
	address, override, clientConfig, err := configFromEnv("orderer")
	if err != nil {
		return nil, errors.WithMessage(err, "failed to load config for OrdererClient")
	}
	gClient, err := comm.NewGRPCClient(clientConfig)
	if err != nil {
		return nil, errors.WithMessage(err, "failed to create OrdererClient from config")
	}
	oClient := &OrdererClient{
		commonClient: commonClient{
			GRPCClient: gClient,
			address:    address,
			sn:         override}}
	localsclogger.Infof("zs: get order address:%s sn:%s",address,override)
	return oClient, nil
}

// Broadcast returns a broadcast client for the AtomicBroadcast service
func (oc *OrdererClient) Broadcast() (ab.AtomicBroadcast_BroadcastClient, error) {
	conn, err := oc.commonClient.NewConnection(oc.address, oc.sn)
	if err != nil {
		return nil, errors.WithMessage(err, fmt.Sprintf("orderer client failed to connect to %s", oc.address))
	}
	// TODO: check to see if we should actually handle error before returning
	return ab.NewAtomicBroadcastClient(conn).Broadcast(context.TODO())
}
func (oc *OrdererClient) GetAddress() string {
	return oc.address
}

// Deliver returns a deliver client for the AtomicBroadcast service
func (oc *OrdererClient) Deliver() (ab.AtomicBroadcast_DeliverClient, error) {
	conn, err := oc.commonClient.NewConnection(oc.address, oc.sn)
	if err != nil {
		return nil, errors.WithMessage(err, fmt.Sprintf("orderer client failed to connect to %s", oc.address))
	}
	// TODO: check to see if we should actually handle error before returning
	return ab.NewAtomicBroadcastClient(conn).Deliver(context.TODO())

}

// Certificate returns the TLS client certificate (if available)
func (oc *OrdererClient) Certificate() tls.Certificate {
	return oc.commonClient.Certificate()
}
