package localdeliverservice
import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hyperledger/fabric/core/comm"
	"github.com/hyperledger/fabric/core/localdeliverservice/blocksprovider"
	"github.com/hyperledger/fabric/protos/common"
	"github.com/hyperledger/fabric/protos/orderer"
	"google.golang.org/grpc"
)

// broadcastSetup is a function that is called by the broadcastClient immediately after each
// successful connection to the ordering service
type broadcastSetup func(blocksprovider.BlocksDeliverer) error

// retryPolicy receives as parameters the number of times the attempt has failed
// and a duration that specifies the total elapsed time passed since the first attempt.
// If further attempts should be made, it returns:
// 	- a time duration after which the next attempt would be made, true
// Else, a zero duration, false
type retryPolicy func(attemptNum int, elapsedTime time.Duration) (time.Duration, bool)

// clientFactory creates a gRPC broadcast client out of a ClientConn
type clientFactory func(*grpc.ClientConn) orderer.AtomicBroadcastClient

//type broadcastClient struct {
//	stopFlag     int32
//	stopChan     chan struct{}
//	createClient clientFactory
//	shouldRetry  retryPolicy
//	onConnect    broadcastSetup
//	prod         comm.ConnectionProducer
//
//	mutex           sync.Mutex
//	blocksDeliverer blocksprovider.BlocksDeliverer
//	conn            *connection
//	endpoint        string
//}
//zs
type broadcastClient struct {
	stopFlag     int32
	stopChan     chan struct{}
	createClient clientFactory
	shouldRetry  retryPolicy
	onConnect    broadcastSetup
	prod         comm.ConnectionProducer

	mutex           sync.Mutex
	blocksDeliverer blocksprovider.BlocksDeliverer
	conn            *connection
	endpoint        string
	SendChan chan *blocksprovider.SendEnv
	NumSendTx    int64
	NunpushSendchan int64
}
//zs

// NewBroadcastClient returns a broadcastClient with the given params
//func NewBroadcastClient(prod comm.ConnectionProducer, clFactory clientFactory, onConnect broadcastSetup, bos retryPolicy) *broadcastClient {
//	return &broadcastClient{prod: prod, onConnect: onConnect, shouldRetry: bos, createClient: clFactory, stopChan: make(chan struct{}, 1)}
//}
//zs
func NewBroadcastClient(prod comm.ConnectionProducer, clFactory clientFactory, onConnect broadcastSetup, bos retryPolicy) *broadcastClient {
	return &broadcastClient{prod: prod, onConnect: onConnect, shouldRetry: bos, createClient: clFactory, stopChan: make(chan struct{}, 1), SendChan: make(chan *blocksprovider.SendEnv, 8000),
		NumSendTx: 0,NunpushSendchan: 0}
}
func (bc *broadcastClient) GetSendChan() chan *blocksprovider.SendEnv{
	return bc.SendChan
}
func (bc *broadcastClient) SetNumSendTx()int64{
	bc.NumSendTx++
	return bc.NumSendTx
}

//zs

// Recv receives a message from the ordering service
func (bc *broadcastClient) Recv() (*orderer.DeliverResponse, error) {
	logger.Infof("zs: local Recv()")
	o, err := bc.try(func() (interface{}, error) {
		if bc.shouldStop() {
			return nil, errors.New("closing")
		}
		return bc.tryReceive()
	})
	if err != nil {
		return nil, err
	}
	return o.(*orderer.DeliverResponse), nil
}

// Send sends a message to the ordering service
func (bc *broadcastClient) Send(msg *common.Envelope) error {
	logger.Infof("zs: local Send()")
	_, err := bc.try(func() (interface{}, error) {
		if bc.shouldStop() {
			return nil, errors.New("closing")
		}
		return bc.trySend(msg)
	})
	return err
}

func (bc *broadcastClient) trySend(msg *common.Envelope) (interface{}, error) {
	logger.Infof("zs: local trySend()")
	bc.mutex.Lock()
	stream := bc.blocksDeliverer
	bc.mutex.Unlock()
	if stream == nil {
		return nil, errors.New("client stream has been closed")
	}
	return nil, stream.Send(msg)
}

func (bc *broadcastClient) tryReceive() (*orderer.DeliverResponse, error) {
	logger.Infof("zs: local tryReceive()")
	bc.mutex.Lock()
	stream := bc.blocksDeliverer
	bc.mutex.Unlock()
	if stream == nil {
		return nil, errors.New("client stream has been closed")
	}
	return stream.Recv()
}

func (bc *broadcastClient) try(action func() (interface{}, error)) (interface{}, error) {
	logger.Infof("zs: local try")
	attempt := 0
	var totalRetryTime time.Duration
	var backoffDuration time.Duration
	retry := true
	resetAttemptCounter := func() {
		attempt = 0
		totalRetryTime = 0
	}
	for retry && !bc.shouldStop() {
		resp, err := bc.doAction(action, resetAttemptCounter)
		if err != nil {
			attempt++
			backoffDuration, retry = bc.shouldRetry(attempt, totalRetryTime)
			if !retry {
				logger.Warning("Got error:", err, "at", attempt, "attempt. Ceasing to retry")
				break
			}
			logger.Warning("Got error:", err, ", at", attempt, "attempt. Retrying in", backoffDuration)
			totalRetryTime += backoffDuration
			bc.sleep(backoffDuration)
			continue
		}
		return resp, nil
	}
	if bc.shouldStop() {
		return nil, errors.New("client is closing")
	}
	return nil, fmt.Errorf("attempts (%d) or elapsed time (%v) exhausted", attempt, totalRetryTime)
}

func (bc *broadcastClient) doAction(action func() (interface{}, error), actionOnNewConnection func()) (interface{}, error) {
	logger.Infof("zs: local doAction")
	bc.mutex.Lock()
	conn := bc.conn
	bc.mutex.Unlock()
	if conn == nil {
		err := bc.connect()
		if err != nil {
			return nil, err
		}
		actionOnNewConnection()
	}
	resp, err := action()
	if err != nil {
		bc.Disconnect()
		return nil, err
	}
	return resp, nil
}

func (bc *broadcastClient) sleep(duration time.Duration) {
	logger.Infof("zs: local sleep")
	select {
	case <-time.After(duration):
	case <-bc.stopChan:
	}
}

func (bc *broadcastClient) connect() error {
	logger.Infof("zs: local connect")
	bc.mutex.Lock()
	bc.endpoint = ""
	bc.mutex.Unlock()
	conn, endpoint, err := bc.prod.NewConnection()
	logger.Debug("Connected to", endpoint)
	if err != nil {
		logger.Error("Failed obtaining connection:", err)
		return err
	}
	ctx, cf := context.WithCancel(context.Background())
	logger.Debug("Establishing gRPC stream with", endpoint, "...")
	abc, err := bc.createClient(conn).LocalDeliver(ctx)
	if err != nil {
		logger.Infof("zs: local connect abc, err := bc.createClient(conn).Deliver(ctx)")
		logger.Error("Connection to ", endpoint, "established but was unable to create gRPC stream:", err)
		conn.Close()
		cf()
		return err
	}
	err = bc.afterConnect(conn, abc, cf, endpoint)
	if err == nil {
		return nil
	}
	logger.Warning("Failed running post-connection procedures:", err)
	// If we reached here, lets make sure connection is closed
	// and nullified before we return
	bc.Disconnect()
	return err
}

func (bc *broadcastClient) afterConnect(conn *grpc.ClientConn, abc orderer.AtomicBroadcast_DeliverClient, cf context.CancelFunc, endpoint string) error {
	logger.Infof("zs: local afterconnect")
	logger.Debug("Entering")
	defer logger.Debug("Exiting")
	bc.mutex.Lock()
	bc.endpoint = endpoint
	bc.conn = &connection{ClientConn: conn, cancel: cf}
	bc.blocksDeliverer = abc
	if bc.shouldStop() {
		bc.mutex.Unlock()
		return errors.New("closing")
	}
	bc.mutex.Unlock()
	return nil
	//// If the client is closed at this point- before onConnect,
	//// any use of this object by onConnect would return an error.
	//err := bc.onConnect(bc)
	//// If the client is closed right after onConnect, but before
	//// the following lock- this method would return an error because
	//// the client has been closed.
	//bc.mutex.Lock()
	//defer bc.mutex.Unlock()
	//if bc.shouldStop() {
	//	return errors.New("closing")
	//}
	//// If the client is closed right after this method exits,
	//// it's because this method returned nil and not an error.
	//// So- connect() would return nil also, and the flow of the goroutine
	//// is returned to doAction(), where action() is invoked - and is configured
	//// to check whether the client has closed or not.
	//if err == nil {
	//	return nil
	//}
	//logger.Error("Failed setting up broadcast:", err)
	//return err
}

func (bc *broadcastClient) shouldStop() bool {

	if atomic.LoadInt32(&bc.stopFlag) == int32(1){
		logger.Infof("zs: local shouldStop afterconnect true")
	}else{
		logger.Infof("zs: local shouldStop afterconnect false")
	}
	return atomic.LoadInt32(&bc.stopFlag) == int32(1)
}

// Close makes the client close its connection and shut down
func (bc *broadcastClient) Close() {
	logger.Infof("zs: local close")
	logger.Debug("Entering")
	defer logger.Debug("Exiting")
	bc.mutex.Lock()
	defer bc.mutex.Unlock()
	if bc.shouldStop() {
		return
	}
	atomic.StoreInt32(&bc.stopFlag, int32(1))
	bc.stopChan <- struct{}{}
	if bc.conn == nil {
		return
	}
	bc.endpoint = ""
	bc.conn.Close()
}

// Disconnect makes the client close the existing connection and makes current endpoint unavailable for time interval, if disableEndpoint set to true
func (bc *broadcastClient) Disconnect() {
	logger.Infof("zs: local Disconnect")
	logger.Debug("Entering")
	defer logger.Debug("Exiting")
	bc.mutex.Lock()
	defer bc.mutex.Unlock()
	bc.endpoint = ""
	if bc.conn == nil {
		return
	}
	bc.conn.Close()
	bc.conn = nil
	bc.blocksDeliverer = nil
}

// UpdateEndpoints update endpoints to new values
func (bc *broadcastClient) UpdateEndpoints(endpoints []comm.EndpointCriteria) {
	logger.Infof("zs: local UpdateEndpoints")
	bc.mutex.Lock()
	endpointsUpdated := bc.areEndpointsUpdated(endpoints)
	bc.mutex.Unlock()

	if !endpointsUpdated {
		return
	}

	bc.prod.UpdateEndpoints(endpoints)
	bc.Disconnect()
}

func (bc *broadcastClient) areEndpointsUpdated(newEndpoints []comm.EndpointCriteria) bool {
	logger.Infof("zs: local areEndpointsUpdated")
	existingEndpoints := bc.prod.GetEndpoints()

	if len(newEndpoints) != len(existingEndpoints) {
		return true
	}

	// Check that endpoints were actually updated
	for _, endpoint := range newEndpoints {
		if !contains(endpoint, existingEndpoints) {
			// Found new endpoint
			return true
		}
	}
	// Endpoints are of the same length and the existing endpoints contain all the new endpoints,
	// so there are no new changes.
	return false
}

func contains(s comm.EndpointCriteria, a []comm.EndpointCriteria) bool {
	logger.Infof("zs: local contains")
	for _, e := range a {
		if e.Equals(s) {
			return true
		}
	}
	return false
}

type connection struct {
	sync.Once
	*grpc.ClientConn
	cancel context.CancelFunc
}

func (c *connection) Close() error {
	logger.Infof("zs: local (c *connection) Close()")
	var err error
	c.Once.Do(func() {
		c.cancel()
		err = c.ClientConn.Close()
	})
	return err
}
