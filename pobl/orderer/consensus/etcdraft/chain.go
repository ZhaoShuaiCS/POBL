/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package etcdraft

import (
	"context"
	"encoding/pem"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
	"strings"

	"code.cloudfoundry.org/clock"
	"github.com/golang/protobuf/proto"
	"github.com/hyperledger/fabric/common/configtx"
	"github.com/hyperledger/fabric/common/flogging"
	"github.com/hyperledger/fabric/orderer/common/cluster"
	"github.com/hyperledger/fabric/orderer/consensus"
	"github.com/hyperledger/fabric/protos/common"
	"github.com/hyperledger/fabric/protos/orderer"
	"github.com/hyperledger/fabric/protos/orderer/etcdraft"
	"github.com/hyperledger/fabric/protos/utils"
	"github.com/pkg/errors"
	"go.etcd.io/etcd/raft"
	"go.etcd.io/etcd/raft/raftpb"
	"go.etcd.io/etcd/wal"

	//"github.com/hyperledger/fabric/core/ledger/kvledger/txmgmt/rwsetutil"
	"github.com/hyperledger/fabric/orderer/common/partialorder"

)
var logger = flogging.MustGetLogger("ZSRaft")

const (
	BYTE = 1 << (10 * iota)
	KILOBYTE
	MEGABYTE
	GIGABYTE
	TERABYTE
)

const (
	// DefaultSnapshotCatchUpEntries is the default number of entries
	// to preserve in memory when a snapshot is taken. This is for
	// slow followers to catch up.
	DefaultSnapshotCatchUpEntries = uint64(20)

	// DefaultSnapshotIntervalSize is the default snapshot interval. It is
	// used if SnapshotIntervalSize is not provided in channel config options.
	// It is needed to enforce snapshot being set.
	DefaultSnapshotIntervalSize = 20 * MEGABYTE // 20 MB

	// DefaultEvictionSuspicion is the threshold that a node will start
	// suspecting its own eviction if it has been leaderless for this
	// period of time.
	DefaultEvictionSuspicion = time.Minute * 10

	// DefaultLeaderlessCheckInterval is the interval that a chain checks
	// its own leadership status.
	DefaultLeaderlessCheckInterval = time.Second * 10
)

//go:generate counterfeiter -o mocks/configurator.go . Configurator

// Configurator is used to configure the communication layer
// when the chain starts.
type Configurator interface {
	Configure(channel string, newNodes []cluster.RemoteNode)
}

//go:generate counterfeiter -o mocks/mock_rpc.go . RPC

// RPC is used to mock the transport layer in tests.
type RPC interface {
	SendConsensus(dest uint64, msg *orderer.ConsensusRequest) error
	SendSubmit(dest uint64, request *orderer.SubmitRequest) error
}

//go:generate counterfeiter -o mocks/mock_blockpuller.go . BlockPuller

// BlockPuller is used to pull blocks from other OSN
type BlockPuller interface {
	PullBlock(seq uint64) *common.Block
	HeightsByEndpoints() (map[string]uint64, error)
	Close()
}

// CreateBlockPuller is a function to create BlockPuller on demand.
// It is passed into chain initializer so that tests could mock this.
type CreateBlockPuller func() (BlockPuller, error)

// Options contains all the configurations relevant to the chain.
type Options struct {
	RaftID uint64

	Clock clock.Clock

	WALDir               string
	SnapDir              string
	SnapshotIntervalSize uint32

	// This is configurable mainly for testing purpose. Users are not
	// expected to alter this. Instead, DefaultSnapshotCatchUpEntries is used.
	SnapshotCatchUpEntries uint64

	MemoryStorage MemoryStorage
	Logger        *flogging.FabricLogger

	TickInterval      time.Duration
	ElectionTick      int
	HeartbeatTick     int
	MaxSizePerMsg     uint64
	MaxInflightBlocks int

	// BlockMetdata and Consenters should only be modified while under lock
	// of raftMetadataLock
	BlockMetadata *etcdraft.BlockMetadata
	Consenters    map[uint64]*etcdraft.Consenter

	// MigrationInit is set when the node starts right after consensus-type migration
	MigrationInit bool

	Metrics *Metrics
	Cert    []byte

	EvictionSuspicion   time.Duration
	LeaderCheckInterval time.Duration
}

type submit struct {
	req    *orderer.SubmitRequest
	leader chan uint64
}

type gc struct {
	index uint64
	state raftpb.ConfState
	data  []byte
}
//zs
// type ReadKV struct {
// 	Key string
// 	Value []byte
// 	Type int64     //1: read-write, must wait or false; 2: only-write, must wait or false; 3: only-read, no wait or ture;
// 	ParentTxId string
// }
// type WriteKV struct {
// 	Key string
// 	Value []byte
// 	Type int64     //1: inReadSet; 0: OutofReadset
// }
// type GNode struct {
// 	TxResult *common.Envelope
// 	TxRWSet *rwsetutil.TxRwSet
// 	TxId string
// 	TxCount int64
// 	PeerId string
// 	Votes int64
// 	ParentTxs map[string] *ReadKV
// 	ReadSet map[string] *ReadKV
// 	WriteSet map[string]*WriteKV
// 	State int64 //1.committed; 2.wait; 3:false
// 	NumParentsOfRW int64  //must wait or false
// 	NumParentsOfOW int64  //must wait or false
// 	NumParentsOfOR int64  //no wait or true

// 	IsCommitted int64//1.committed;2.wait
// 	NumPGChooseNode int64//if ==0 commit

// 	WaitPTX map[string]bool
// }
// type GChooseEdge struct {
// 	ChildNode  *GNode
// 	Next *GChooseEdge
// 	State int64 //1:success; 2:wait; 3:false
// 	IsCommitted int64//1.committed;2.wait
// }
// type CommittedGnode struct {
// 	State int64   //1: success commit; 2: wait enough weight or parents; 3: false consensus
// 	committedGnode *GNode
// 	IsCommitted int64//1.has committed;0 not committed
// }
// type GNodeResult struct {
// 	GnodeResult *GNode
// 	Next *GNodeResult
// 	State int64 //1:success; 2:wait; 3:false
// }
// type UnChooseTx struct {
// 	ti time.Time
// 	childTx map[string]bool
// }
// type GConsensus struct {
// 	GChooseEdgeSet map[string]*GChooseEdge
// 	GChooseNode map[string]*GNode
// 	GChooseNodeWaitPtx map[string]*UnChooseTx
// 	//GKVTable map[string] string
// 	//GEdgeSet map[string] *GEdge
// 	GNodeInBlock map[string] *CommittedGnode
// 	GTxWeight map[string]int64 //all notes
// 	GTxRelusts map[string]*GNodeResult //votes of each results
// 	GnodeMaxResults map[string] *GNodeResult
// 	NumExeNodes int64
// 	NumKWeight int64
// 	NumTxwithAllvotes int64
// 	NumSendInBlock int64

// 	NumVotes2 int64
// 	NumVotes3 int64
// 	NumVotes4 int64
// 	NumVotes5 int64
// 	NumGChooseNode int64
// 	NumGChooseEdge int64
// 	LastNumGChooseEdge int64

// }



//zs
//zs0423
// type GVReadKV struct {
// 	Key string
// 	Value string
// 	Type int64     //1: read-write, must wait or false; 2: only-write, must wait or false; 3: only-read, no wait or ture;
// 	ParentTxId string
// }
// type GVWriteKV struct {
// 	Key string
// 	Value string
// 	Type int64     //1: inReadSet; 0: OutofReadset
// }
// type GVNode struct {
// 	TxResult *common.Envelope
// 	TxRWSet *rwsetutil.TxRwSet
// 	TxId string
// 	Votes int64
// 	ReadSet map[string] *GVReadKV
// 	WriteSet map[string]*GVWriteKV
// 	State int64 //1.committed; 2.wait but choosed; 3. wait but not choosed; 4:false
// }
// type ValueVoteNode struct {
// 	Vote int64
// 	ValueGVNode []*GVNode
// 	Value string
// }
// type ValueVote struct {
// 	ValueSet map[string]*ValueVoteNode
// 	MaxValueVoteNode *ValueVoteNode
// }
// type UnchoosedGVNode struct {
// 	ChoosedGVNode *GVNode
// 	ReadVotes map[string]*ValueVote
// 	WriteVotes map[string]*ValueVote
// 	NumConsensusRKV int64
// 	NumConsensusWKV int64
// 	ValueGVNode []*GVNode
// 	AllVotes int64
// }
// type StateValue struct {
// 	Value string
// 	History map[string]bool
// 	//Timestamp time.Time
// }
// type WaitGVNode struct {
// 	WaitValueGVNode map[string]*GVNode
// }
// type WaitStateValue struct {
// 	Value map[string]*WaitGVNode
// }
// type GVConsensus struct {
// 	GUnchoosedGVNode map[string]*UnchoosedGVNode
// 	GChoosedGVNode map[string]*GVNode
// 	GlobalState map[string]*StateValue
// 	WaitStateGVNode map[string]*WaitStateValue
// 	NumExeNodes int64
// 	NumKWeight int64
// 	NumTxwithAllvotes int64
// 	NumVotes2 int64
// 	NumVotes3 int64
// 	NumVotes4 int64
// 	NumVotes5 int64
// 	NumGChooseNode int64
// 	NumGCommittedNode int64
// }

//zs0423
// Chain implements consensus.Chain interface.
type Chain struct {
	configurator Configurator

	rpc RPC

	raftID    uint64
	channelID string

	lastKnownLeader uint64

	submitC  chan *submit
	applyC   chan apply
	//CommitC   chan *common.Envelope
	observeC chan<- raft.SoftState // Notifies external observer on leader change (passed in optionally as an argument for tests)
	haltC    chan struct{}         // Signals to goroutines that the chain is halting
	doneC    chan struct{}         // Closes when the chain halts
	startC   chan struct{}         // Closes when the node is started
	snapC    chan *raftpb.Snapshot // Signal to catch up with snapshot
	gcC      chan *gc              // Signal to take snapshot

	errorCLock sync.RWMutex
	errorC     chan struct{} // returned by Errored()

	raftMetadataLock     sync.RWMutex
	confChangeInProgress *raftpb.ConfChange
	justElected          bool // this is true when node has just been elected
	configInflight       bool // this is true when there is config block or ConfChange in flight
	blockInflight        int  // number of in flight blocks

	clock clock.Clock // Tests can inject a fake clock

	support consensus.ConsenterSupport

	lastBlock    *common.Block
	appliedIndex uint64

	// needed by snapshotting
	sizeLimit        uint32 // SnapshotIntervalSize in bytes
	accDataSize      uint32 // accumulative data size since last snapshot
	lastSnapBlockNum uint64
	confState        raftpb.ConfState // Etcdraft requires ConfState to be persisted within snapshot

	createPuller CreateBlockPuller // func used to create BlockPuller on demand

	fresh bool // indicate if this is a fresh raft node

	// this is exported so that test can use `Node.Status()` to get raft node status.
	Node *node
	opts Options

	Metrics *Metrics
	logger  *flogging.FabricLogger

	periodicChecker *PeriodicCheck

	haltCallback func()

	//zs
	//GCon *GConsensus
	GVCon *partialorder.GVConsensus
	CommitC  chan [][]*common.Envelope
	//GlobalState map[string]*GNode
	//zs
}

// NewChain constructs a chain object.
func NewChain(
	support consensus.ConsenterSupport,
	opts Options,
	conf Configurator,
	rpc RPC,
	f CreateBlockPuller,
	haltCallback func(),
	observeC chan<- raft.SoftState) (*Chain, error) {

	lg := opts.Logger.With("channel", support.ChainID(), "node", opts.RaftID)

	fresh := !wal.Exist(opts.WALDir)
	storage, err := CreateStorage(lg, opts.WALDir, opts.SnapDir, opts.MemoryStorage)
	if err != nil {
		return nil, errors.Errorf("failed to restore persisted raft data: %s", err)
	}

	if opts.SnapshotCatchUpEntries == 0 {
		storage.SnapshotCatchUpEntries = DefaultSnapshotCatchUpEntries
	} else {
		storage.SnapshotCatchUpEntries = opts.SnapshotCatchUpEntries
	}

	sizeLimit := opts.SnapshotIntervalSize
	if sizeLimit == 0 {
		sizeLimit = DefaultSnapshotIntervalSize
	}

	// get block number in last snapshot, if exists
	var snapBlkNum uint64
	var cc raftpb.ConfState
	if s := storage.Snapshot(); !raft.IsEmptySnap(s) {
		b := utils.UnmarshalBlockOrPanic(s.Data)
		snapBlkNum = b.Header.Number
		cc = s.Metadata.ConfState
	}

	b := support.Block(support.Height() - 1)
	if b == nil {
		return nil, errors.Errorf("failed to get last block")
	}

	c := &Chain{
		configurator:     conf,
		rpc:              rpc,
		channelID:        support.ChainID(),
		raftID:           opts.RaftID,
		submitC:          make(chan *submit,80000),
		applyC:           make(chan apply,80000),
		CommitC:          make(chan [][]*common.Envelope,1000),
		haltC:            make(chan struct{}),
		doneC:            make(chan struct{}),
		startC:           make(chan struct{}),
		snapC:            make(chan *raftpb.Snapshot),
		errorC:           make(chan struct{}),
		gcC:              make(chan *gc),
		observeC:         observeC,
		support:          support,
		fresh:            fresh,
		appliedIndex:     opts.BlockMetadata.RaftIndex,
		lastBlock:        b,
		sizeLimit:        sizeLimit,
		lastSnapBlockNum: snapBlkNum,
		confState:        cc,
		createPuller:     f,
		clock:            opts.Clock,
		haltCallback:     haltCallback,
		Metrics: &Metrics{
			ClusterSize:             opts.Metrics.ClusterSize.With("channel", support.ChainID()),
			IsLeader:                opts.Metrics.IsLeader.With("channel", support.ChainID()),
			CommittedBlockNumber:    opts.Metrics.CommittedBlockNumber.With("channel", support.ChainID()),
			SnapshotBlockNumber:     opts.Metrics.SnapshotBlockNumber.With("channel", support.ChainID()),
			LeaderChanges:           opts.Metrics.LeaderChanges.With("channel", support.ChainID()),
			ProposalFailures:        opts.Metrics.ProposalFailures.With("channel", support.ChainID()),
			DataPersistDuration:     opts.Metrics.DataPersistDuration.With("channel", support.ChainID()),
			NormalProposalsReceived: opts.Metrics.NormalProposalsReceived.With("channel", support.ChainID()),
			ConfigProposalsReceived: opts.Metrics.ConfigProposalsReceived.With("channel", support.ChainID()),
		},
		logger: lg,
		opts:   opts,
		// GCon: &GConsensus{
		// 	GNodeInBlock: make(map[string]*CommittedGnode),
		// 	GChooseNodeWaitPtx: make(map[string]*UnChooseTx),
		// 	GTxWeight: make(map[string]int64),
		// 	GTxRelusts: make(map[string]*GNodeResult),
		// 	GnodeMaxResults: make(map[string]*GNodeResult),
		// 	NumExeNodes: 5,
		// 	NumKWeight: 3,
		// 	NumTxwithAllvotes: 0,
		// 	NumSendInBlock: 0,

		// 	NumVotes2: 0,
		// 	NumVotes3: 0,
		// 	NumVotes4: 0,
		// 	NumVotes5: 0,
		// 	GChooseEdgeSet: make(map[string]*GChooseEdge),
		// 	GChooseNode: make(map[string]*GNode),
		// 	NumGChooseEdge: 0,
		// 	NumGChooseNode: 0,
		// 	LastNumGChooseEdge: 0,
		// },
		GVCon: &partialorder.GVConsensus{
			GUnchoosedGVNode: make(map[string]*partialorder.UnchoosedGVNode),
			GChoosedGVNode: make(map[string]*partialorder.GVNode),
			// GlobalState: make(map[string]*partialorder.StateValue),
			// WaitStateGVNode: make(map[string]*partialorder.WaitStateValue),
			NumExeNodes: 5,
			NumKWeight: 3,
			NumTxwithAllvotes: 0,
			NumVotes2: 0,
			NumVotes3: 0,
			NumVotes4: 0,
			NumVotes5: 0,
			NumGChooseNode: 0,
			NumGCommittedNode: 0,
			OTXCount:0, 
			OTXLastCount:0, 
			OTXLastTime:time.Now(),
			ROTXCount:0, 
			ROTXLastCount:0, 
			ROTXLastTime:time.Now(),
		},
	}

	// Sets initial values for metrics
	c.Metrics.ClusterSize.Set(float64(len(c.opts.BlockMetadata.ConsenterIds)))
	c.Metrics.IsLeader.Set(float64(0)) // all nodes start out as followers
	c.Metrics.CommittedBlockNumber.Set(float64(c.lastBlock.Header.Number))
	c.Metrics.SnapshotBlockNumber.Set(float64(c.lastSnapBlockNum))

	// DO NOT use Applied option in config, see https://github.com/etcd-io/etcd/issues/10217
	// We guard against replay of written blocks with `appliedIndex` instead.
	config := &raft.Config{
		ID:              c.raftID,
		ElectionTick:    c.opts.ElectionTick,
		HeartbeatTick:   c.opts.HeartbeatTick,
		MaxSizePerMsg:   c.opts.MaxSizePerMsg,
		MaxInflightMsgs: c.opts.MaxInflightBlocks,
		Logger:          c.logger,
		Storage:         c.opts.MemoryStorage,
		// PreVote prevents reconnected node from disturbing network.
		// See etcd/raft doc for more details.
		PreVote:                   true,
		CheckQuorum:               true,
		DisableProposalForwarding: true, // This prevents blocks from being accidentally proposed by followers
	}

	c.Node = &node{
		chainID:      c.channelID,
		chain:        c,
		logger:       c.logger,
		metrics:      c.Metrics,
		storage:      storage,
		rpc:          c.rpc,
		config:       config,
		tickInterval: c.opts.TickInterval,
		clock:        c.clock,
		metadata:     c.opts.BlockMetadata,
	}

	return c, nil
}

// Start instructs the orderer to begin serving the chain and keep it current.
func (c *Chain) Start() {
	c.logger.Infof("Starting Raft node")

	if err := c.configureComm(); err != nil {
		c.logger.Errorf("Failed to start chain, aborting: +%v", err)
		close(c.doneC)
		return
	}

	isJoin := c.support.Height() > 1
	if isJoin && c.opts.MigrationInit {
		isJoin = false
		c.logger.Infof("Consensus-type migration detected, starting new raft node on an existing channel; height=%d", c.support.Height())
	}
	c.Node.start(c.fresh, isJoin)

	close(c.startC)
	close(c.errorC)

	go c.gc()
	go c.serveRequest()

	es := c.newEvictionSuspector()

	interval := DefaultLeaderlessCheckInterval
	if c.opts.LeaderCheckInterval != 0 {
		interval = c.opts.LeaderCheckInterval
	}

	c.periodicChecker = &PeriodicCheck{
		Logger:        c.logger,
		Report:        es.confirmSuspicion,
		ReportCleared: es.clearSuspicion,
		CheckInterval: interval,
		Condition:     c.suspectEviction,
	}
	c.periodicChecker.Run()
}

// Order submits normal type transactions for ordering.
func (c *Chain) Order(env *common.Envelope, configSeq uint64) error {
	c.Metrics.NormalProposalsReceived.Add(1)
	return c.Submit(&orderer.SubmitRequest{LastValidationSeq: configSeq, Payload: env, Channel: c.channelID}, 0)
}

// Configure submits config type transactions for ordering.
func (c *Chain) Configure(env *common.Envelope, configSeq uint64) error {
	c.Metrics.ConfigProposalsReceived.Add(1)
	if err := c.checkConfigUpdateValidity(env); err != nil {
		c.logger.Warnf("Rejected config: %s", err)
		c.Metrics.ProposalFailures.Add(1)
		return err
	}
	return c.Submit(&orderer.SubmitRequest{LastValidationSeq: configSeq, Payload: env, Channel: c.channelID}, 0)
}

// Validate the config update for being of Type A or Type B as described in the design doc.
func (c *Chain) checkConfigUpdateValidity(ctx *common.Envelope) error {
	var err error
	payload, err := utils.UnmarshalPayload(ctx.Payload)
	if err != nil {
		return err
	}
	chdr, err := utils.UnmarshalChannelHeader(payload.Header.ChannelHeader)
	if err != nil {
		return err
	}

	if chdr.Type != int32(common.HeaderType_ORDERER_TRANSACTION) &&
		chdr.Type != int32(common.HeaderType_CONFIG) {
		return errors.Errorf("config transaction has unknown header type: %s", common.HeaderType(chdr.Type))
	}

	if chdr.Type == int32(common.HeaderType_ORDERER_TRANSACTION) {
		newChannelConfig, err := utils.UnmarshalEnvelope(payload.Data)
		if err != nil {
			return err
		}

		payload, err = utils.UnmarshalPayload(newChannelConfig.Payload)
		if err != nil {
			return err
		}
	}

	configUpdate, err := configtx.UnmarshalConfigUpdateFromPayload(payload)
	if err != nil {
		return err
	}

	metadata, err := MetadataFromConfigUpdate(configUpdate)
	if err != nil {
		return err
	}

	if metadata == nil {
		return nil // ConsensusType is not updated
	}

	if err = CheckConfigMetadata(metadata); err != nil {
		return err
	}

	switch chdr.Type {
	case int32(common.HeaderType_ORDERER_TRANSACTION):
		c.raftMetadataLock.RLock()
		set := MembershipByCert(c.opts.Consenters)
		c.raftMetadataLock.RUnlock()

		for _, c := range metadata.Consenters {
			if _, exits := set[string(c.ClientTlsCert)]; !exits {
				return errors.Errorf("new channel has consenter that is not part of system consenter set")
			}
		}

		return nil

	case int32(common.HeaderType_CONFIG):
		c.raftMetadataLock.RLock()
		_, err = ComputeMembershipChanges(c.opts.BlockMetadata, c.opts.Consenters, metadata.Consenters)
		c.raftMetadataLock.RUnlock()

		return err

	default:
		// panic here because we have just check header type and return early
		c.logger.Panicf("Programming error, unknown header type")
	}

	return nil
}

// WaitReady blocks when the chain:
// - is catching up with other nodes using snapshot
//
// In any other case, it returns right away.
func (c *Chain) WaitReady() error {
	if err := c.isRunning(); err != nil {
		return err
	}

	select {
	case c.submitC <- nil:
	case <-c.doneC:
		return errors.Errorf("chain is stopped")
	}

	return nil
}

// Errored returns a channel that closes when the chain stops.
func (c *Chain) Errored() <-chan struct{} {
	c.errorCLock.RLock()
	defer c.errorCLock.RUnlock()
	return c.errorC
}

// Halt stops the chain.
func (c *Chain) Halt() {
	select {
	case <-c.startC:
	default:
		c.logger.Warnf("Attempted to halt a chain that has not started")
		return
	}

	select {
	case c.haltC <- struct{}{}:
	case <-c.doneC:
		return
	}
	<-c.doneC

	if c.haltCallback != nil {
		c.haltCallback()
	}
}

func (c *Chain) isRunning() error {
	select {
	case <-c.startC:
	default:
		return errors.Errorf("chain is not started")
	}

	select {
	case <-c.doneC:
		return errors.Errorf("chain is stopped")
	default:
	}

	return nil
}

// Consensus passes the given ConsensusRequest message to the raft.Node instance
func (c *Chain) Consensus(req *orderer.ConsensusRequest, sender uint64) error {
	if err := c.isRunning(); err != nil {
		return err
	}

	stepMsg := &raftpb.Message{}
	if err := proto.Unmarshal(req.Payload, stepMsg); err != nil {
		return fmt.Errorf("failed to unmarshal StepRequest payload to Raft Message: %s", err)
	}

	if err := c.Node.Step(context.TODO(), *stepMsg); err != nil {
		return fmt.Errorf("failed to process Raft Step message: %s", err)
	}

	return nil
}

// Submit forwards the incoming request to:
// - the local serveRequest goroutine if this is leader
// - the actual leader via the transport mechanism
// The call fails if there's no leader elected yet.
func (c *Chain) Submit(req *orderer.SubmitRequest, sender uint64) error {
	if err := c.isRunning(); err != nil {
		c.Metrics.ProposalFailures.Add(1)
		return err
	}

	leadC := make(chan uint64, 1)
	select {
	case c.submitC <- &submit{req, leadC}:
		lead := <-leadC
		if lead == raft.None {
			c.Metrics.ProposalFailures.Add(1)
			return errors.Errorf("no Raft leader")
		}

		if lead != c.raftID {
			if err := c.rpc.SendSubmit(lead, req); err != nil {
				c.Metrics.ProposalFailures.Add(1)
				return err
			}
		}

	case <-c.doneC:
		c.Metrics.ProposalFailures.Add(1)
		return errors.Errorf("chain is stopped")
	}

	return nil
}

type apply struct {
	entries []raftpb.Entry
	soft    *raft.SoftState
}

func isCandidate(state raft.StateType) bool {
	return state == raft.StatePreCandidate || state == raft.StateCandidate
}

//zs0424
// func (c *Chain) serveRequest() {
// 	ticking := false
// 	timer := c.clock.NewTimer(time.Second)
// 	// we need a stopped timer rather than nil,
// 	// because we will be select waiting on timer.C()
// 	if !timer.Stop() {
// 		<-timer.C()
// 	}

// 	// if timer is already started, this is a no-op
// 	startTimer := func() {
// 		if !ticking {
// 			ticking = true
// 			timer.Reset(c.support.SharedConfig().BatchTimeout())
// 		}
// 	}

// 	stopTimer := func() {
// 		if !timer.Stop() && ticking {
// 			// we only need to drain the channel if the timer expired (not explicitly stopped)
// 			<-timer.C()
// 		}
// 		ticking = false
// 	}

// 	var soft raft.SoftState
// 	submitC := c.submitC
// 	var bc *blockCreator

// 	var propC chan<- *common.Block
// 	var cancelProp context.CancelFunc
// 	cancelProp = func() {} // no-op as initial value

// 	becomeLeader := func() (chan<- *common.Block, context.CancelFunc) {
// 		c.Metrics.IsLeader.Set(1)

// 		c.blockInflight = 0
// 		c.justElected = true
// 		submitC = nil
// 		ch := make(chan *common.Block, c.opts.MaxInflightBlocks)

// 		// if there is unfinished ConfChange, we should resume the effort to propose it as
// 		// new leader, and wait for it to be committed before start serving new requests.
// 		if cc := c.getInFlightConfChange(); cc != nil {
// 			// The reason `ProposeConfChange` should be called in go routine is documented in `writeConfigBlock` method.
// 			go func() {
// 				if err := c.Node.ProposeConfChange(context.TODO(), *cc); err != nil {
// 					c.logger.Warnf("Failed to propose configuration update to Raft node: %s", err)
// 				}
// 			}()

// 			c.confChangeInProgress = cc
// 			c.configInflight = true
// 		}

// 		// Leader should call Propose in go routine, because this method may be blocked
// 		// if node is leaderless (this can happen when leader steps down in a heavily
// 		// loaded network). We need to make sure applyC can still be consumed properly.
// 		ctx, cancel := context.WithCancel(context.Background())
// 		go func(ctx context.Context, ch <-chan *common.Block) {
// 			for {
// 				select {
// 				case b := <-ch:
// 					data := utils.MarshalOrPanic(b)
// 					if err := c.Node.Propose(ctx, data); err != nil {
// 						c.logger.Errorf("Failed to propose block [%d] to raft and discard %d blocks in queue: %s", b.Header.Number, len(ch), err)
// 						return
// 					}
// 					c.logger.Debugf("Proposed block [%d] to raft consensus", b.Header.Number)

// 				case <-ctx.Done():
// 					c.logger.Debugf("Quit proposing blocks, discarded %d blocks in the queue", len(ch))
// 					return
// 				}
// 			}
// 		}(ctx, ch)

// 		return ch, cancel
// 	}

// 	becomeFollower := func() {
// 		cancelProp()
// 		c.blockInflight = 0
// 		_ = c.support.BlockCutter().Cut()
// 		stopTimer()
// 		submitC = c.submitC
// 		bc = nil
// 		c.Metrics.IsLeader.Set(0)
// 	}

// 	for {
// 		select {
// 		case env :=<-c.CommitC:
// 			batches, pending := c.support.BlockCutter().Ordered(env)
// 			if pending {
// 				startTimer() // no-op if timer is already started
// 			} else {
// 				stopTimer()
// 			}

// 			c.propose(propC, bc, batches...)
// 		case s := <-submitC:
// 			if s == nil {
// 				// polled by `WaitReady`
// 				continue
// 			}

// 			if soft.RaftState == raft.StatePreCandidate || soft.RaftState == raft.StateCandidate {
// 				s.leader <- raft.None
// 				continue
// 			}

// 			s.leader <- soft.Lead
// 			if soft.Lead != c.raftID {
// 				continue
// 			}

// 			batches, _, err := c.ordered(s.req)
// 			if err != nil {
// 				c.logger.Errorf("Failed to order message: %s", err)
// 				continue
// 			}
// 			// if pending {
// 			// 	startTimer() // no-op if timer is already started
// 			// } else {
// 			// 	stopTimer()
// 			// }

// 			if batches!=nil{
// 				c.propose(propC, bc, batches...)
// 			}
// 			if c.configInflight {
// 				c.logger.Info("Received config transaction, pause accepting transaction till it is committed")
// 				submitC = nil
// 			} else if c.blockInflight >= c.opts.MaxInflightBlocks {
// 				c.logger.Debugf("Number of in-flight blocks (%d) reaches limit (%d), pause accepting transaction",
// 					c.blockInflight, c.opts.MaxInflightBlocks)
// 				submitC = nil
// 			}

// 		case app := <-c.applyC:
// 			if app.soft != nil {
// 				newLeader := atomic.LoadUint64(&app.soft.Lead) // etcdraft requires atomic access
// 				if newLeader != soft.Lead {
// 					c.logger.Infof("Raft leader changed: %d -> %d", soft.Lead, newLeader)
// 					c.Metrics.LeaderChanges.Add(1)

// 					atomic.StoreUint64(&c.lastKnownLeader, newLeader)

// 					if newLeader == c.raftID {
// 						propC, cancelProp = becomeLeader()
// 					}

// 					if soft.Lead == c.raftID {
// 						becomeFollower()
// 					}
// 				}

// 				foundLeader := soft.Lead == raft.None && newLeader != raft.None
// 				quitCandidate := isCandidate(soft.RaftState) && !isCandidate(app.soft.RaftState)

// 				if foundLeader || quitCandidate {
// 					c.errorCLock.Lock()
// 					c.errorC = make(chan struct{})
// 					c.errorCLock.Unlock()
// 				}

// 				if isCandidate(app.soft.RaftState) || newLeader == raft.None {
// 					atomic.StoreUint64(&c.lastKnownLeader, raft.None)
// 					select {
// 					case <-c.errorC:
// 					default:
// 						nodeCount := len(c.opts.BlockMetadata.ConsenterIds)
// 						// Only close the error channel (to signal the broadcast/deliver front-end a consensus backend error)
// 						// If we are a cluster of size 3 or more, otherwise we can't expand a cluster of size 1 to 2 nodes.
// 						if nodeCount > 2 {
// 							close(c.errorC)
// 						} else {
// 							c.logger.Warningf("No leader is present, cluster size is %d", nodeCount)
// 						}
// 					}
// 				}

// 				soft = raft.SoftState{Lead: newLeader, RaftState: app.soft.RaftState}

// 				// notify external observer
// 				select {
// 				case c.observeC <- soft:
// 				default:
// 				}
// 			}

// 			c.apply(app.entries)

// 			if c.justElected {
// 				msgInflight := c.Node.lastIndex() > c.appliedIndex
// 				if msgInflight {
// 					c.logger.Debugf("There are in flight blocks, new leader should not serve requests")
// 					continue
// 				}

// 				if c.configInflight {
// 					c.logger.Debugf("There is config block in flight, new leader should not serve requests")
// 					continue
// 				}

// 				c.logger.Infof("Start accepting requests as Raft leader at block [%d]", c.lastBlock.Header.Number)
// 				bc = &blockCreator{
// 					hash:   c.lastBlock.Header.Hash(),
// 					number: c.lastBlock.Header.Number,
// 					logger: c.logger,
// 				}
// 				submitC = c.submitC
// 				c.justElected = false
// 			} else if c.configInflight {
// 				c.logger.Info("Config block or ConfChange in flight, pause accepting transaction")
// 				submitC = nil
// 			} else if c.blockInflight < c.opts.MaxInflightBlocks {
// 				submitC = c.submitC
// 			}

// 		case <-timer.C():
// 			ticking = false

// 			batch := c.support.BlockCutter().Cut()
// 			if len(batch) == 0 {
// 				c.logger.Warningf("Batch timer expired with no pending requests, this might indicate a bug")
// 				continue
// 			}

// 			c.logger.Debugf("Batch timer expired, creating block")
// 			c.propose(propC, bc, batch) // we are certain this is normal block, no need to block

// 		case sn := <-c.snapC:
// 			if sn.Metadata.Index != 0 {
// 				if sn.Metadata.Index <= c.appliedIndex {
// 					c.logger.Debugf("Skip snapshot taken at index %d, because it is behind current applied index %d", sn.Metadata.Index, c.appliedIndex)
// 					break
// 				}

// 				c.confState = sn.Metadata.ConfState
// 				c.appliedIndex = sn.Metadata.Index
// 			} else {
// 				c.logger.Infof("Received artificial snapshot to trigger catchup")
// 			}

// 			if err := c.catchUp(sn); err != nil {
// 				c.logger.Panicf("Failed to recover from snapshot taken at Term %d and Index %d: %s",
// 					sn.Metadata.Term, sn.Metadata.Index, err)
// 			}

// 		case <-c.doneC:
// 			cancelProp()

// 			select {
// 			case <-c.errorC: // avoid closing closed channel
// 			default:
// 				close(c.errorC)
// 			}

// 			c.logger.Infof("Stop serving requests")
// 			c.periodicChecker.Stop()
// 			return
// 		}
// 	}
// }
//zs0424
//zs0513
func (c *Chain) serveRequest() {
	ticking := false
	timer := c.clock.NewTimer(time.Second)
	// we need a stopped timer rather than nil,
	// because we will be select waiting on timer.C()
	if !timer.Stop() {
		<-timer.C()
	}

	// if timer is already started, this is a no-op
	startTimer := func() {
		if !ticking {
			ticking = true
			timer.Reset(c.support.SharedConfig().BatchTimeout())
		}
	}

	stopTimer := func() {
		if !timer.Stop() && ticking {
			// we only need to drain the channel if the timer expired (not explicitly stopped)
			<-timer.C()
		}
		ticking = false
	}

	var soft raft.SoftState
	submitC := c.submitC
	var bc *blockCreator

	var propC chan<- *common.Block
	var cancelProp context.CancelFunc
	cancelProp = func() {} // no-op as initial value

	becomeLeader := func() (chan<- *common.Block, context.CancelFunc) {
		c.Metrics.IsLeader.Set(1)

		c.blockInflight = 0
		c.justElected = true
		submitC = nil
		ch := make(chan *common.Block, c.opts.MaxInflightBlocks)

		// if there is unfinished ConfChange, we should resume the effort to propose it as
		// new leader, and wait for it to be committed before start serving new requests.
		if cc := c.getInFlightConfChange(); cc != nil {
			// The reason `ProposeConfChange` should be called in go routine is documented in `writeConfigBlock` method.
			go func() {
				if err := c.Node.ProposeConfChange(context.TODO(), *cc); err != nil {
					c.logger.Warnf("Failed to propose configuration update to Raft node: %s", err)
				}
			}()

			c.confChangeInProgress = cc
			c.configInflight = true
		}

		// Leader should call Propose in go routine, because this method may be blocked
		// if node is leaderless (this can happen when leader steps down in a heavily
		// loaded network). We need to make sure applyC can still be consumed properly.
		ctx, cancel := context.WithCancel(context.Background())
		go func(ctx context.Context, ch <-chan *common.Block) {
			for {
				select {
				case b := <-ch:
					data := utils.MarshalOrPanic(b)
					if err := c.Node.Propose(ctx, data); err != nil {
						c.logger.Errorf("Failed to propose block [%d] to raft and discard %d blocks in queue: %s", b.Header.Number, len(ch), err)
						return
					}
					c.logger.Debugf("Proposed block [%d] to raft consensus", b.Header.Number)

				case <-ctx.Done():
					c.logger.Debugf("Quit proposing blocks, discarded %d blocks in the queue", len(ch))
					return
				}
			}
		}(ctx, ch)

		return ch, cancel
	}

	becomeFollower := func() {
		cancelProp()
		c.blockInflight = 0
		_ = c.support.BlockCutter().Cut()
		stopTimer()
		submitC = c.submitC
		bc = nil
		c.Metrics.IsLeader.Set(0)
	}

	for {
		select {
		case raftbatches := <-c.CommitC:
			logger.Infof("zs:raftbatch c.CommitC BE\n")
			stopTimer()
			c.propose(propC, bc, raftbatches...)
			logger.Infof("zs:raftbatch c.CommitC EN\n")
		case s := <-submitC:
			if s == nil {
				// polled by `WaitReady`
				continue
			}

			if soft.RaftState == raft.StatePreCandidate || soft.RaftState == raft.StateCandidate {
				s.leader <- raft.None
				continue
			}

			s.leader <- soft.Lead
			if soft.Lead != c.raftID {
				continue
			}

			batches, pending, err := c.ordered(s.req)
			if err != nil {
				c.logger.Errorf("Failed to order message: %s", err)
				continue
			}
			if pending {
				//logger.Infof("zs:CUT startTimer()\n")
				startTimer() // no-op if timer is already started
			} else {
				//logger.Infof("zs:CUT stopTimer()\n")
				stopTimer()
			}
			if len(batches)>0{
				c.propose(propC, bc, batches...)
			}


			if c.configInflight {
				c.logger.Info("Received config transaction, pause accepting transaction till it is committed")
				submitC = nil
			} else if c.blockInflight >= c.opts.MaxInflightBlocks {
				c.logger.Debugf("Number of in-flight blocks (%d) reaches limit (%d), pause accepting transaction",
					c.blockInflight, c.opts.MaxInflightBlocks)
				submitC = nil
			}

		case app := <-c.applyC:
			if app.soft != nil {
				newLeader := atomic.LoadUint64(&app.soft.Lead) // etcdraft requires atomic access
				if newLeader != soft.Lead {
					c.logger.Infof("Raft leader changed: %d -> %d", soft.Lead, newLeader)
					c.Metrics.LeaderChanges.Add(1)

					atomic.StoreUint64(&c.lastKnownLeader, newLeader)

					if newLeader == c.raftID {
						propC, cancelProp = becomeLeader()
					}

					if soft.Lead == c.raftID {
						becomeFollower()
					}
				}

				foundLeader := soft.Lead == raft.None && newLeader != raft.None
				quitCandidate := isCandidate(soft.RaftState) && !isCandidate(app.soft.RaftState)

				if foundLeader || quitCandidate {
					c.errorCLock.Lock()
					c.errorC = make(chan struct{})
					c.errorCLock.Unlock()
				}

				if isCandidate(app.soft.RaftState) || newLeader == raft.None {
					atomic.StoreUint64(&c.lastKnownLeader, raft.None)
					select {
					case <-c.errorC:
					default:
						nodeCount := len(c.opts.BlockMetadata.ConsenterIds)
						// Only close the error channel (to signal the broadcast/deliver front-end a consensus backend error)
						// If we are a cluster of size 3 or more, otherwise we can't expand a cluster of size 1 to 2 nodes.
						if nodeCount > 2 {
							close(c.errorC)
						} else {
							c.logger.Warningf("No leader is present, cluster size is %d", nodeCount)
						}
					}
				}

				soft = raft.SoftState{Lead: newLeader, RaftState: app.soft.RaftState}

				// notify external observer
				select {
				case c.observeC <- soft:
				default:
				}
			}

			c.apply(app.entries)

			if c.justElected {
				msgInflight := c.Node.lastIndex() > c.appliedIndex
				if msgInflight {
					c.logger.Debugf("There are in flight blocks, new leader should not serve requests")
					continue
				}

				if c.configInflight {
					c.logger.Debugf("There is config block in flight, new leader should not serve requests")
					continue
				}

				c.logger.Infof("Start accepting requests as Raft leader at block [%d]", c.lastBlock.Header.Number)
				bc = &blockCreator{
					hash:   c.lastBlock.Header.Hash(),
					number: c.lastBlock.Header.Number,
					logger: c.logger,
				}
				submitC = c.submitC
				c.justElected = false
			} else if c.configInflight {
				c.logger.Info("Config block or ConfChange in flight, pause accepting transaction")
				submitC = nil
			} else if c.blockInflight < c.opts.MaxInflightBlocks {
				submitC = c.submitC
			}

		case <-timer.C():
			ticking = false
			
			logger.Infof("zs:CUT timer\n")
			tempenvmsg:=&partialorder.EnvMsg{
				Env:nil,
				CommitC:c.CommitC,   
			}
			raftEnvMsgC:=c.support.BlockCutter().GetRaftEnvMsgC()
			logger.Infof("zs:zs:CUT timer raftEnvMsgC tempgvnodemsg len:%d BE\n",len(raftEnvMsgC))
			raftEnvMsgC <-tempenvmsg
			logger.Infof("zs:zs:CUT timer raftEnvMsgC tempgvnodemsg len:%d EN\n",len(raftEnvMsgC))

			// tempgvnodemsg:=&partialorder.GVNodeMsg{
			// 	ChoosedNode:nil,
			// 	CommitC:c.CommitC,   
			// }
			// raftGVNodeMsgC:=c.support.BlockCutter().GetRaftGVNodeMsgC()
			// logger.Infof("zs:zs:CUT timer raftGVNodeMsgC tempgvnodemsg len:%d BE\n",len(raftGVNodeMsgC))
			// raftGVNodeMsgC <-tempgvnodemsg
			// logger.Infof("zs:zs:CUT timer raftGVNodeMsgC tempgvnodemsg len:%d EN\n",len(raftGVNodeMsgC))

			// batch := c.support.BlockCutter().Cut()
			// if len(batch) == 0 {
			// 	c.logger.Warningf("Batch timer expired with no pending requests, this might indicate a bug")
			// 	continue
			// }

			// c.logger.Debugf("Batch timer expired, creating block")
			// c.propose(propC, bc, batch) // we are certain this is normal block, no need to block

		case sn := <-c.snapC:
			if sn.Metadata.Index != 0 {
				if sn.Metadata.Index <= c.appliedIndex {
					c.logger.Debugf("Skip snapshot taken at index %d, because it is behind current applied index %d", sn.Metadata.Index, c.appliedIndex)
					break
				}

				c.confState = sn.Metadata.ConfState
				c.appliedIndex = sn.Metadata.Index
			} else {
				c.logger.Infof("Received artificial snapshot to trigger catchup")
			}

			if err := c.catchUp(sn); err != nil {
				c.logger.Panicf("Failed to recover from snapshot taken at Term %d and Index %d: %s",
					sn.Metadata.Term, sn.Metadata.Index, err)
			}

		case <-c.doneC:
			cancelProp()

			select {
			case <-c.errorC: // avoid closing closed channel
			default:
				close(c.errorC)
			}

			c.logger.Infof("Stop serving requests")
			c.periodicChecker.Stop()
			return
		}
	}
}
//zs0513
// func (c *Chain) serveRequest() {
// 	ticking := false
// 	timer := c.clock.NewTimer(time.Second)
// 	// we need a stopped timer rather than nil,
// 	// because we will be select waiting on timer.C()
// 	if !timer.Stop() {
// 		<-timer.C()
// 	}

// 	// if timer is already started, this is a no-op
// 	startTimer := func() {
// 		if !ticking {
// 			ticking = true
// 			timer.Reset(c.support.SharedConfig().BatchTimeout())
// 		}
// 	}

// 	stopTimer := func() {
// 		if !timer.Stop() && ticking {
// 			// we only need to drain the channel if the timer expired (not explicitly stopped)
// 			<-timer.C()
// 		}
// 		ticking = false
// 	}

// 	var soft raft.SoftState
// 	submitC := c.submitC
// 	var bc *blockCreator

// 	var propC chan<- *common.Block
// 	var cancelProp context.CancelFunc
// 	cancelProp = func() {} // no-op as initial value

// 	becomeLeader := func() (chan<- *common.Block, context.CancelFunc) {
// 		c.Metrics.IsLeader.Set(1)

// 		c.blockInflight = 0
// 		c.justElected = true
// 		submitC = nil
// 		ch := make(chan *common.Block, c.opts.MaxInflightBlocks)

// 		// if there is unfinished ConfChange, we should resume the effort to propose it as
// 		// new leader, and wait for it to be committed before start serving new requests.
// 		if cc := c.getInFlightConfChange(); cc != nil {
// 			// The reason `ProposeConfChange` should be called in go routine is documented in `writeConfigBlock` method.
// 			go func() {
// 				if err := c.Node.ProposeConfChange(context.TODO(), *cc); err != nil {
// 					c.logger.Warnf("Failed to propose configuration update to Raft node: %s", err)
// 				}
// 			}()

// 			c.confChangeInProgress = cc
// 			c.configInflight = true
// 		}

// 		// Leader should call Propose in go routine, because this method may be blocked
// 		// if node is leaderless (this can happen when leader steps down in a heavily
// 		// loaded network). We need to make sure applyC can still be consumed properly.
// 		ctx, cancel := context.WithCancel(context.Background())
// 		go func(ctx context.Context, ch <-chan *common.Block) {
// 			for {
// 				select {
// 				case b := <-ch:
// 					data := utils.MarshalOrPanic(b)
// 					if err := c.Node.Propose(ctx, data); err != nil {
// 						c.logger.Errorf("Failed to propose block [%d] to raft and discard %d blocks in queue: %s", b.Header.Number, len(ch), err)
// 						return
// 					}
// 					c.logger.Debugf("Proposed block [%d] to raft consensus", b.Header.Number)

// 				case <-ctx.Done():
// 					c.logger.Debugf("Quit proposing blocks, discarded %d blocks in the queue", len(ch))
// 					return
// 				}
// 			}
// 		}(ctx, ch)

// 		return ch, cancel
// 	}

// 	becomeFollower := func() {
// 		cancelProp()
// 		c.blockInflight = 0
// 		_ = c.support.BlockCutter().Cut()
// 		stopTimer()
// 		submitC = c.submitC
// 		bc = nil
// 		c.Metrics.IsLeader.Set(0)
// 	}

// 	for {
// 		select {
// 		case s := <-submitC:
// 			if s == nil {
// 				// polled by `WaitReady`
// 				continue
// 			}

// 			if soft.RaftState == raft.StatePreCandidate || soft.RaftState == raft.StateCandidate {
// 				s.leader <- raft.None
// 				continue
// 			}

// 			s.leader <- soft.Lead
// 			if soft.Lead != c.raftID {
// 				continue
// 			}

// 			batches, pending, err := c.ordered(s.req)
// 			if err != nil {
// 				c.logger.Errorf("Failed to order message: %s", err)
// 				continue
// 			}
// 			if pending {
// 				logger.Infof("zs:CUT startTimer()\n")
// 				startTimer() // no-op if timer is already started
// 			} else {
// 				logger.Infof("zs:CUT stopTimer()\n")
// 				stopTimer()
// 			}

// 			c.propose(propC, bc, batches...)

// 			if c.configInflight {
// 				c.logger.Info("Received config transaction, pause accepting transaction till it is committed")
// 				submitC = nil
// 			} else if c.blockInflight >= c.opts.MaxInflightBlocks {
// 				c.logger.Debugf("Number of in-flight blocks (%d) reaches limit (%d), pause accepting transaction",
// 					c.blockInflight, c.opts.MaxInflightBlocks)
// 				submitC = nil
// 			}

// 		case app := <-c.applyC:
// 			if app.soft != nil {
// 				newLeader := atomic.LoadUint64(&app.soft.Lead) // etcdraft requires atomic access
// 				if newLeader != soft.Lead {
// 					c.logger.Infof("Raft leader changed: %d -> %d", soft.Lead, newLeader)
// 					c.Metrics.LeaderChanges.Add(1)

// 					atomic.StoreUint64(&c.lastKnownLeader, newLeader)

// 					if newLeader == c.raftID {
// 						propC, cancelProp = becomeLeader()
// 					}

// 					if soft.Lead == c.raftID {
// 						becomeFollower()
// 					}
// 				}

// 				foundLeader := soft.Lead == raft.None && newLeader != raft.None
// 				quitCandidate := isCandidate(soft.RaftState) && !isCandidate(app.soft.RaftState)

// 				if foundLeader || quitCandidate {
// 					c.errorCLock.Lock()
// 					c.errorC = make(chan struct{})
// 					c.errorCLock.Unlock()
// 				}

// 				if isCandidate(app.soft.RaftState) || newLeader == raft.None {
// 					atomic.StoreUint64(&c.lastKnownLeader, raft.None)
// 					select {
// 					case <-c.errorC:
// 					default:
// 						nodeCount := len(c.opts.BlockMetadata.ConsenterIds)
// 						// Only close the error channel (to signal the broadcast/deliver front-end a consensus backend error)
// 						// If we are a cluster of size 3 or more, otherwise we can't expand a cluster of size 1 to 2 nodes.
// 						if nodeCount > 2 {
// 							close(c.errorC)
// 						} else {
// 							c.logger.Warningf("No leader is present, cluster size is %d", nodeCount)
// 						}
// 					}
// 				}

// 				soft = raft.SoftState{Lead: newLeader, RaftState: app.soft.RaftState}

// 				// notify external observer
// 				select {
// 				case c.observeC <- soft:
// 				default:
// 				}
// 			}

// 			c.apply(app.entries)

// 			if c.justElected {
// 				msgInflight := c.Node.lastIndex() > c.appliedIndex
// 				if msgInflight {
// 					c.logger.Debugf("There are in flight blocks, new leader should not serve requests")
// 					continue
// 				}

// 				if c.configInflight {
// 					c.logger.Debugf("There is config block in flight, new leader should not serve requests")
// 					continue
// 				}

// 				c.logger.Infof("Start accepting requests as Raft leader at block [%d]", c.lastBlock.Header.Number)
// 				bc = &blockCreator{
// 					hash:   c.lastBlock.Header.Hash(),
// 					number: c.lastBlock.Header.Number,
// 					logger: c.logger,
// 				}
// 				submitC = c.submitC
// 				c.justElected = false
// 			} else if c.configInflight {
// 				c.logger.Info("Config block or ConfChange in flight, pause accepting transaction")
// 				submitC = nil
// 			} else if c.blockInflight < c.opts.MaxInflightBlocks {
// 				submitC = c.submitC
// 			}

// 		case <-timer.C():
// 			ticking = false
			
// 			logger.Infof("zs:CUT timer\n")

// 			batch := c.support.BlockCutter().Cut()
// 			if len(batch) == 0 {
// 				c.logger.Warningf("Batch timer expired with no pending requests, this might indicate a bug")
// 				continue
// 			}

// 			c.logger.Debugf("Batch timer expired, creating block")
// 			c.propose(propC, bc, batch) // we are certain this is normal block, no need to block

// 		case sn := <-c.snapC:
// 			if sn.Metadata.Index != 0 {
// 				if sn.Metadata.Index <= c.appliedIndex {
// 					c.logger.Debugf("Skip snapshot taken at index %d, because it is behind current applied index %d", sn.Metadata.Index, c.appliedIndex)
// 					break
// 				}

// 				c.confState = sn.Metadata.ConfState
// 				c.appliedIndex = sn.Metadata.Index
// 			} else {
// 				c.logger.Infof("Received artificial snapshot to trigger catchup")
// 			}

// 			if err := c.catchUp(sn); err != nil {
// 				c.logger.Panicf("Failed to recover from snapshot taken at Term %d and Index %d: %s",
// 					sn.Metadata.Term, sn.Metadata.Index, err)
// 			}

// 		case <-c.doneC:
// 			cancelProp()

// 			select {
// 			case <-c.errorC: // avoid closing closed channel
// 			default:
// 				close(c.errorC)
// 			}

// 			c.logger.Infof("Stop serving requests")
// 			c.periodicChecker.Stop()
// 			return
// 		}
// 	}
// }

func (c *Chain) writeBlock(block *common.Block, index uint64) {
	if block.Header.Number > c.lastBlock.Header.Number+1 {
		c.logger.Panicf("Got block [%d], expect block [%d]", block.Header.Number, c.lastBlock.Header.Number+1)
	} else if block.Header.Number < c.lastBlock.Header.Number+1 {
		c.logger.Infof("Got block [%d], expect block [%d], this node was forced to catch up", block.Header.Number, c.lastBlock.Header.Number+1)
		return
	}

	if c.blockInflight > 0 {
		c.blockInflight-- // only reduce on leader
	}
	c.lastBlock = block

	c.logger.Infof("Writing block [%d] (Raft index: %d) to ledger", block.Header.Number, index)

	if utils.IsConfigBlock(block) {
		c.writeConfigBlock(block, index)
		return
	}

	c.raftMetadataLock.Lock()
	c.opts.BlockMetadata.RaftIndex = index
	m := utils.MarshalOrPanic(c.opts.BlockMetadata)
	c.raftMetadataLock.Unlock()

	c.support.WriteBlock(block, m)
}

//zs
// func (c *Chain) GnodeCompare(gnodeNew *GNode, gnodeOld *GNode) bool {

// 	txRWSet:=gnodeNew.TxRWSet
// 	txId:=gnodeNew.TxId

// 	for _,rwset:=range txRWSet.NsRwSets{
// 		logger.Infof("zs: gettx txid:%s namespace:%s ",txId,rwset.NameSpace)
// 		if strings.Compare(rwset.NameSpace,"lscc")!=0{
// 			for _,rkv:=range rwset.KvRwSet.Reads{
// 				rkValue:=string(rkv.Value)
// 				if strings.Compare(rkValue,"onlywrite")==0{

// 					logger.Infof("zs: gettx txid:%s onlywrite rtxid:%s rtxcount:%d namespace:%s rk:%s rv:%s rtxid:%s",txId,rkv.Txid,rkv.Version.TxNum,rwset.NameSpace,rkv.Key,string(rkv.Value),rkv.Txid)
// 				}else if strings.Compare(rkValue,"onlyread")==0{

// 					logger.Infof("zs: gettx txid:%s onlyread rtxid:%s rtxcount:%d namespace:%s rk:%s rv:%s rtxid:%s",txId,rkv.Txid,rkv.Version.TxNum,rwset.NameSpace,rkv.Key,string(rkv.Value),rkv.Txid)
// 				}else{
// 					logger.Infof("zs: gettx txid:%s normal namespace:%s rk:%s rv:%s rtxid:%s",txId,rwset.NameSpace,rkv.Key,string(rkv.Value),rkv.Txid)


// 					rkvOld,isExist:=gnodeOld.ReadSet[rkv.Key]
// 					if isExist{
// 						//if strings.Compare(string(rkv.Value),string(rkvOld.Value))!=0|| rkvOld.Type!=1{
// 						//	logger.Infof("zs: compare newtxid:%s k:%s v:%s ptxid:%s withExistTx rptxid:%s v:%s rkvBType:%d ",
// 						//		gnodeNew.TxId,rkv.Key,string(rkv.Value),rkv.Txid,rkvOld.ParentTxId,string(rkvOld.Value),rkvOld.Type)
// 						//	return false
// 						//}
// 						if strings.Compare(string(rkv.Value),string(rkvOld.Value))!=0 || strings.Compare(rkv.Txid,rkvOld.ParentTxId)!=0 || rkvOld.Type!=1{
// 							logger.Infof("zs: compare newtxid:%s k:%s v:%s ptxid:%s withExistTx rptxid:%s v:%s rkvBType:%d ",
// 								gnodeNew.TxId,rkv.Key,string(rkv.Value),rkv.Txid,rkvOld.ParentTxId,string(rkvOld.Value),rkvOld.Type)
// 							return false
// 						}
// 					}else{
// 						logger.Infof("zs: compare newtxid:%s k:%s v:%s  with NotExistTx in Oldtx",gnodeNew.TxId,rkv.Key,string(rkv.Value))
// 						return false
// 					}


// 				}
// 			}




// 		}
// 	}
// 	return true
// }
// func (c *Chain) GConAddNode(gnodeA *GNode) bool {

// 	txRWSet:=gnodeA.TxRWSet
// 	txId:=gnodeA.TxId
// 	for _,rwset:=range txRWSet.NsRwSets{
// 		logger.Infof("zs: gettx txid:%s namespace:%s ",txId,rwset.NameSpace)
// 		if strings.Compare(rwset.NameSpace,"lscc")!=0{
// 			for _,rkv:=range rwset.KvRwSet.Reads{
// 				rkValue:=string(rkv.Value)
// 				childNodeType:=int64(0)
// 				if strings.Compare(rkValue,"onlywrite")==0{
// 					childNodeType=2
// 					logger.Infof("zs: gettx txid:%s onlywrite rtxid:%s rtxcount:%d namespace:%s rk:%s rv:%s rtxid:%s",txId,rkv.Txid,rkv.Version.TxNum,rwset.NameSpace,rkv.Key,string(rkv.Value),rkv.Txid)
// 				}else if strings.Compare(rkValue,"onlyread")==0{
// 					childNodeType=3
// 					logger.Infof("zs: gettx txid:%s onlyread rtxid:%s rtxcount:%d namespace:%s rk:%s rv:%s rtxid:%s",txId,rkv.Txid,rkv.Version.TxNum,rwset.NameSpace,rkv.Key,string(rkv.Value),rkv.Txid)
// 				}else{
// 					childNodeType=1
// 					logger.Infof("zs: gettx txid:%s normal namespace:%s rk:%s rv:%s rtxid:%s",txId,rwset.NameSpace,rkv.Key,string(rkv.Value),rkv.Txid)
// 				}
// 				readKV:=&ReadKV{
// 					Key:        rkv.Key,
// 					Value:      rkv.Value,
// 					Type:       childNodeType,
// 					ParentTxId: rkv.Txid,
// 				}



// 				if childNodeType!=1{
// 					readKV.Key=rkv.Key[:len(rkv.Key)-len(rkv.Txid)-1]
// 				}


// 				gnodeA.ReadSet[rkv.Key]=readKV








// 			}

// 			for _,wkv:=range rwset.KvRwSet.Writes{
// 				_,isInR:=gnodeA.ReadSet[wkv.Key]
// 				if isInR==true{
// 					logger.Infof("zs: gettx txid:%s normal namespace:%s wk:%s wv:%s InR",txId,rwset.NameSpace,wkv.Key,string(wkv.Value))
// 					gnodeA.WriteSet[wkv.Key]=&WriteKV{
// 						Key:   wkv.Key,
// 						Value: wkv.Value,
// 						Type:  1,
// 					}
// 				}else{
// 					if len(c.GCon.GTxWeight)>5{
// 						logger.Infof("zs: gettx txid:%s normal namespace:%s wk:%s wv:%s OutR",txId,rwset.NameSpace,wkv.Key,string(wkv.Value))
// 					}
// 					gnodeA.WriteSet[wkv.Key]=&WriteKV{
// 						Key:   wkv.Key,
// 						Value: wkv.Value,
// 						Type:  0,
// 					}
// 				}

// 			}


// 		}
// 	}



// 	gnodeResult,isExistgnode:=c.GCon.GTxRelusts[txId]
// 	if isExistgnode==false{
// 		//zs:chuanxing
// 		c.GCon.GTxRelusts[txId]=&GNodeResult{
// 			GnodeResult: gnodeA,
// 			Next:        nil,
// 			State: 2,
// 		}
// 		c.GCon.GnodeMaxResults[txId]=&GNodeResult{
// 			GnodeResult: gnodeA,
// 			Next:        nil,
// 			State:       2,
// 		}
// 	}else{
// 		c.GCon.GTxRelusts[txId].Next=&GNodeResult{
// 			GnodeResult: gnodeResult.GnodeResult,
// 			Next:        gnodeResult.Next,
// 			State:       gnodeResult.State,
// 		}
// 		c.GCon.GTxRelusts[txId].GnodeResult=gnodeA

// 		if c.GCon.GnodeMaxResults[txId].GnodeResult.State==3{
// 			logger.Infof("zs: h.GCon.GnodeMaxResultsChange txid:%s txcount:%d  with oldTx txcount:%d",gnodeA.TxId,gnodeA.TxCount,c.GCon.GnodeMaxResults[txId].GnodeResult.TxCount)
// 			c.GCon.GnodeMaxResults[txId].GnodeResult=gnodeA
// 		}
// 	}
// 	return true

// }
// type UnChooseTx struct {
// 	ti time.Time
// 	childTx map[string]bool
// }

// func (c *Chain) DeleteGChooseNode(gnode *GNode, batches *[][]*common.Envelope, pending *bool)  {





// 	if gnode.IsCommitted==0{
// 		flagCommit:=true
// 		for _,rkv:=range gnode.ReadSet{
// 			ptxGChooseNode,isInGChooseNode:=c.GCon.GChooseNode[rkv.ParentTxId]
// 			flagAddGChooseEdge:=false
// 			if isInGChooseNode==false{
// 				logger.Infof("zs: DeleteGChooseNode txid:%s rk:%s v:%s ptxid is not in GChooseNode ptxid:%s",gnode.TxId,rkv.Key,string(rkv.Value),rkv.ParentTxId)
// 				flagAddGChooseEdge=true
// 				_,ptxIsExist:=c.GCon.GChooseNodeWaitPtx[rkv.ParentTxId]
// 				if ptxIsExist==false{
// 					t1:=time.Now()
// 					c.GCon.GChooseNodeWaitPtx[rkv.ParentTxId]=&UnChooseTx{
// 						ti:t1,
// 						childTx: map[string]bool{},
// 					}
// 					c.GCon.GChooseNodeWaitPtx[rkv.ParentTxId].childTx[gnode.TxId]=true
// 					gnode.WaitPTX[rkv.ParentTxId]=true
// 					logger.Infof("zs: txid:%s Add c.GCon.GChooseNodeWaitPtx[%s]=[%v]",gnode.TxId,rkv.ParentTxId,t1)
// 					logger.Infof("zs: txid:%s gnode.WaitPTX[%s]=[%v]",gnode.TxId,rkv.ParentTxId,time.Now())
// 				}
// 			}else{
// 				logger.Infof("zs: DeleteGChooseNode txid:%s rk:%s v:%s ptxid is already in GChooseNode ptxid:%s",gnode.TxId,rkv.Key,string(rkv.Value),rkv.ParentTxId)
// 				if ptxGChooseNode.IsCommitted==1{
// 					logger.Infof("zs: DeleteGChooseNode txid:%s rk:%s v:%s ptxid is already in GChooseNode already committed ptxid:%s",gnode.TxId,rkv.Key,string(rkv.Value),rkv.ParentTxId)
// 				}else{
// 					logger.Infof("zs: DeleteGChooseNode txid:%s rk:%s v:%s ptxid is already in GChooseNode but not committed ptxid:%s",gnode.TxId,rkv.Key,string(rkv.Value),rkv.ParentTxId)
// 					flagAddGChooseEdge=true

// 					for wptx,_:=range ptxGChooseNode.WaitPTX{
// 						gnode.WaitPTX[wptx]=true
// 						c.GCon.GChooseNodeWaitPtx[wptx].childTx[gnode.TxId]=true
// 						logger.Infof("zs: txid:%s gnode.WaitPTX[%s]=[%v]",gnode.TxId,wptx,time.Now())
// 					}

// 				}
// 			}
// 			if flagAddGChooseEdge==true{
// 				flagCommit=false
// 				gnode.NumPGChooseNode++
// 				childnode, isExist := c.GCon.GChooseEdgeSet[rkv.ParentTxId]
// 				if isExist ==true{
// 					c.GCon.GChooseEdgeSet[rkv.ParentTxId].Next=&GChooseEdge{
// 						ChildNode:   childnode.ChildNode,
// 						Next:        childnode.Next,
// 						State:       2,
// 						IsCommitted: 0,
// 					}

// 					c.GCon.GChooseEdgeSet[rkv.ParentTxId].ChildNode=gnode
// 				} else {
// 					c.GCon.GChooseEdgeSet[rkv.ParentTxId]=&GChooseEdge{
// 						ChildNode:   gnode,
// 						Next:        nil,
// 						State:       2,
// 						IsCommitted: 0,
// 					}
// 				}
// 			}

// 		}
// 		if flagCommit==true{

// 			logger.Infof("zs: committed inblock success txid:%s ", gnode.TxId)
// 			//batches, pending = c.support.BlockCutter().Ordered(msg.Payload)
// 			tempbatches := [][]*common.Envelope{}
// 			tempbatches, *pending = c.support.BlockCutter().Ordered(gnode.TxResult)
// 			logger.Infof("zs: committed inblock success txid:%s lenTempbatches:%d", gnode.TxId,len(tempbatches))
// 			c.GCon.NumSendInBlock++
// 			gnode.IsCommitted=1
// 			c.GCon.NumGChooseEdge++
// 			if len(tempbatches)!=0{
// 				for _,batch:= range tempbatches{
// 					*batches=append(*batches,batch)
// 					logger.Infof("zs: committed inblock batches txid:%s lenbatches:%d addbatch:%d", gnode.TxId,len(*batches),len(batch))
// 				}
				
// 			}
// 			unchoosetx,txIsExist:=c.GCon.GChooseNodeWaitPtx[gnode.TxId]
// 			if txIsExist==true{
// 				for childTx,_:=range unchoosetx.childTx{
// 					delete(c.GCon.GChooseNode[childTx].WaitPTX,gnode.TxId)
// 				}
// 				logger.Infof("zs: txid:%s DeleteWaitNode committed c.GCon.GChooseNodeWaitPtx[%s]: childtx[%d] ti[%v]at time[%v] lenmap:%d",
// 				gnode.TxId,gnode.TxId,len(unchoosetx.childTx),unchoosetx.ti,time.Now(),len(c.GCon.GChooseNodeWaitPtx))
// 				delete(c.GCon.GChooseNodeWaitPtx,gnode.TxId)
// 			}
// 			c.DeleteGChooseEdge(gnode,batches,pending)
// 		}else{
// 			unchoosetx,txIsExist:=c.GCon.GChooseNodeWaitPtx[gnode.TxId]
// 			if txIsExist==true{
// 				for wptx,_:=range gnode.WaitPTX{
// 					logger.Infof("zs: txid:%s DeleteWaitNode Before c.GCon.GChooseNodeWaitPtx[%s]: childtx[%d] ti[%v]  to wptx[%s]-ctx[%d]  at time[%v] lenmap:%d",
// 					gnode.TxId,gnode.TxId,len(unchoosetx.childTx),unchoosetx.ti,wptx,len(c.GCon.GChooseNodeWaitPtx[wptx].childTx),time.Now(),len(c.GCon.GChooseNodeWaitPtx))

// 				}
// 				for childTx,_:=range unchoosetx.childTx{
// 					delete(c.GCon.GChooseNode[childTx].WaitPTX,gnode.TxId)
// 					for wptx,_:=range gnode.WaitPTX{
// 						c.GCon.GChooseNode[childTx].WaitPTX[wptx]=true
// 						c.GCon.GChooseNodeWaitPtx[wptx].childTx[childTx]=true
// 					}
// 				}
// 				for wptx,_:=range gnode.WaitPTX{
// 					logger.Infof("zs: txid:%s DeleteWaitNode After c.GCon.GChooseNodeWaitPtx[%s]: childtx[%d] ti[%v]  to wptx[%s]-ctx[%d]  at time[%v] lenmap:%d",
// 					gnode.TxId,gnode.TxId,len(unchoosetx.childTx),unchoosetx.ti,wptx,len(c.GCon.GChooseNodeWaitPtx[wptx].childTx),time.Now(),len(c.GCon.GChooseNodeWaitPtx))

// 				}
// 				delete(c.GCon.GChooseNodeWaitPtx,gnode.TxId)
// 			}
// 		}
// 		if c.GCon.NumGChooseNode%100==0{
// 			for k,v:=range c.GCon.GChooseNodeWaitPtx{
// 				logger.Infof("zs:#### NumGCNode:%d  c.GCon.GChooseNodeWaitPtx[%s]: childTx[%d] \tti[%v] lenmap:%d ####\n",c.GCon.NumGChooseNode,k,len(v.childTx),v.ti,len(c.GCon.GChooseNodeWaitPtx))
// 			}

// 		}
// 	}

// }

// func (c *Chain) DeleteGChooseEdge(gnode *GNode, batches *[][]*common.Envelope, pending *bool)  {
// 	childTx,isExist:=c.GCon.GChooseEdgeSet[gnode.TxId]
// 	logger.Infof("zs: DeleteGChooseEdge Ptxid:%s BE",gnode.TxId)
// 	if isExist==true && c.GCon.GChooseEdgeSet[gnode.TxId].IsCommitted!=1{
// 		c.GCon.GChooseEdgeSet[gnode.TxId].IsCommitted=1
// 		for childTx!=nil{
// 			childTx.ChildNode.NumPGChooseNode--
// 			logger.Infof("zs: DeleteGChooseEdge Ptxid:%s childTx:%s childPtxNum:%d",gnode.TxId,childTx.ChildNode.TxId,childTx.ChildNode.NumPGChooseNode)
// 			if childTx.ChildNode.NumPGChooseNode==0 && childTx.ChildNode.IsCommitted!=1{
// 				logger.Infof("zs: DeleteGChooseEdge Ptxid:%s DeleteGChoosenode BE childTx:%s childPtxNum:%d",gnode.TxId,childTx.ChildNode.TxId,childTx.ChildNode.NumPGChooseNode)
// 				c.DeleteGChooseNode(childTx.ChildNode,batches,pending)
// 				logger.Infof("zs: DeleteGChooseEdge Ptxid:%s DeleteGChoosenode EN childTx:%s childPtxNum:%d",gnode.TxId,childTx.ChildNode.TxId,childTx.ChildNode.NumPGChooseNode)
// 			}
// 			childTx=childTx.Next
// 		}
// 	}

// 	logger.Infof("zs: DeleteGChooseEdge Ptxid:%s EN",gnode.TxId)
// }

//zs0426
func (c *Chain) GVConAddUnchooseGVNode(gnodeA *partialorder.GVNode) {
	txRWSet:=gnodeA.TxRWSet
	txId:=gnodeA.TxId
	unchooseGVNode,_:=c.GVCon.GUnchoosedGVNode[txId]
	// var NumgnodeAConsensusRKV int64
	// NumgnodeAConsensusRKV=0
	for _,rwset:=range txRWSet.NsRwSets{
		logger.Infof("zs: gettx txid:%s namespace:%s ",txId,rwset.NameSpace)
		if strings.Compare(rwset.NameSpace,"lscc")!=0{
			for _,rkv:=range rwset.KvRwSet.Reads{
				rkValue:=string(rkv.Value)
				if rkv.IsRead==true{
					gvreadKV:=&partialorder.GVReadKV{
						Key:        rkv.Key,
						Value:      rkValue,
						//Type:       1,
						ParentTxId: rkv.Txid,
					}
					gnodeA.ReadSet[rkv.Key]=gvreadKV
	
					ptmap,isExistptmap:=gnodeA.PTx[rkv.Txid]
					if isExistptmap==false{
						gnodeA.PTx[rkv.Txid]=&partialorder.PtxKV{
							KV: make(map[string]string),
							//Value map[string]string
							Type: make(map[string]int64),  
						}
						ptmap=gnodeA.PTx[rkv.Txid]
					}
					ptmap.KV[rkv.Key]=rkValue
					ptmap.Type[rkv.Key]=1
					logger.Infof("zs: gettx txid:%s normal namespace:%s rk:%s rv:%s risRead:%v rtxid:%s type:%d",
					txId,rwset.NameSpace,rkv.Key,string(rkv.Value),rkv.IsRead,rkv.Txid,ptmap.Type[rkv.Key])
				}else{
					unpv,isExistunpv:=unchooseGVNode.UNPTxVotes[rkv.Key]
					if isExistunpv==false{
						unpv=0
					}
					unchooseGVNode.UNPTxVotes[rkv.Key]=unpv+1



					newk:=rkv.Key[:len(rkv.Key)-len(rkv.Txid)]
					unptmap,isExistunptmap:=unchooseGVNode.UNPTx[rkv.Txid]
					if isExistunptmap==false{
						unchooseGVNode.UNPTx[rkv.Txid]=&partialorder.PtxKV{
							KV: make(map[string]string),
							//Value map[string]string
							Type: make(map[string]int64),  
						}
						unptmap=unchooseGVNode.UNPTx[rkv.Txid]
					}
					unptmap.KV[newk]=rkValue
					unptmap.Type[newk]=4
					logger.Infof("zs: gettx txid:%s normal namespace:%s rk:%s rv:%s risRead:%v rtxid:%s type:%d",
					txId,rwset.NameSpace,newk,string(rkv.Value),rkv.IsRead,rkv.Txid,unptmap.Type[newk])

					gunptmap,isExistgunptmap:=gnodeA.UNPTx[rkv.Txid]
					if isExistgunptmap==false{
						gnodeA.UNPTx[rkv.Txid]=&partialorder.PtxKV{
							KV: make(map[string]string),
							//Value map[string]string
							Type: make(map[string]int64),  
						}
						gunptmap=gnodeA.UNPTx[rkv.Txid]
					}
					gunptmap.KV[newk]=rkValue
					gunptmap.Type[newk]=4
					//logger.Infof("zs: gettx txid:%s normal namespace:%s rk:%s rv:%s risRead:%v rtxid:%s type:%d",
					//txId,rwset.NameSpace,newk,string(rkv.Value),rkv.IsRead,rkv.Txid,gunptmap.Type[newk])





					// if rkv.Version!=nil{
					// 	newk:=rkv.Key[:len(rkv.Key)-len(rkv.Txid)]
					// 	unptmap,isExistunptmap:=gnodeA.UNPTx[rkv.Txid]
					// 	if isExistunptmap==false{
					// 		gnodeA.UNPTx[rkv.Txid]=&partialorder.PtxKV{
					// 			KV: make(map[string]string),
					// 			//Value map[string]string
					// 			Type: make(map[string]int64),  
					// 		}
					// 		unptmap=gnodeA.UNPTx[rkv.Txid]
					// 	}
					// 	unptmap.KV[newk]=rkValue
					// 	unptmap.Type[newk]=4
					// 	logger.Infof("zs: gettx txid:%s normal namespace:%s rk:%s rv:%s risRead:%v rtxid:%s type:%d",
					// 	txId,rwset.NameSpace,newk,string(rkv.Value),rkv.IsRead,rkv.Txid,unptmap.Type[newk])

					// }else{
					// 	newk:=rkv.Key[:len(rkv.Key)-len(rkv.Txid)]
					// 	unptmap,isExistunptmap:=gnodeA.UNPTx[rkv.Txid]
					// 	if isExistunptmap==false{
					// 		gnodeA.UNPTx[rkv.Txid]=&partialorder.PtxKV{
					// 			KV: make(map[string]string),
					// 			//Value map[string]string
					// 			Type: make(map[string]int64),  
					// 		}
					// 		unptmap=gnodeA.UNPTx[rkv.Txid]
					// 	}
					// 	unptmap.KV[newk]=rkValue
					// 	unptmap.Type[newk]=4
					// 	logger.Infof("zs: gettx txid:%s normal namespace:%s rk:%s rv:%s risRead:%v rtxid:%s type:%d",
					// 	txId,rwset.NameSpace,newk,string(rkv.Value),rkv.IsRead,rkv.Txid,unptmap.Type[newk])
					// }


				}
			}
			for _,wkv:=range rwset.KvRwSet.Writes{
				gnodeA.WriteSet[wkv.Key]=&partialorder.GVWriteKV{
					Key:   wkv.Key,
					Value: string(wkv.Value),
					//Type:  1,
					ParentTxId: wkv.Txid,
				}
				// if strings.Compare(wkv.Txid,txId)!=0{
				// 	_,isInR:=gnodeA.ReadSet[wkv.Key]
				// 	if isInR==true{
				// 		gnodeA.PTx[wkv.Txid].Type[wkv.Key]=2
				// 		if len(gnodeA.ReadSet)>=2{
				// 			logger.Infof("zs: gettx txid:%s normal namespace:%s wk:%s wv:%s type:%d",txId,rwset.NameSpace,wkv.Key,string(wkv.Value),gnodeA.PTx[wkv.Txid].Type[wkv.Key])
				// 		}
				// 	}else{
				// 		// unpv,isExistunpv:=unchooseGVNode.UNPTxVotes[wkv.Key+wkv.Txid]
				// 		// if isExistunpv==false{
				// 		// 	//unchooseGVNode.UNPTxVotes[rkv.Txid]=0
				// 		// 	unpv=0
				// 		// }
				// 		// unchooseGVNode.UNPTxVotes[wkv.Key+wkv.Txid]=unpv+1
				// 		// ptmap,isExistptmap:=gnodeA.PTx[wkv.Txid]
				// 		// if isExistptmap==false{
				// 		// 	gnodeA.PTx[wkv.Txid]=&partialorder.PtxKV{
				// 		// 		KV: make(map[string]string),
				// 		// 		//Value map[string]string
				// 		// 		Type: make(map[string]int64),  
				// 		// 	}
				// 		// 	ptmap=gnodeA.PTx[wkv.Txid]
				// 		// }
				// 		// ptmap.KV[wkv.Key]=""
				// 		// ptmap.Type[wkv.Key]=3	
				// 		// if len(gnodeA.ReadSet)>=2{
				// 		// 	logger.Infof("zs: gettx txid:%s normal namespace:%s wk:%s wv:%s type:%d",txId,rwset.NameSpace,wkv.Key,string(wkv.Value),gnodeA.PTx[wkv.Txid].Type[wkv.Key])
				// 		// }	
				// 		// unptmap,isExistunptmap:=gnodeA.UNPTx[wkv.Txid]
				// 		// if isExistunptmap==false{
				// 		// 	gnodeA.UNPTx[wkv.Txid]=&partialorder.PtxKV{
				// 		// 		KV: make(map[string]string),
				// 		// 		//Value map[string]string
				// 		// 		Type: make(map[string]int64),  
				// 		// 	}
				// 		// 	unptmap=gnodeA.UNPTx[wkv.Txid]
				// 		// }
				// 		// unptmap.KV[wkv.Key]=""
				// 		// unptmap.Type[wkv.Key]=3	
				// 		// if len(gnodeA.ReadSet)>=2{
				// 		// 	logger.Infof("zs: gettx txid:%s normal namespace:%s wk:%s wv:%s type:%d",txId,rwset.NameSpace,wkv.Key,string(wkv.Value),gnodeA.UNPTx[wkv.Txid].Type[wkv.Key])
				// 		// }					
				// 	}

				// }else{
				// 	if len(gnodeA.ReadSet)>=2{
				// 		logger.Infof("zs: gettx txid:%s normal namespace:%s wk:%s wv:%s createkey",txId,rwset.NameSpace,wkv.Key,string(wkv.Value))
				// 	}
				// }
			}
		}
	}

	for rk,rkv:=range gnodeA.ReadSet{
		logger.Infof("zs: gettx @@txid:%s rk:%s rv:%s \n",txId,rk,string(rkv.Value))
	}
	if len(gnodeA.ReadSet)>=2{
		for wk,wkv:=range gnodeA.WriteSet{
			logger.Infof("zs: gettx @@txid:%s wk:%s wv:%s \n",txId,wk,string(wkv.Value))
		}
	}
	for ptxid,ptx:=range gnodeA.PTx{
		for ptxk,ptxkv:=range ptx.KV{
			logger.Infof("zs: gettx @@txid:%s ptxid:%s  pk:%s pv:%s ptype:%d\n",txId,ptxid,ptxk,ptxkv,ptx.Type[ptxk])
		}
	}
	for ptxid,ptx:=range gnodeA.UNPTx{
		for ptxk,ptxkv:=range ptx.KV{
			logger.Infof("zs: gettx @@txid:%s UNptxid:%s  UNpk:%s UNpv:%s UNptype:%d\n",txId,ptxid,ptxk,ptxkv,ptx.Type[ptxk])
		}
	}






	//logger.Infof("zs: gettx txid:%s NumgnodeAConsensusRKV:%d lenRKV:%d",txId,NumgnodeAConsensusRKV,int64(len(unchooseGVNode.ReadVotes)))
	//c.GVCon.GUnchoosedGVNode[txId].ValueGVNode=append(unchooseGVNode.ValueGVNode,gvnode)
	flagRKVSame:=true
	flagRKVPtxSame:=true
	flagAppend:=true
	for _,g:=range *unchooseGVNode.ValueGVNode{
		flagRKVSame=true
		for _,rkv:=range gnodeA.ReadSet{
			grkv,isExistgrkv:=g.ReadSet[rkv.Key]
			if isExistgrkv==false || strings.Compare(grkv.Value,rkv.Value)!=0{
				flagRKVSame=false
				flagRKVPtxSame=false
				break
			}
		}
		if flagRKVSame==true{
			g.NumSameRKV++
			gnodeA.NumSameRKV=g.NumSameRKV
			// if gnodeA.NumSameRKV==2{
			// 	c.GVCon.NumRKVVotes2++
			// }else if gnodeA.NumSameRKV==3{
			// 	c.GVCon.NumRKVVotes3++
			// }else if gnodeA.NumSameRKV==4{
			// 	c.GVCon.NumRKVVotes4++
			// }else if gnodeA.NumSameRKV==5{
			// 	c.GVCon.NumRKVVotes5++
			// }
			
			flagRKVPtxSame=true
			for ptxid,ptx:=range gnodeA.PTx{
				gptx,isExistgptxid:=g.PTx[ptxid]
				if isExistgptxid==false{
					logger.Infof("zs: gettx txid:%s ptxid:%s vs g not exist this ptx\n",txId,ptxid)
					flagRKVPtxSame=false
					break
				}else{
					for ptxk,ptxv:=range ptx.KV{
						gptxv,isExistgptxkv:=gptx.KV[ptxk]
						if isExistgptxkv==false{
							logger.Infof("zs: gettx txid:%s ptxid:%s pk:%s pv:%s type:%d vs g not exist this key\n",
							txId,ptxid,ptxk,ptxv,ptx.Type[ptxk])
							flagRKVPtxSame=false
							break
						}else{
							logger.Infof("zs: gettx txid:%s ptxid:%s pk:%s pv:%s type:%d vs g gpk:%s gpv:%s gtype:%d\n",
							txId,ptxid,ptxk,ptxv,ptx.Type[ptxk],ptxk,gptxv,gptx.Type[ptxk])
							if strings.Compare(ptxv,gptxv)!=0 || ptx.Type[ptxk]!=gptx.Type[ptxk]{
								flagRKVPtxSame=false
								break
							}
						}

					}
				}
			}
			if flagRKVPtxSame==true{
				flagAppend=false
				g.NumSameRKVPtx++
				// if g.NumSameRKVPtx==2{
				// 	c.GVCon.NumVotes2++
				// }else if g.NumSameRKVPtx==3{
				// 	c.GVCon.NumVotes3++
				// }else if g.NumSameRKVPtx==4{
				// 	c.GVCon.NumVotes4++
				// }else if g.NumSameRKVPtx==5{
				// 	c.GVCon.NumVotes5++
				// }
				if g.NumSameRKVPtx>=c.GVCon.NumKWeight&&unchooseGVNode.ChoosedGVNode==nil{
					logger.Infof("zs: ##GVConAddUnchooseGVNode txid:%s Choosed NumSameRKV:%d NumSameRKVPtx:%d\n",txId,g.NumSameRKV,g.NumSameRKVPtx)
					c.GVCon.NumGChooseNode++
					unchooseGVNode.ChoosedGVNode=g
					g.ConsensusState=1
					if c.GVCon.GUnchoosedGVNode[txId].ChoosedGVNode==nil{
						logger.Infof("zs: GVConAddUnchooseGVNode txid:%s Choosed NumSameRKV:%d NumSameRKVPtx:%d ERROR\n",txId,g.NumSameRKV,g.NumSameRKVPtx)
					}else{
						logger.Infof("zs: GVConAddUnchooseGVNode txid:%s Choosed NumSameRKV:%d NumSameRKVPtx:%d SUCCESS\n",txId,g.NumSameRKV,g.NumSameRKVPtx)
					}
				}else{
					logger.Infof("zs: GVConAddUnchooseGVNode txid:%s NumSameRKV:%d NumSameRKVPtx:%d flagRKVPtxSame==true",txId,g.NumSameRKV,g.NumSameRKVPtx)
				}
				//break
			}
		}
	}
	if len(*unchooseGVNode.ValueGVNode)==0||flagAppend==true{
		*unchooseGVNode.ValueGVNode=append(*unchooseGVNode.ValueGVNode,gnodeA)
		logger.Infof("zs: GVConAddUnchooseGVNode txid:%s NumSameRKV:%d NumSameRKVPtx:%d flagAppend==false",txId,gnodeA.NumSameRKV,gnodeA.NumSameRKVPtx)
	}

	// if unchooseGVNode.NumConsensusRKV == int64(len(unchooseGVNode.ReadVotes)) && unchooseGVNode.AllVotes>=c.GVCon.NumKWeight{
	// 	if unchooseGVNode.ChoosedGVNode==nil{

	// 		if NumgnodeAConsensusRKV==int64(len(unchooseGVNode.ReadVotes)){


	// 			// unchooseGVNode.ChoosedGVNode=gnodeA
	// 			// logger.Infof("zs: gettx txid:%s NumgnodeAConsensusRKV:%d lenRKV:%d same CreateVotes:%d",
	// 			// txId,NumgnodeAConsensusRKV,int64(len(unchooseGVNode.ReadVotes)),unchooseGVNode.ChoosedGVNode.Votes)
	// 			// for _,rkv:=range gnodeA.ReadSet{
	// 			// 	logger.Infof("zs: ConsensusTxid:%s rk:%s rv:%s rtxid:%s\n",txId,rkv.Key,string(rkv.Value),rkv.ParentTxId)
	// 			// }
	// 			// if len(gnodeA.ReadSet)>=2{
	// 			// 	for _,wkv:=range gnodeA.WriteSet{
	// 			// 		logger.Infof("zs: ConsensusTxid:%s wk:%s wv:%s \n",txId,wkv.Key,string(wkv.Value))
	// 			// 	}
	// 			// }
	// 			//compare
				
	// 			//unchooseGVNode.ChoosedGVNode.Votes=0

	// 		}else{
	// 			logger.Infof("zs: gettx txid:%s NumgnodeAConsensusRKV:%d lenRKV:%d nil but diff",txId,NumgnodeAConsensusRKV,int64(len(unchooseGVNode.ReadVotes)))
	// 		}
		
		
		
	// 	}else{
	// 		if NumgnodeAConsensusRKV==int64(len(unchooseGVNode.ReadVotes)){
	// 			unchooseGVNode.ChoosedGVNode.Votes++
	// 			logger.Infof("zs: gettx txid:%s NumgnodeAConsensusRKV:%d lenRKV:%d same SameVotes:%d",
	// 			txId,NumgnodeAConsensusRKV,int64(len(unchooseGVNode.ReadVotes)),unchooseGVNode.ChoosedGVNode.Votes)
	// 			if unchooseGVNode.ChoosedGVNode.Votes==2{
	// 				c.GVCon.NumVotes2++
	// 			}else if unchooseGVNode.ChoosedGVNode.Votes==3{
	// 				c.GVCon.NumVotes3++
	// 			}else if unchooseGVNode.ChoosedGVNode.Votes==4{
	// 				c.GVCon.NumVotes4++
	// 			}else if unchooseGVNode.ChoosedGVNode.Votes==5{
	// 				c.GVCon.NumVotes5++
	// 			}
	// 		}else{
	// 			logger.Infof("zs: gettx txid:%s NumgnodeAConsensusRKV:%d lenRKV:%d diff SameVotes:%d",
	// 			txId,NumgnodeAConsensusRKV,int64(len(unchooseGVNode.ReadVotes)),unchooseGVNode.ChoosedGVNode.Votes)
	// 		}
	// 	}
	// }
}

func (c *Chain) SelectRKVConsensusGVNode(unchoosedGVNode *partialorder.UnchoosedGVNode){
	unchoosedGVNode.ChoosedGVNode=nil
	for _,gvnode:=range *unchoosedGVNode.ValueGVNode{
		logger.Infof("zs: SelectRKVConsensusGVNode txid:%s NumRKV:%d NumRKVPTX:%d",
		gvnode.TxId,gvnode.NumSameRKV,gvnode.NumSameRKVPtx)
		if gvnode.NumSameRKV>=c.GVCon.NumKWeight{
			if unchoosedGVNode.ChoosedGVNode==nil{
				unchoosedGVNode.ChoosedGVNode=gvnode
				gvnode.ConsensusState=2

				logger.Infof("zs: SelectRKVConsensusGVNode txid:%s NumRKV:%d NumRKVPTX:%d choose",
				gvnode.TxId,gvnode.NumSameRKV,gvnode.NumSameRKVPtx)



				gvnode.UNPTx= make(map[string]*partialorder.PtxKV)
				for unkptxid,unv:=range unchoosedGVNode.UNPTxVotes{
					newptxid:=unkptxid[len(unkptxid)-len(gvnode.TxId):]
					//if unv>=c.GVCon.NumKWeight || c.GVCon.GUnchoosedGVNode[newptxid].Count<unchoosedGVNode.Count{
					if unv>=c.GVCon.NumKWeight{
						gvnode.UNPTx[newptxid]=unchoosedGVNode.UNPTx[newptxid]
					}
				}






				for gptxid,_:=range gvnode.PTx{
					logger.Infof("zs: SelectRKVConsensusGVNode BEFORE txid:%s ptxid:%s",
					gvnode.TxId,gptxid)
				}
				for ungptxid,_:=range gvnode.UNPTx{
					logger.Infof("zs: SelectRKVConsensusGVNode BEFORE txid:%s unptxid:%s",
					gvnode.TxId,ungptxid)
				}



			}else{
				for gptxid,gptx:=range gvnode.PTx{
					//gptxnode:=

					if c.GVCon.GUnchoosedGVNode[gptxid].Count>unchoosedGVNode.Count{
						logger.Infof("zs: SelectRKVConsensusGVNode txid:%s addptxid:%s false txcount:%d ptxcount:%d",
						gvnode.TxId,gptxid,unchoosedGVNode.Count,c.GVCon.GUnchoosedGVNode[gptxid].Count)
						continue
					}

					_,isExistcptx:=unchoosedGVNode.ChoosedGVNode.PTx[gptxid]
					if isExistcptx==false{
						unchoosedGVNode.ChoosedGVNode.PTx[gptxid]=gptx
						logger.Infof("zs: SelectRKVConsensusGVNode txid:%s addptxid:%s",
					gvnode.TxId,gptxid)
					}
				}
				// for ungptxid,ungptx:=range gvnode.UNPTx{

				// 	if c.GVCon.GUnchoosedGVNode[ungptxid].Count>unchoosedGVNode.Count{
				// 		logger.Infof("zs: SelectRKVConsensusGVNode txid:%s addUNptxid:%s false txcount:%d UNptxcount:%d",
				// 		gvnode.TxId,ungptxid,unchoosedGVNode.Count,c.GVCon.GUnchoosedGVNode[ungptxid].Count)
				// 		continue
				// 	}

				// 	_,isExistuncptx:=unchoosedGVNode.ChoosedGVNode.UNPTx[ungptxid]
				// 	if isExistuncptx==false{
				// 		unchoosedGVNode.ChoosedGVNode.PTx[ungptxid]=ungptx
				// 		logger.Infof("zs: SelectRKVConsensusGVNode txid:%s addUNptxid:%s",
				// 	gvnode.TxId,ungptxid)
				// 	}
				// }
				
			}
		}
	}
	if unchoosedGVNode.ChoosedGVNode!=nil{
		cgvnode:=unchoosedGVNode.ChoosedGVNode
		for gptxid,_:=range cgvnode.PTx{
			logger.Infof("zs: SelectRKVConsensusGVNode END txid:%s ptxid:%s",
			cgvnode.TxId,gptxid)
		}
		for ungptxid,_:=range cgvnode.UNPTx{
			logger.Infof("zs: SelectRKVConsensusGVNode END txid:%s unptxid:%s",
			cgvnode.TxId,ungptxid)
		}
	}
}
func (c *Chain) CountNum(unchoosedGVNode *partialorder.UnchoosedGVNode){
	var maxnumrkv,maxnumrkvptx int64 
	maxnumrkv=0
	maxnumrkvptx=0
	var txid string
	for _,gvnode:=range *unchoosedGVNode.ValueGVNode{
		txid=gvnode.TxId
		if gvnode.NumSameRKV>maxnumrkv{
			maxnumrkv=gvnode.NumSameRKV
		}
		if gvnode.NumSameRKVPtx>maxnumrkvptx{
			maxnumrkvptx=gvnode.NumSameRKVPtx
		}
	}
	logger.Infof("zs: CountNum txid:%s MAXNumSameRKV:%d MAXNumSameRKVPtx:%d ",txid,maxnumrkv,maxnumrkvptx)

			if maxnumrkv>=2{
				c.GVCon.NumRKVVotes2++
			}
			if maxnumrkv>=3{
				c.GVCon.NumRKVVotes3++
			}
			if maxnumrkv>=4{
				c.GVCon.NumRKVVotes4++
			}
			if maxnumrkv>=5{
				c.GVCon.NumRKVVotes5++
			}

			if maxnumrkvptx>=2{
				c.GVCon.NumVotes2++
			}
			if maxnumrkvptx>=3{
				c.GVCon.NumVotes3++
			}
			if maxnumrkvptx>=4{
				c.GVCon.NumVotes4++
			}
			if maxnumrkvptx>=5{
				c.GVCon.NumVotes5++
			}
}
//zs0426
//zs0423
// func (c *Chain) GVConAddUnchooseGVNode(gnodeA *partialorder.GVNode) {
// 	txRWSet:=gnodeA.TxRWSet
// 	txId:=gnodeA.TxId
// 	unchooseGVNode,_:=c.GVCon.GUnchoosedGVNode[txId]
// 	var NumgnodeAConsensusRKV int64
// 	NumgnodeAConsensusRKV=0
// 	for _,rwset:=range txRWSet.NsRwSets{
// 		logger.Infof("zs: gettx txid:%s namespace:%s ",txId,rwset.NameSpace)
// 		if strings.Compare(rwset.NameSpace,"lscc")!=0{
// 			for _,rkv:=range rwset.KvRwSet.Reads{
// 				rkValue:=string(rkv.Value)
// 				logger.Infof("zs: gettx txid:%s normal namespace:%s rk:%s rv:%s rtxid:%s",txId,rwset.NameSpace,rkv.Key,string(rkv.Value),rkv.Txid)
// 				gvreadKV:=&partialorder.GVReadKV{
// 					Key:        rkv.Key,
// 					Value:      rkValue,
// 					Type:       1,
// 					ParentTxId: rkv.Txid,
// 				}
// 				gnodeA.ReadSet[rkv.Key]=gvreadKV
				
// 				rkVotes,isrkExist:=unchooseGVNode.ReadVotes[rkv.Key]
// 				if isrkExist==false{
// 					unchooseGVNode.ReadVotes[rkv.Key]=&partialorder.ValueVote{
// 					 	ValueSet: make(map[string]*partialorder.ValueVoteNode),
// 						MaxValueVoteNode: nil,
// 					}

// 					unchooseGVNode.ReadVotes[rkv.Key].ValueSet[rkValue]=&partialorder.ValueVoteNode{
// 						Vote: 1,
// 						Value: rkValue,
// 						ValueGVNode: []*partialorder.GVNode{},
// 					}
// 					valueGVNode:=unchooseGVNode.ReadVotes[rkv.Key].ValueSet[rkValue].ValueGVNode
// 					valueGVNode=append(valueGVNode,gnodeA)
// 				}else{
// 					rkvVotes,isrkvExist:=rkVotes.ValueSet[rkValue]
// 					if isrkvExist==false{
// 						rkVotes.ValueSet[rkValue]=&partialorder.ValueVoteNode{
// 							Vote: 1,
// 							Value: rkValue,
// 							ValueGVNode: []*partialorder.GVNode{},
// 						}
// 						valueGVNode:=rkVotes.ValueSet[rkValue].ValueGVNode
// 						valueGVNode=append(valueGVNode,gnodeA)
// 					}else{
// 						rkvVotes.Vote++
// 						rkvVotes.ValueGVNode=append(rkvVotes.ValueGVNode,gnodeA)
// 						if rkvVotes.Vote>=c.GVCon.NumKWeight && rkVotes.MaxValueVoteNode==nil{
// 							rkVotes.MaxValueVoteNode=rkvVotes
// 							unchooseGVNode.NumConsensusRKV++
// 						}
// 						if rkVotes.MaxValueVoteNode!=nil && strings.Compare(rkVotes.MaxValueVoteNode.Value,rkValue)==0{
// 							NumgnodeAConsensusRKV++
// 						}
// 					}
// 				}
// 			}
// 			for _,wkv:=range rwset.KvRwSet.Writes{
// 				_,isInR:=gnodeA.ReadSet[wkv.Key]
// 				if isInR==true{
// 					logger.Infof("zs: gettx txid:%s normal namespace:%s wk:%s wv:%s InR",txId,rwset.NameSpace,wkv.Key,string(wkv.Value))
// 					gnodeA.WriteSet[wkv.Key]=&partialorder.GVWriteKV{
// 						Key:   wkv.Key,
// 						Value: string(wkv.Value),
// 						Type:  1,
// 					}
// 				}else{
// 					if len(gnodeA.ReadSet)>=2{
// 						logger.Infof("zs: gettx txid:%s normal namespace:%s wk:%s wv:%s OutR",txId,rwset.NameSpace,wkv.Key,string(wkv.Value))
// 					}
// 					gnodeA.WriteSet[wkv.Key]=&partialorder.GVWriteKV{
// 						Key:   wkv.Key,
// 						Value: string(wkv.Value),
// 						Type:  0,
// 					}
// 				}

// 			}
// 		}
// 	}
// 	logger.Infof("zs: gettx txid:%s NumgnodeAConsensusRKV:%d lenRKV:%d",txId,NumgnodeAConsensusRKV,int64(len(unchooseGVNode.ReadVotes)))
// 	if unchooseGVNode.NumConsensusRKV == int64(len(unchooseGVNode.ReadVotes)) && unchooseGVNode.AllVotes>=c.GVCon.NumKWeight{
// 		if unchooseGVNode.ChoosedGVNode==nil{
// 			if NumgnodeAConsensusRKV==int64(len(unchooseGVNode.ReadVotes)){
// 				unchooseGVNode.ChoosedGVNode=gnodeA
// 				logger.Infof("zs: gettx txid:%s NumgnodeAConsensusRKV:%d lenRKV:%d same CreateVotes:%d",
// 				txId,NumgnodeAConsensusRKV,int64(len(unchooseGVNode.ReadVotes)),unchooseGVNode.ChoosedGVNode.Votes)
// 				for _,rkv:=range gnodeA.ReadSet{
// 					logger.Infof("zs: ConsensusTxid:%s rk:%s rv:%s rtxid:%s\n",txId,rkv.Key,string(rkv.Value),rkv.ParentTxId)
// 				}
// 				if len(gnodeA.ReadSet)>=2{
// 					for _,wkv:=range gnodeA.WriteSet{
// 						logger.Infof("zs: ConsensusTxid:%s wk:%s wv:%s \n",txId,wkv.Key,string(wkv.Value))
// 					}
// 				}

// 				//compare
// 				unchooseGVNode.ChoosedGVNode.Votes=0
// 				for _,g:=range unchooseGVNode.ValueGVNode{
// 					flagSameGVNode:=true

// 					for _,rkv:=range gnodeA.ReadSet{
// 						grkv,isExistgrkv:=g.ReadSet[rkv.Key]
// 						if isExistgrkv==false || strings.Compare(grkv.Value,rkv.Value)!=0{
// 							flagSameGVNode=false
// 							break
// 						}
// 					}
// 					if flagSameGVNode==true{
// 						unchooseGVNode.ChoosedGVNode.Votes++
// 						if unchooseGVNode.ChoosedGVNode.Votes==2{
// 							c.GVCon.NumVotes2++
// 						}else if unchooseGVNode.ChoosedGVNode.Votes==3{
// 							c.GVCon.NumVotes3++
// 						}else if unchooseGVNode.ChoosedGVNode.Votes==4{
// 							c.GVCon.NumVotes4++
// 						}else if unchooseGVNode.ChoosedGVNode.Votes==5{
// 							c.GVCon.NumVotes5++
// 						}
// 					}
// 				}
// 			}else{
// 				logger.Infof("zs: gettx txid:%s NumgnodeAConsensusRKV:%d lenRKV:%d nil but diff",txId,NumgnodeAConsensusRKV,int64(len(unchooseGVNode.ReadVotes)))
// 			}
// 		}else{
// 			if NumgnodeAConsensusRKV==int64(len(unchooseGVNode.ReadVotes)){
// 				unchooseGVNode.ChoosedGVNode.Votes++
// 				logger.Infof("zs: gettx txid:%s NumgnodeAConsensusRKV:%d lenRKV:%d same SameVotes:%d",
// 				txId,NumgnodeAConsensusRKV,int64(len(unchooseGVNode.ReadVotes)),unchooseGVNode.ChoosedGVNode.Votes)
// 				if unchooseGVNode.ChoosedGVNode.Votes==2{
// 					c.GVCon.NumVotes2++
// 				}else if unchooseGVNode.ChoosedGVNode.Votes==3{
// 					c.GVCon.NumVotes3++
// 				}else if unchooseGVNode.ChoosedGVNode.Votes==4{
// 					c.GVCon.NumVotes4++
// 				}else if unchooseGVNode.ChoosedGVNode.Votes==5{
// 					c.GVCon.NumVotes5++
// 				}
// 			}else{
// 				logger.Infof("zs: gettx txid:%s NumgnodeAConsensusRKV:%d lenRKV:%d diff SameVotes:%d",
// 				txId,NumgnodeAConsensusRKV,int64(len(unchooseGVNode.ReadVotes)),unchooseGVNode.ChoosedGVNode.Votes)
// 			}
// 		}
// 	}
// }
// func (c *Chain) ChooseGVNode(txId string) {
// 	//var votes int64
// 	//votes=0
// 	unchooseGVNode,_:=c.GVCon.GUnchoosedGVNode[txId]
// 	for _,gvnode :=range unchooseGVNode.ValueGVNode{
// 		flag:=true
// 		for rk,rkVotes:=range unchooseGVNode.ReadVotes{
// 			rkValue:=rkVotes.MaxValueVoteNode.Value
// 			gvnodeRK,isExist:=gvnode.ReadSet[rk]
// 			if isExist==true && strings.Compare(gvnodeRK.Value,rkValue)==0{
// 				continue
// 			}else{
// 				flag=false
// 				break
// 			}
// 		}
// 		if flag==true{
// 			if unchooseGVNode.ChoosedGVNode==nil{
// 				unchooseGVNode.ChoosedGVNode=gvnode
// 			}else{
// 				unchooseGVNode.ChoosedGVNode.Votes++
// 			}
// 		}
// 	}
// }
// func (c *Chain) DeleteChoosedGVNode(gvnode *GVNode) {
// 	logger.Infof("zs: DeleteChoosedGVNode txid:%s  BE\n",gvnode.TxId)

// 	if gvnode.State!=1{
// 		flagCommit:=true
// 		for _,rkv:=range gvnode.ReadSet{
// 			flagWaitRKV:=false
// 			globalRKV,isExistgrkv:=c.GVCon.GlobalState[rkv.Key]
// 			if isExistgrkv==false{
// 				flagCommit=false
// 				flagWaitRKV=true
// 			}else{
// 				if strings.Compare(globalRKV.Value,rkv.Value)!=0{
// 					flagCommit=false
// 					flagWaitRKV=true
// 				}
// 			}
// 			if flagWaitRKV==true{
// 				logger.Infof("zs: DeleteChoosedGVNode txid:%s  wait: rk[%s] rv[%s]\n",gvnode.TxId,rkv.Key,rkv.Value)
// 				waitStateValue,isExistWSV:=c.GVCon.WaitStateGVNode[rkv.Key]
// 				if isExistWSV==false{
// 					waitGVNode:=&WaitGVNode{
// 						WaitValueGVNode: make(map[string]*GVNode),
// 					}

// 					waitGVNode.WaitValueGVNode[gvnode.TxId]=gvnode
// 					c.GVCon.WaitStateGVNode[rkv.Key]=&WaitStateValue{
// 						Value: make(map[string]*WaitGVNode),
// 					}
// 					c.GVCon.WaitStateGVNode[rkv.Key].Value[rkv.Value]=waitGVNode
// 				}else{
// 					waitStateValueValue,isExistwSVV:=waitStateValue.Value[rkv.Value]
// 					if isExistwSVV==false{
// 						waitGVNode:=&WaitGVNode{
// 							WaitValueGVNode: make(map[string]*GVNode),
// 						}
// 						waitGVNode.WaitValueGVNode[gvnode.TxId]=gvnode
// 						waitStateValue.Value[rkv.Value]=waitGVNode
// 					}else{
// 						waitStateValueValue.WaitValueGVNode[gvnode.TxId]=gvnode
// 					}
// 				}
// 			}
// 		}
// 		if flagCommit==true{
// 			gvnode.State=1
// 			c.GVCon.NumGCommittedNode++
// 			c.CommitC <- gvnode.TxResult
// 			logger.Infof("zs: DeleteChoosedGVNode txid:%s  committed\n",gvnode.TxId)
// 			logger.Infof("zs: DeleteChoosedGVNode txid:%s  c.UpdateGlobalValue(gvnode) BE\n",gvnode.TxId)
// 			c.DeleteChildTx(gvnode)
// 			logger.Infof("zs: DeleteChoosedGVNode txid:%s  c.UpdateGlobalValue(gvnode) EN\n",gvnode.TxId)
// 		}
// 	}
// 	logger.Infof("zs: DeleteChoosedGVNode txid:%s  EN\n",gvnode.TxId)
// }
// func (c *Chain) DeleteChildTx(gvnode *GVNode){
// 	logger.Infof("zs: DeleteChildTx txid:%s  BE\n",gvnode.TxId)
// 	for _,wkv:=range gvnode.WriteSet{
// 		globalKV,isExistgkv:=c.GVCon.GlobalState[wkv.Key]
// 		if isExistgkv==false{
// 			c.GVCon.GlobalState[wkv.Key]=&StateValue{
// 				Value: wkv.Value,
// 				History: make(map[string]bool),
// 			}
// 		}else{
// 			c.GVCon.GlobalState[wkv.Key].History[globalKV.Value]=true
// 			globalKV.Value=wkv.Value
// 		}
// 	}
// 	for _,wkv:=range gvnode.WriteSet{
// 		waitStateValue,isExistWSV:=c.GVCon.WaitStateGVNode[wkv.Key]
// 		if isExistWSV==true{
// 			waitStateValueValue,isExistwSVV:=waitStateValue.Value[wkv.Value]
// 			if isExistwSVV==true{
// 				for _,childGVNode:=range waitStateValueValue.WaitValueGVNode{
// 					if childGVNode.State==1{
// 						delete(waitStateValueValue.WaitValueGVNode,childGVNode.TxId)
// 						logger.Infof("zs: DeleteChildTx txid:%s rk:%s rv:%s  childtxid:%s  has already committed\n",
// 							gvnode.TxId,wkv.Key,wkv.Value,childGVNode.TxId)
// 					}else{
// 						flagCommitCTX:=true
// 						for _,rkvCTX:=range childGVNode.ReadSet{
// 							globalKVCTX,isExistglobalKVCTX:=c.GVCon.GlobalState[rkvCTX.Key]
// 							if isExistglobalKVCTX==true && strings.Compare(globalKVCTX.Value,rkvCTX.Value)==0{
// 								continue
// 							}else{
// 								flagCommitCTX=false
// 								break
// 							}
// 						}
// 						if flagCommitCTX==true{
// 							childGVNode.State=1
// 							c.GVCon.NumGCommittedNode++
// 							delete(waitStateValueValue.WaitValueGVNode,childGVNode.TxId)
// 							c.CommitC <- childGVNode.TxResult
// 							logger.Infof("zs: DeleteChildTx txid:%s rk:%s rv:%s wrkvlen[%d] childtxid:%s  committed\n",
// 							gvnode.TxId,wkv.Key,wkv.Value,len(waitStateValueValue.WaitValueGVNode),childGVNode.TxId)
// 							logger.Infof("zs: DeleteChildTx txid:%s rk:%s rv:%s wrkvlen[%d] childtxid:%s  c.UpdateGlobalValue(ctx) BE\n",
// 							gvnode.TxId,wkv.Key,wkv.Value,len(waitStateValueValue.WaitValueGVNode),childGVNode.TxId)
// 							c.DeleteChildTx(childGVNode)
// 							logger.Infof("zs: DeleteChildTx txid:%s rk:%s rv:%s wrkvlen[%d] childtxid:%s  c.UpdateGlobalValue(ctx) EN\n",
// 							gvnode.TxId,wkv.Key,wkv.Value,len(waitStateValueValue.WaitValueGVNode),childGVNode.TxId)
// 						}
// 					}
// 				}
				

// 			}
// 		}
// 	}

// 	logger.Infof("zs: DeleteChildTx txid:%s  EN\n",gvnode.TxId)
// }
//zs0521withcli
func (c *Chain) ordered(msg *orderer.SubmitRequest) ( [][]*common.Envelope,  bool, error) {
	//logger.Infof("zs: OTpsCount   c Chain ordered\n")
	seq := c.support.Sequence()
	batches := [][]*common.Envelope{}
	var pending bool
	var err error 
	pending=true
	//pending=false
	if c.isConfig(msg.Payload) {
		// ConfigMsg
		if msg.LastValidationSeq < seq {
			c.logger.Warnf("Config message was validated against %d, although current config seq has advanced (%d)", msg.LastValidationSeq, seq)
			msg.Payload, _, err = c.support.ProcessConfigMsg(msg.Payload)
			if err != nil {
				c.Metrics.ProposalFailures.Add(1)
				return nil, true, errors.Errorf("bad config message: %s", err)
			}

			if err = c.checkConfigUpdateValidity(msg.Payload); err != nil {
				c.Metrics.ProposalFailures.Add(1)
				return nil, true, errors.Errorf("bad config message: %s", err)
			}
		}
		batch := c.support.BlockCutter().Cut()
		batches = [][]*common.Envelope{}
		if len(batch) != 0 {
			batches = append(batches, batch)
		}
		batches = append(batches, []*common.Envelope{msg.Payload})
		return batches, false, nil
	}
	// it is a normal message
	if msg.LastValidationSeq < seq {
		c.logger.Warnf("Normal message was validated against %d, although current config seq has advanced (%d)", msg.LastValidationSeq, seq)
		if _, err := c.support.ProcessNormalMsg(msg.Payload); err != nil {
			c.Metrics.ProposalFailures.Add(1)
			return nil, true, errors.Errorf("bad normal message: %s", err)
		}
	}
	//zs

	// c.GVCon.ROTXCount++
	// logger.Infof("zs: ROTpsCount  order alltx:%d\n",c.GVCon.ROTXCount)
	// if c.GVCon.ROTXCount==1000{
	// 	c.GVCon.ROTXLastCount=c.GVCon.ROTXCount
	// 	c.GVCon.ROTXLastTime=time.Now()
	// }
	// if c.GVCon.ROTXCount>1000{
	// 	tempti:=time.Now()
	// 	if tempti.Sub(c.GVCon.ROTXLastTime).Seconds()>=1{
	// 		logger.Infof("zs: ROTpsCounttps order Receive txnum:%d lastti[%v] nowti[%v] subti[%v]  alltx:%d\n",
	// 		c.GVCon.ROTXCount-c.GVCon.ROTXLastCount,c.GVCon.ROTXLastTime,tempti,tempti.Sub(c.GVCon.ROTXLastTime),c.GVCon.ROTXCount)
	// 		c.GVCon.ROTXLastCount=c.GVCon.ROTXCount
	// 		c.GVCon.ROTXLastTime=tempti
	// 	}
	// }






	// pending=true
	tempenvmsg:=&partialorder.EnvMsg{
		Env:msg.Payload,
		CommitC:c.CommitC,   
	}
	raftEnvMsgC:=c.support.BlockCutter().GetRaftEnvMsgC()
	//logger.Infof("zs: raftEnvMsgC tempenvmsg len:%d BE\n",len(raftEnvMsgC))
	raftEnvMsgC <-tempenvmsg
	//logger.Infof("zs: raftEnvMsgC tempenvmsg len:%d EN\n",len(raftEnvMsgC))
	// c.GVCon.OTXCount++
	// logger.Infof("zs: OTpsCount   alltx:%d\n",c.GVCon.OTXCount)
	// if c.GVCon.OTXCount==1000{
	// 	c.GVCon.OTXLastCount=c.GVCon.OTXCount
	// 	c.GVCon.OTXLastTime=time.Now()
	// }
	// if c.GVCon.OTXCount>1000{
	// 	tempti:=time.Now()
	// 	if tempti.Sub(c.GVCon.OTXLastTime).Seconds()>=1{
	// 		logger.Infof("zs: OTpsCounttps Receive txnum:%d lastti[%v] nowti[%v] subti[%v]  alltx:%d\n",
	// 		c.GVCon.OTXCount-c.GVCon.OTXLastCount,c.GVCon.OTXLastTime,tempti,tempti.Sub(c.GVCon.OTXLastTime),c.GVCon.OTXCount)
	// 		c.GVCon.OTXLastCount=c.GVCon.OTXCount
	// 		c.GVCon.OTXLastTime=tempti
	// 	}
	// }

	if c.GVCon.OTXCount==2||c.GVCon.OTXCount==3{
		pending=false
	}

	
	return batches, pending, nil
}
//zs0521withcli
// func (c *Chain) ordered(msg *orderer.SubmitRequest) ( [][]*common.Envelope,  bool, error) {
// 	seq := c.support.Sequence()
// 	batches := [][]*common.Envelope{}
// 	var pending bool
// 	var err error 
// 	pending=true
// 	//pending=false
// 	if c.isConfig(msg.Payload) {
// 		// ConfigMsg
// 		if msg.LastValidationSeq < seq {
// 			c.logger.Warnf("Config message was validated against %d, although current config seq has advanced (%d)", msg.LastValidationSeq, seq)
// 			msg.Payload, _, err = c.support.ProcessConfigMsg(msg.Payload)
// 			if err != nil {
// 				c.Metrics.ProposalFailures.Add(1)
// 				return nil, true, errors.Errorf("bad config message: %s", err)
// 			}

// 			if err = c.checkConfigUpdateValidity(msg.Payload); err != nil {
// 				c.Metrics.ProposalFailures.Add(1)
// 				return nil, true, errors.Errorf("bad config message: %s", err)
// 			}
// 		}
// 		batch := c.support.BlockCutter().Cut()
// 		batches = [][]*common.Envelope{}
// 		if len(batch) != 0 {
// 			batches = append(batches, batch)
// 		}
// 		batches = append(batches, []*common.Envelope{msg.Payload})
// 		return batches, false, nil
// 	}
// 	// it is a normal message
// 	if msg.LastValidationSeq < seq {
// 		c.logger.Warnf("Normal message was validated against %d, although current config seq has advanced (%d)", msg.LastValidationSeq, seq)
// 		if _, err := c.support.ProcessNormalMsg(msg.Payload); err != nil {
// 			c.Metrics.ProposalFailures.Add(1)
// 			return nil, true, errors.Errorf("bad normal message: %s", err)
// 		}
// 	}
// 	//zs
// 	env:=msg.Payload
// 	chdr,respPayload,errres:=utils.GetActionAndTxidFromEnvelopeMsg(env)
// 	if errres!=nil{
// 		logger.Infof("zs:ERROR in respPayload,errres:=utils.GetActionFromEnvelopeMsg(env)\n")
// 	}
// 	txId:=chdr.TxId



// 	//numUnchoose:=len(c.GVCon.GUnchoosedGVNode)
// 	// if numUnchoose%100==0{
// 	// 	for wk,wkvmap:=range c.GVCon.WaitStateGVNode{
// 	// 		for wkv,waitStateValue:=range wkvmap.Value{
// 	// 			logger.Infof("zs:#### wait k:%s v:%s numwaitTX:%d",wk,wkv,len(waitStateValue.WaitValueGVNode))

// 	// 		}
// 	// 	}
// 	// }


// 	//zs...
// 	// type GVNode struct {
// 	// 	TxResult *common.Envelope
// 	// 	TxRWSet *rwsetutil.TxRwSet
// 	// 	TxId string
// 	// 	Votes int64
// 	// 	ReadSet map[string] *GVReadKV
// 	// 	WriteSet map[string]*GVWriteKV
// 	// 	State int64 //1.committed; 2.wait but choosed; 3. wait but not choosed; 4:false
// 	// }
// 	// type ValueVoteNode struct {
// 	// 	Vote int64
// 	// 	ValueGVNode []*GVNode
// 	// }
// 	// type ValueVote struct {
// 	// 	ValueSet map[string]*ValueVoteNode
// 	// 	MaxValueVoteNode *ValueVoteNode
// 	// }
// 	// type UnchoosedGVNode struct {
// 	// 	ChoosedGVNode *GVNode
// 	// 	ReadVotes map[string]*ValueVote
// 	// 	WriteVotes map[string]*ValueVote
// 	// NumConsensusRKV int64
// 	// NumConsensusWKV int64
// 	// 	ValueGVNode []*GVNode
// 	//  AllVotes int64
// 	// }
// 	// type StateValue struct {
// 	// 	Value string
// 	// 	History map[string]bool
// 	// 	//Timestamp time.Time
// 	// }
// 	// type WaitGVNode struct {
// 	// 	WaitValueGVNode []*GVNode
// 	// }
// 	// type WaitStateValue struct {
// 	// 	Value map[string]*WaitGVNode
// 	// }
// 	// type GVConsensus struct {
// 	// 	GUnchoosedGVNode map[string]*UnchoosedGVNode
// 	// 	GChoosedGVNode map[string]*GVNode
// 	// 	GlobalState map[string]*StateValue
// 	// 	WaitStateGVNode map[string]*WaitStateValue
// 	// 	NumExeNodes int64
// 	// 	NumKWeight int64
// 	// 	NumTxwithAllvotes int64
// 	// 	NumVotes2 int64
// 	// 	NumVotes3 int64
// 	// 	NumVotes4 int64
// 	// 	NumVotes5 int64
// 	// 	NumGChooseNode int64
// 	// 	NumGCommittedNode int64
// 	// }
// 	//zs...

// 	//GChooseGVNode
// 	chooseGVNode,isChoose:=c.GVCon.GChoosedGVNode[txId]
// 	if isChoose==false{
// 		txRWSet := &rwsetutil.TxRwSet{}
// 		if err = txRWSet.FromProtoBytes(respPayload.Results); err != nil {
// 			logger.Infof("zs: gettx txid:%s txRWSet.FromProtoBytes failed",txId)
// 		}
// 		gvnode:=&partialorder.GVNode{
// 			TxResult: env,
// 			TxRWSet: txRWSet,
// 			TxId: txId,
// 			ReadSet: make(map[string] *partialorder.GVReadKV),
// 			WriteSet: make(map[string]*partialorder.GVWriteKV),
// 			PTx: make(map[string]*partialorder.PtxKV),
// 			UNPTx: make(map[string]*partialorder.PtxKV),
// 			State: 3, //1.committed; 2.wait but choosed; 3. wait but not choosed; 4:false
// 			ConsensusState: 0,//1.all rkv rkvptx wkvptx;2. rkv rkvptx;3. rkv; 0.unconsensus
// 			NumSameRKV: 1,
// 			NumSameRKVPtx: 1,
// 		}
// 		unchooseGVNode,isExist:=c.GVCon.GUnchoosedGVNode[txId]
// 		if isExist==false{
// 			tempcount:=int64(len(c.GVCon.GUnchoosedGVNode))
// 			c.GVCon.GUnchoosedGVNode[txId]=&partialorder.UnchoosedGVNode{
// 				UNPTx: make(map[string]*partialorder.PtxKV),
// 				UNPTxVotes: make(map[string]int64),
// 				Count: tempcount,
// 				ChoosedGVNode: nil,
// 				ValueGVNode: &[]*partialorder.GVNode{},
// 				AllVotes: 1,
// 				State:3,//1.committed; 2.wait but choosed; 3. wait but not choosed; 4:false
// 			}
// 			logger.Infof("zs: gettx txid[%s] times:%d count:%d\n",txId,c.GVCon.GUnchoosedGVNode[txId].AllVotes,c.GVCon.GUnchoosedGVNode[txId].Count)
// 			unchooseGVNode=c.GVCon.GUnchoosedGVNode[txId]
// 			c.GVConAddUnchooseGVNode(gvnode)

// 			if len(c.GVCon.GUnchoosedGVNode)<=1{
// 				gvnode.State=1
// 				c.GVCon.GUnchoosedGVNode[txId].ChoosedGVNode=gvnode
// 				c.GVCon.GChoosedGVNode[txId]=gvnode
// 				c.GVCon.NumGChooseNode++
// 				c.GVCon.NumGCommittedNode++
// 				c.GVCon.NumTxwithAllvotes++
// 				c.GVCon.GUnchoosedGVNode[txId].State=2
// 				//c.CommitC <- gvnode.TxResult
// 				gvnode.Count=unchooseGVNode.Count
// 				batches, pending = c.support.BlockCutter().OrderedRaft(gvnode)

// 				// pending=true
// 				// tempgvnodemsg:=&partialorder.GVNodeMsg{
// 				// 	ChoosedNode:gvnode,
// 				// 	CommitC:c.CommitC,   
// 				// }
// 				// raftGVNodeMsgC:=c.support.BlockCutter().GetRaftGVNodeMsgC()
// 				// logger.Infof("zs:txid:%s raftGVNodeMsgC tempgvnodemsg len:%d BE\n",txId,len(raftGVNodeMsgC))
// 				// raftGVNodeMsgC <-tempgvnodemsg
// 				// logger.Infof("zs:txid:%s raftGVNodeMsgC tempgvnodemsg len:%d EN\n",txId,len(raftGVNodeMsgC))
// 			}
// 		}else{
// 			//unchooseGVNode.ValueGVNode=append(unchooseGVNode.ValueGVNode,gvnode)
// 			unchooseGVNode.AllVotes++

// 			logger.Infof("zs: gettx txid[%s] times:%d wait choose\n",txId,c.GVCon.GUnchoosedGVNode[txId].AllVotes)
// 			c.GVConAddUnchooseGVNode(gvnode)
// 			if unchooseGVNode.AllVotes==c.GVCon.NumExeNodes{
// 				c.GVCon.NumTxwithAllvotes++
// 				c.CountNum(unchooseGVNode)
// 			}
// 			unchooseGVNode=c.GVCon.GUnchoosedGVNode[txId]
// 			chooseGVNode=unchooseGVNode.ChoosedGVNode
// 			if chooseGVNode!=nil{
// 				// logger.Infof("zs: gettx txid:%s numrkv:%d conrkv:%d conTXvotes:%d\n",
// 				// txId,len(unchooseGVNode.ReadVotes),unchooseGVNode.NumConsensusRKV,chooseGVNode.Votes)
// 				logger.Infof("zs: gettx txid:%s Choose GVNode with NumSameRKVPtx:%d ConsensusState:1\n",txId,chooseGVNode.NumSameRKVPtx)
// 				if (c.GVCon.NumGCommittedNode<2 && chooseGVNode.NumSameRKVPtx == c.GVCon.NumExeNodes )  || (c.GVCon.NumGCommittedNode>=2 && chooseGVNode.NumSameRKVPtx >= c.GVCon.NumKWeight){
// 				//if unchooseGVNode.AllVotes==c.GVCon.NumExeNodes {
// 					chooseGVNode.UNPTx= make(map[string]*partialorder.PtxKV)
// 					for unkptxid,unv:=range unchooseGVNode.UNPTxVotes{
// 						newptxid:=unkptxid[len(unkptxid)-len(txId):]
// 						//if unv>=c.GVCon.NumKWeight || c.GVCon.GUnchoosedGVNode[newptxid].Count<unchooseGVNode.Count{
// 						if unv>=c.GVCon.NumKWeight{
// 							chooseGVNode.UNPTx[newptxid]=unchooseGVNode.UNPTx[newptxid]
// 						}
// 					}
// 					//c.GVCon.NumGChooseNode++
// 					c.GVCon.NumGCommittedNode++
// 					chooseGVNode.State=2
// 					c.GVCon.GChoosedGVNode[txId]=chooseGVNode
// 					unchooseGVNode.State=2
// 					//c.DeleteChoosedGVNode(chooseGVNode)
// 					chooseGVNode.Count=unchooseGVNode.Count
// 					batches, pending = c.support.BlockCutter().OrderedRaft(chooseGVNode)

// 					// tempgvnodemsg:=&partialorder.GVNodeMsg{
// 					// 	ChoosedNode:chooseGVNode,
// 					// 	CommitC:c.CommitC,   
// 					// }
// 					// pending=true
// 					// raftGVNodeMsgC:=c.support.BlockCutter().GetRaftGVNodeMsgC()
// 					// logger.Infof("zs:txid:%s raftGVNodeMsgC tempgvnodemsg len:%d BE\n",txId,len(raftGVNodeMsgC))
// 					// raftGVNodeMsgC <-tempgvnodemsg
// 					// logger.Infof("zs:txid:%s raftGVNodeMsgC tempgvnodemsg len:%d EN\n",txId,len(raftGVNodeMsgC))
// 				}
// 			}else{
// 				logger.Infof("zs: gettx txid:%s chooseGVNode==nil\n",txId)
// 			}
// 			//zs0513
// 			if unchooseGVNode.AllVotes==c.GVCon.NumExeNodes{
// 				if unchooseGVNode.State!=2{
// 						unchooseGVNode.ChoosedGVNode=(*unchooseGVNode.ValueGVNode)[0]
// 						chooseGVNode=unchooseGVNode.ChoosedGVNode
// 						chooseGVNode.Count=unchooseGVNode.Count
// 						chooseGVNode.State=4
// 						unchooseGVNode.State=4
// 						chooseGVNode.ConsensusState=3
// 						batches, pending = c.support.BlockCutter().OrderedRaft(chooseGVNode)


// 						// tempgvnodemsg:=&partialorder.GVNodeMsg{
// 						// 	ChoosedNode:chooseGVNode,
// 						// 	CommitC:c.CommitC,   
// 						// }
// 						// pending=true
// 						// raftGVNodeMsgC:=c.support.BlockCutter().GetRaftGVNodeMsgC()
// 						// logger.Infof("zs:txid:%s raftGVNodeMsgC tempgvnodemsg len:%d BE false reach Consensus\n",txId,len(raftGVNodeMsgC))
// 						// raftGVNodeMsgC <-tempgvnodemsg
// 						// logger.Infof("zs:txid:%s raftGVNodeMsgC tempgvnodemsg len:%d EN false reach Consensus\n",txId,len(raftGVNodeMsgC))
// 						// logger.Infof("zs: gettx txid:%s false reach Consensus\n",txId)
					
// 					// logger.Infof("zs: gettx txid:%s numrkv:%d conrkv:%d\n",txId,len(unchooseGVNode.ReadVotes),unchooseGVNode.NumConsensusRKV)
					
// 				}
// 			}
// 			//zs0513

// 			// if unchooseGVNode.AllVotes==c.GVCon.NumExeNodes{
// 			// 	if unchooseGVNode.State!=2{
// 			// 		c.SelectRKVConsensusGVNode(unchooseGVNode)
// 			// 		chooseGVNode=unchooseGVNode.ChoosedGVNode
// 			// 		if chooseGVNode!=nil{
// 			// 			logger.Infof("zs: gettx txid:%s Choose GVNode with NumSameRKVPtx:%d NumSameRKV:%d ConsensusState:2\n",txId,chooseGVNode.NumSameRKVPtx,chooseGVNode.NumSameRKV)
// 			// 			//c.GVCon.NumGChooseNode++
// 			// 			c.GVCon.NumGCommittedNode++
// 			// 			chooseGVNode.State=2
// 			// 			unchooseGVNode.State=2
// 			// 			c.GVCon.GChoosedGVNode[txId]=chooseGVNode
// 			// 			chooseGVNode.Count=unchooseGVNode.Count
// 			// 			batches, pending = c.support.BlockCutter().OrderedRaft(chooseGVNode)
// 			// 		}else{
// 			// 			unchooseGVNode.ChoosedGVNode=(*unchooseGVNode.ValueGVNode)[0]
// 			// 			chooseGVNode=unchooseGVNode.ChoosedGVNode
// 			// 			chooseGVNode.Count=unchooseGVNode.Count
// 			// 			chooseGVNode.State=4
// 			// 			unchooseGVNode.State=4
// 			// 			chooseGVNode.ConsensusState=3
// 			// 			batches, pending = c.support.BlockCutter().OrderedRaft(chooseGVNode)
// 			// 			logger.Infof("zs: gettx txid:%s false reach Consensus\n",txId)
// 			// 		}
// 			// 		// logger.Infof("zs: gettx txid:%s numrkv:%d conrkv:%d\n",txId,len(unchooseGVNode.ReadVotes),unchooseGVNode.NumConsensusRKV)
					
// 			// 	}
// 			// }
			
// 		}
// 	}else{
// 		//has already choosen
// 		 unchooseGVNode,_:=c.GVCon.GUnchoosedGVNode[txId]
// 		txRWSet := &rwsetutil.TxRwSet{}
// 		if err = txRWSet.FromProtoBytes(respPayload.Results); err != nil {
// 			logger.Infof("zs: gettx txid:%s txRWSet.FromProtoBytes failed",txId)
// 		}
// 		gvnode:=&partialorder.GVNode{
// 			TxResult: env,
// 			TxRWSet: txRWSet,
// 			TxId: txId,
// 			ReadSet: make(map[string] *partialorder.GVReadKV),
// 			WriteSet: make(map[string]*partialorder.GVWriteKV),
// 			PTx: make(map[string]*partialorder.PtxKV),
// 			UNPTx: make(map[string]*partialorder.PtxKV),
// 			State: 3, //1.committed; 2.wait but choosed; 3. wait but not choosed; 4:false
// 			ConsensusState: 0,//1.all rkv rkvptx wkvptx;2. rkv rkvptx;3. rkv; 0.unconsensus
// 			NumSameRKV: 1,
// 			NumSameRKVPtx: 1,
// 		}
// 		unchooseGVNode.AllVotes++

// 		logger.Infof("zs: gettx txid[%s] times:%d already choose\n",txId,c.GVCon.GUnchoosedGVNode[txId].AllVotes)
// 		c.GVConAddUnchooseGVNode(gvnode)
// 		if unchooseGVNode.AllVotes==c.GVCon.NumExeNodes{
// 			c.GVCon.NumTxwithAllvotes++
// 			c.CountNum(unchooseGVNode)
// 		}
// 	}
// 	//batches, pending = c.support.BlockCutter().Ordered(msg.Payload)
// 	if len(c.GVCon.GUnchoosedGVNode)==2||len(c.GVCon.GUnchoosedGVNode)==2{
// 		pending=false
// 	}

// 	logger.Infof("zs:### txid:%s  all tx:%d txwithallvotes:%d NumChoosedNode1:%d NumGCommittedNode1a2a4:%d RKVPTX2=%d RKVPTX3=%d RKVPTX4=%d RKVPTX5=%d GChooseNode:%d RKV2=%d RKV3=%d RKV4=%d RKV5=%d\n",
// 	txId, len(c.GVCon.GUnchoosedGVNode), c.GVCon.NumTxwithAllvotes,c.GVCon.NumGChooseNode,c.GVCon.NumGCommittedNode,c.GVCon.NumVotes2,c.GVCon.NumVotes3,c.GVCon.NumVotes4,c.GVCon.NumVotes5,
// 	c.GVCon.NumGChooseNode,c.GVCon.NumRKVVotes2,c.GVCon.NumRKVVotes3,c.GVCon.NumRKVVotes4,c.GVCon.NumRKVVotes5)

// 	return batches, pending, nil
// }
//zs0423


//zs
// Orders the envelope in the `msg` content. SubmitRequest.
// Returns
//   -- batches [][]*common.Envelope; the batches cut,
//   -- pending bool; if there are envelopes pending to be ordered,
//   -- err error; the error encountered, if any.
// It takes care of config messages as well as the revalidation of messages if the config sequence has advanced.
//zs
// func (c *Chain) ordered(msg *orderer.SubmitRequest) ( [][]*common.Envelope,  bool, error) {
// 	seq := c.support.Sequence()
// 	batches := [][]*common.Envelope{}
// 	var pending bool
// 	var err error 

// 	if c.isConfig(msg.Payload) {
// 		// ConfigMsg
// 		if msg.LastValidationSeq < seq {
// 			c.logger.Warnf("Config message was validated against %d, although current config seq has advanced (%d)", msg.LastValidationSeq, seq)
// 			msg.Payload, _, err = c.support.ProcessConfigMsg(msg.Payload)
// 			if err != nil {
// 				c.Metrics.ProposalFailures.Add(1)
// 				return nil, true, errors.Errorf("bad config message: %s", err)
// 			}

// 			if err = c.checkConfigUpdateValidity(msg.Payload); err != nil {
// 				c.Metrics.ProposalFailures.Add(1)
// 				return nil, true, errors.Errorf("bad config message: %s", err)
// 			}
// 		}
// 		batch := c.support.BlockCutter().Cut()
// 		batches = [][]*common.Envelope{}
// 		if len(batch) != 0 {
// 			batches = append(batches, batch)
// 		}
// 		batches = append(batches, []*common.Envelope{msg.Payload})
// 		return batches, false, nil
// 	}
// 	// it is a normal message
// 	if msg.LastValidationSeq < seq {
// 		c.logger.Warnf("Normal message was validated against %d, although current config seq has advanced (%d)", msg.LastValidationSeq, seq)
// 		if _, err := c.support.ProcessNormalMsg(msg.Payload); err != nil {
// 			c.Metrics.ProposalFailures.Add(1)
// 			return nil, true, errors.Errorf("bad normal message: %s", err)
// 		}
// 	}

// 	//zs
// 	env:=msg.Payload
// 	txCount,txId,_,respPayload,_:=utils.GetTxnResultFromEnvelopeMsg(env)

// 	logger.Infof("zs:### txid:%s txcount:%d all tx:%d txwithallvotes:%d NumsendInblcok:%d 2=Votes:%d 3=Votes:%d 4=Votes:%d 5=Votes:%d GChooseNode:%d GChooseEdge:%d",
// 		txId,txCount, len(c.GCon.GNodeInBlock), c.GCon.NumTxwithAllvotes,c.GCon.NumSendInBlock,c.GCon.NumVotes2,c.GCon.NumVotes3,c.GCon.NumVotes4,c.GCon.NumVotes5,
// 		c.GCon.NumGChooseNode,c.GCon.NumGChooseEdge)

// 	txState,isExistBefore:=c.GCon.GNodeInBlock[txId]

// 	if isExistBefore==false{

// 		c.GCon.GTxWeight[txId]=1
// 		c.GCon.GNodeInBlock[txId]=&CommittedGnode{
// 			State:          2,
// 			committedGnode: nil,
// 			IsCommitted: 0,
// 		}
// 		logger.Infof("zs: gettx times:%d in block txid:%s txcount:%d firsttimes",c.GCon.GTxWeight[txId],txId,txCount)


// 		txRWSet := &rwsetutil.TxRwSet{}
// 		if err = txRWSet.FromProtoBytes(respPayload.Results); err != nil {
// 			logger.Infof("zs: gettx txid:%s txcount:%d txRWSet.FromProtoBytes failed",txId,txCount)
// 		}
// 		gnode:=&GNode{
// 			TxResult: 	env,
// 			TxRWSet: txRWSet,
// 			TxId:      txId,
// 			TxCount:   txCount,
// 			PeerId:    "",
// 			Votes:  1,
// 			ParentTxs: make(map[string]*ReadKV),
// 			ReadSet: make(map[string]*ReadKV),
// 			WriteSet: make(map[string]*WriteKV),
// 			State: 2,
// 			NumParentsOfRW: 0,
// 			NumParentsOfOW: 0,
// 			NumParentsOfOR: 0,
// 			IsCommitted: 0,
// 			NumPGChooseNode: 0,
// 			WaitPTX: make(map[string]bool),

// 		}
// 		flagAddNode:=c.GConAddNode(gnode)
// 		if flagAddNode==true{
// 			logger.Infof("zs: gettx txid:%s txcount:%d addnode success",txId,txCount)
// 		}else{
// 			logger.Infof("zs: gettx txid:%s txcount:%d addnode false",txId,txCount)
// 		}


// 		if len(c.GCon.GTxWeight)<=1{
// 			//time.Sleep(5 * time.Second) 
// 			gnode.State=1
// 			c.GCon.GNodeInBlock[txId].State=1
// 			c.GCon.GNodeInBlock[txId].IsCommitted=1
// 			c.GCon.GNodeInBlock[txId].committedGnode=gnode
// 			c.GCon.GnodeMaxResults[txId].GnodeResult=gnode
// 			c.GCon.GnodeMaxResults[txId].State=1
// 			c.GCon.GTxRelusts[txId].State=1
// 			logger.Infof("zs: committed inblock txid:%s txcount:%d",txId,txCount)
// 			batches, pending = c.support.BlockCutter().Ordered(gnode.TxResult)
// 			//batches, pending = c.support.BlockCutter().Ordered(msg.Payload)
// 			c.GCon.NumSendInBlock++
// 		}
// 	}else if txState.IsCommitted==0{

// 		txTimes:=c.GCon.GTxWeight[txId]
// 		txTimes++
// 		c.GCon.GTxWeight[txId]=txTimes
// 		logger.Infof("zs: gettx times:%d in block txid:%s txcount:%d ",c.GCon.GTxWeight[txId],txId,txCount)

// 		if txTimes==c.GCon.NumExeNodes{
// 			c.GCon.NumTxwithAllvotes++
// 		}


// 		txRWSet := &rwsetutil.TxRwSet{}
// 		if err = txRWSet.FromProtoBytes(respPayload.Results); err != nil {
// 			logger.Infof("zs: gettx txid:%s txcount:%d txRWSet.FromProtoBytes failed",txId,txCount)
// 		}

// 		gnode:=&GNode{
// 			TxResult: 	env,
// 			TxRWSet: txRWSet,
// 			TxId:      txId,
// 			TxCount:   txCount,
// 			PeerId:    "",
// 			Votes:  1,
// 			ParentTxs: make(map[string]*ReadKV),
// 			ReadSet: make(map[string]*ReadKV),
// 			WriteSet: make(map[string]*WriteKV),
// 			State: 2,
// 			NumParentsOfRW: 0,
// 			NumParentsOfOW: 0,
// 			NumParentsOfOR: 0,
// 			IsCommitted: 0,
// 			NumPGChooseNode: 0,
// 			WaitPTX: make(map[string]bool),
// 		}
// 		txResult:=c.GCon.GTxRelusts[gnode.TxId]
// 		flagIsSame:=false
// 		for txResult!=nil{
// 			if c.GnodeCompare(gnode,txResult.GnodeResult)==true{
// 				flagIsSame=true
// 				break
// 			}
// 			txResult=txResult.Next
// 		}
// 		if flagIsSame==true {
// 			txResult.GnodeResult.Votes++

// 			if txResult.GnodeResult.Votes == 2 {
// 				c.GCon.NumVotes2++
// 			} else if txResult.GnodeResult.Votes == 3 {
// 				c.GCon.NumVotes3++
// 			} else if txResult.GnodeResult.Votes == 4 {
// 				c.GCon.NumVotes4++
// 			} else if txResult.GnodeResult.Votes ==5{
// 				c.GCon.NumVotes5++
// 			}

// 			if (c.GCon.NumSendInBlock<2 && txResult.GnodeResult.Votes == c.GCon.NumExeNodes )  || (c.GCon.NumSendInBlock>=2 && txResult.GnodeResult.Votes >= c.GCon.NumKWeight) {
// 				c.GCon.GChooseNode[txResult.GnodeResult.TxId]=txResult.GnodeResult

// 				c.GCon.NumGChooseNode++
// 				txResult.GnodeResult.State=1
// 				c.GCon.GnodeMaxResults[txId].GnodeResult=txResult.GnodeResult
// 				c.GCon.GNodeInBlock[txId].State = 1
// 				c.GCon.GNodeInBlock[txId].IsCommitted = 1
// 				c.GCon.GNodeInBlock[txId].committedGnode = txResult.GnodeResult

// 				c.GCon.GnodeMaxResults[txId].State = 1
// 				c.GCon.GTxRelusts[txId].State = 1

// 				logger.Infof("zs: GChooseNode txid:%s",txResult.GnodeResult.TxId)

// 				c.DeleteGChooseNode(txResult.GnodeResult,&batches,&pending)

// 				logger.Infof("zs: GChooseNode txid:%s END lenbatches:%d",txResult.GnodeResult.TxId,len(batches))
// 			}
// 		}else{
// 			flagAddNode:=c.GConAddNode(gnode)
// 			if flagAddNode==true{
// 				logger.Infof("zs: gettx txid:%s txcount:%d addnode success in state==2",txId,txCount)
// 			}else{
// 				logger.Infof("zs: gettx txid:%s txcount:%d addnode false in state==2",txId,txCount)
// 			}

// 		}
// 	}else{

// 		txTimes:=c.GCon.GTxWeight[txId]
// 		txTimes++
// 		c.GCon.GTxWeight[txId]=txTimes
// 		logger.Infof("zs: gettx times:%d in block txid:%s txcount:%d is already committed",c.GCon.GTxWeight[txId],txId,txCount)
// 		if txTimes==c.GCon.NumExeNodes{
// 			c.GCon.NumTxwithAllvotes++
// 		}


// 		txRWSet := &rwsetutil.TxRwSet{}
// 		if err = txRWSet.FromProtoBytes(respPayload.Results); err != nil {
// 			logger.Infof("zs: gettx txid:%s txcount:%d txRWSet.FromProtoBytes failed",txId,txCount)
// 		}

// 		gnode:=&GNode{
// 			TxResult: 	env,
// 			TxRWSet: txRWSet,
// 			TxId:      txId,
// 			TxCount:   txCount,
// 			PeerId:    "",
// 			Votes:  1,
// 			ParentTxs: make(map[string]*ReadKV),
// 			ReadSet: make(map[string]*ReadKV),
// 			WriteSet: make(map[string]*WriteKV),
// 			State: 1,
// 			NumParentsOfRW: 0,
// 			NumParentsOfOW: 0,
// 			NumParentsOfOR: 0,
// 			IsCommitted: 0,
// 			NumPGChooseNode: 0,
// 			WaitPTX: make(map[string]bool),
// 		}
// 		txResult:=c.GCon.GTxRelusts[gnode.TxId]
// 		flagIsSame:=false
// 		for txResult!=nil{
// 			if c.GnodeCompare(gnode,txResult.GnodeResult)==true{
// 				flagIsSame=true
// 				break
// 			}
// 			txResult=txResult.Next
// 		}
// 		if flagIsSame==true {
// 			txResult.GnodeResult.Votes++

// 			if txResult.GnodeResult.Votes == 2 {
// 				c.GCon.NumVotes2++
// 			} else if txResult.GnodeResult.Votes == 3 {
// 				c.GCon.NumVotes3++
// 			} else if txResult.GnodeResult.Votes == 4 {
// 				c.GCon.NumVotes4++
// 			}else if txResult.GnodeResult.Votes == 5 {
// 				c.GCon.NumVotes5++
// 			}
// 		}



// 		for _,rwset:=range txRWSet.NsRwSets{
// 			logger.Infof("zs: gettx txid:%s namespace:%s ",txId,rwset.NameSpace)
// 			if strings.Compare(rwset.NameSpace,"lscc")!=0{
// 				for _,rkv:=range rwset.KvRwSet.Reads{
// 					rkValue:=string(rkv.Value)
// 					if strings.Compare(rkValue,"onlywrite")==0{

// 						logger.Infof("zs: gettx txid:%s onlywrite rtxid:%s rtxcount:%d namespace:%s rk:%s rv:%s rtxid:%s",txId,rkv.Txid,rkv.Version.TxNum,rwset.NameSpace,rkv.Key,string(rkv.Value),rkv.Txid)
// 					}else if strings.Compare(rkValue,"onlyread")==0{

// 						logger.Infof("zs: gettx txid:%s onlyread rtxid:%s rtxcount:%d namespace:%s rk:%s rv:%s rtxid:%s",txId,rkv.Txid,rkv.Version.TxNum,rwset.NameSpace,rkv.Key,string(rkv.Value),rkv.Txid)
// 					}else{
// 						logger.Infof("zs: gettx txid:%s normal namespace:%s rk:%s rv:%s rtxid:%s",txId,rwset.NameSpace,rkv.Key,string(rkv.Value),rkv.Txid)
// 					}
// 				}

// 				if len(rwset.KvRwSet.Reads)>1{
// 					for _,wkv:=range rwset.KvRwSet.Writes{

// 						logger.Infof("zs: gettx txid:%s normal namespace:%s wk:%s wv:%s InR",txId,rwset.NameSpace,wkv.Key,string(wkv.Value))
	
	
// 					}
// 				}




// 			}
// 		}
// 	}
// 	//zs




// 	//batches, pending = c.support.BlockCutter().Ordered(msg.Payload)

// 	return batches, pending, nil

// }
//zs
// func (c *Chain) ordered(msg *orderer.SubmitRequest) (batches [][]*common.Envelope, pending bool, err error) {
// 	seq := c.support.Sequence()

// 	if c.isConfig(msg.Payload) {
// 		// ConfigMsg
// 		if msg.LastValidationSeq < seq {
// 			c.logger.Warnf("Config message was validated against %d, although current config seq has advanced (%d)", msg.LastValidationSeq, seq)
// 			msg.Payload, _, err = c.support.ProcessConfigMsg(msg.Payload)
// 			if err != nil {
// 				c.Metrics.ProposalFailures.Add(1)
// 				return nil, true, errors.Errorf("bad config message: %s", err)
// 			}

// 			if err = c.checkConfigUpdateValidity(msg.Payload); err != nil {
// 				c.Metrics.ProposalFailures.Add(1)
// 				return nil, true, errors.Errorf("bad config message: %s", err)
// 			}
// 		}
// 		batch := c.support.BlockCutter().Cut()
// 		batches = [][]*common.Envelope{}
// 		if len(batch) != 0 {
// 			batches = append(batches, batch)
// 		}
// 		batches = append(batches, []*common.Envelope{msg.Payload})
// 		return batches, false, nil
// 	}
// 	// it is a normal message
// 	if msg.LastValidationSeq < seq {
// 		c.logger.Warnf("Normal message was validated against %d, although current config seq has advanced (%d)", msg.LastValidationSeq, seq)
// 		if _, err := c.support.ProcessNormalMsg(msg.Payload); err != nil {
// 			c.Metrics.ProposalFailures.Add(1)
// 			return nil, true, errors.Errorf("bad normal message: %s", err)
// 		}
// 	}
// 	batches, pending = c.support.BlockCutter().Ordered(msg.Payload)
// 	return batches, pending, nil

// }

func (c *Chain) propose(ch chan<- *common.Block, bc *blockCreator, batches ...[]*common.Envelope) {
	for _, batch := range batches {
		b := bc.createNextBlock(batch)
		c.logger.Infof("Created block [%d], there are %d blocks in flight", b.Header.Number, c.blockInflight)

		select {
		case ch <- b:
		default:
			c.logger.Panic("Programming error: limit of in-flight blocks does not properly take effect or block is proposed by follower")
		}

		// if it is config block, then we should wait for the commit of the block
		if utils.IsConfigBlock(b) {
			c.configInflight = true
		}

		c.blockInflight++
	}

	return
}

func (c *Chain) catchUp(snap *raftpb.Snapshot) error {
	b, err := utils.UnmarshalBlock(snap.Data)
	if err != nil {
		return errors.Errorf("failed to unmarshal snapshot data to block: %s", err)
	}

	if c.lastBlock.Header.Number >= b.Header.Number {
		c.logger.Warnf("Snapshot is at block [%d], local block number is %d, no sync needed", b.Header.Number, c.lastBlock.Header.Number)
		return nil
	}

	puller, err := c.createPuller()
	if err != nil {
		return errors.Errorf("failed to create block puller: %s", err)
	}
	defer puller.Close()

	next := c.lastBlock.Header.Number + 1

	c.logger.Infof("Catching up with snapshot taken at block [%d], starting from block [%d]", b.Header.Number, next)

	for next <= b.Header.Number {
		block := puller.PullBlock(next)
		if block == nil {
			return errors.Errorf("failed to fetch block [%d] from cluster", next)
		}
		if utils.IsConfigBlock(block) {
			c.support.WriteConfigBlock(block, nil)

			configMembership := c.detectConfChange(block)

			if configMembership != nil && configMembership.Changed() {
				c.logger.Infof("Config block [%d] changes consenter set, communication should be reconfigured", block.Header.Number)

				c.raftMetadataLock.Lock()
				c.opts.BlockMetadata = configMembership.NewBlockMetadata
				c.opts.Consenters = configMembership.NewConsenters
				c.raftMetadataLock.Unlock()

				if err := c.configureComm(); err != nil {
					c.logger.Panicf("Failed to configure communication: %s", err)
				}
			}
		} else {
			c.support.WriteBlock(block, nil)
		}

		c.lastBlock = block
		next++
	}

	c.logger.Infof("Finished syncing with cluster up to and including block [%d]", b.Header.Number)
	return nil
}

func (c *Chain) detectConfChange(block *common.Block) *MembershipChanges {
	// If config is targeting THIS channel, inspect consenter set and
	// propose raft ConfChange if it adds/removes node.
	configMetadata := c.newConfigMetadata(block)

	if configMetadata == nil {
		return nil
	}

	if configMetadata.Options != nil &&
		configMetadata.Options.SnapshotIntervalSize != 0 &&
		configMetadata.Options.SnapshotIntervalSize != c.sizeLimit {
		c.logger.Infof("Update snapshot interval size to %d bytes (was %d)",
			configMetadata.Options.SnapshotIntervalSize, c.sizeLimit)
		c.sizeLimit = configMetadata.Options.SnapshotIntervalSize
	}

	changes, err := ComputeMembershipChanges(c.opts.BlockMetadata, c.opts.Consenters, configMetadata.Consenters)
	if err != nil {
		c.logger.Panicf("illegal configuration change detected: %s", err)
	}

	if changes.Rotated() {
		c.logger.Infof("Config block [%d] rotates TLS certificate of node %d", block.Header.Number, changes.RotatedNode)
	}

	return changes
}

func (c *Chain) apply(ents []raftpb.Entry) {
	if len(ents) == 0 {
		return
	}

	if ents[0].Index > c.appliedIndex+1 {
		c.logger.Panicf("first index of committed entry[%d] should <= appliedIndex[%d]+1", ents[0].Index, c.appliedIndex)
	}

	var position int
	for i := range ents {
		switch ents[i].Type {
		case raftpb.EntryNormal:
			if len(ents[i].Data) == 0 {
				break
			}

			position = i
			c.accDataSize += uint32(len(ents[i].Data))

			// We need to strictly avoid re-applying normal entries,
			// otherwise we are writing the same block twice.
			if ents[i].Index <= c.appliedIndex {
				c.logger.Debugf("Received block with raft index (%d) <= applied index (%d), skip", ents[i].Index, c.appliedIndex)
				break
			}

			block := utils.UnmarshalBlockOrPanic(ents[i].Data)
			c.writeBlock(block, ents[i].Index)
			c.Metrics.CommittedBlockNumber.Set(float64(block.Header.Number))

		case raftpb.EntryConfChange:
			var cc raftpb.ConfChange
			if err := cc.Unmarshal(ents[i].Data); err != nil {
				c.logger.Warnf("Failed to unmarshal ConfChange data: %s", err)
				continue
			}

			c.confState = *c.Node.ApplyConfChange(cc)

			switch cc.Type {
			case raftpb.ConfChangeAddNode:
				c.logger.Infof("Applied config change to add node %d, current nodes in channel: %+v", cc.NodeID, c.confState.Nodes)
			case raftpb.ConfChangeRemoveNode:
				c.logger.Infof("Applied config change to remove node %d, current nodes in channel: %+v", cc.NodeID, c.confState.Nodes)
			default:
				c.logger.Panic("Programming error, encountered unsupported raft config change")
			}

			// This ConfChange was introduced by a previously committed config block,
			// we can now unblock submitC to accept envelopes.
			if c.confChangeInProgress != nil &&
				c.confChangeInProgress.NodeID == cc.NodeID &&
				c.confChangeInProgress.Type == cc.Type {

				if err := c.configureComm(); err != nil {
					c.logger.Panicf("Failed to configure communication: %s", err)
				}

				c.confChangeInProgress = nil
				c.configInflight = false
				// report the new cluster size
				c.Metrics.ClusterSize.Set(float64(len(c.opts.BlockMetadata.ConsenterIds)))
			}

			if cc.Type == raftpb.ConfChangeRemoveNode && cc.NodeID == c.raftID {
				c.logger.Infof("Current node removed from replica set for channel %s", c.channelID)
				// calling goroutine, since otherwise it will be blocked
				// trying to write into haltC
				lead := atomic.LoadUint64(&c.lastKnownLeader)
				if lead == c.raftID {
					c.logger.Info("This node is being removed as current leader, halt with delay")
					c.configInflight = true // toggle the flag so this node does not accept further tx
					go func() {
						select {
						case <-c.clock.After(time.Duration(c.opts.ElectionTick) * c.opts.TickInterval):
						case <-c.doneC:
						}

						c.Halt()
					}()
				} else {
					go c.Halt()
				}
			}
		}

		if ents[i].Index > c.appliedIndex {
			c.appliedIndex = ents[i].Index
		}
	}

	if c.accDataSize >= c.sizeLimit {
		b := utils.UnmarshalBlockOrPanic(ents[position].Data)

		select {
		case c.gcC <- &gc{index: c.appliedIndex, state: c.confState, data: ents[position].Data}:
			c.logger.Infof("Accumulated %d bytes since last snapshot, exceeding size limit (%d bytes), "+
				"taking snapshot at block [%d] (index: %d), last snapshotted block number is %d, current nodes: %+v",
				c.accDataSize, c.sizeLimit, b.Header.Number, c.appliedIndex, c.lastSnapBlockNum, c.confState.Nodes)
			c.accDataSize = 0
			c.lastSnapBlockNum = b.Header.Number
			c.Metrics.SnapshotBlockNumber.Set(float64(b.Header.Number))
		default:
			c.logger.Warnf("Snapshotting is in progress, it is very likely that SnapshotIntervalSize is too small")
		}
	}

	return
}

func (c *Chain) gc() {
	for {
		select {
		case g := <-c.gcC:
			c.Node.takeSnapshot(g.index, g.state, g.data)
		case <-c.doneC:
			c.logger.Infof("Stop garbage collecting")
			return
		}
	}
}

func (c *Chain) isConfig(env *common.Envelope) bool {
	h, err := utils.ChannelHeader(env)
	if err != nil {
		c.logger.Panicf("failed to extract channel header from envelope")
	}

	return h.Type == int32(common.HeaderType_CONFIG) || h.Type == int32(common.HeaderType_ORDERER_TRANSACTION)
}

func (c *Chain) configureComm() error {
	// Reset unreachable map when communication is reconfigured
	c.Node.unreachableLock.Lock()
	c.Node.unreachable = make(map[uint64]struct{})
	c.Node.unreachableLock.Unlock()

	nodes, err := c.remotePeers()
	if err != nil {
		return err
	}

	c.configurator.Configure(c.channelID, nodes)
	return nil
}

func (c *Chain) remotePeers() ([]cluster.RemoteNode, error) {
	c.raftMetadataLock.RLock()
	defer c.raftMetadataLock.RUnlock()

	var nodes []cluster.RemoteNode
	for raftID, consenter := range c.opts.Consenters {
		// No need to know yourself
		if raftID == c.raftID {
			continue
		}
		serverCertAsDER, err := pemToDER(consenter.ServerTlsCert, raftID, "server", c.logger)
		if err != nil {
			return nil, errors.WithStack(err)
		}
		clientCertAsDER, err := pemToDER(consenter.ClientTlsCert, raftID, "client", c.logger)
		if err != nil {
			return nil, errors.WithStack(err)
		}
		nodes = append(nodes, cluster.RemoteNode{
			ID:            raftID,
			Endpoint:      fmt.Sprintf("%s:%d", consenter.Host, consenter.Port),
			ServerTLSCert: serverCertAsDER,
			ClientTLSCert: clientCertAsDER,
		})
	}
	return nodes, nil
}

func pemToDER(pemBytes []byte, id uint64, certType string, logger *flogging.FabricLogger) ([]byte, error) {
	bl, _ := pem.Decode(pemBytes)
	if bl == nil {
		logger.Errorf("Rejecting PEM block of %s TLS cert for node %d, offending PEM is: %s", certType, id, string(pemBytes))
		return nil, errors.Errorf("invalid PEM block")
	}
	return bl.Bytes, nil
}

// writeConfigBlock writes configuration blocks into the ledger in
// addition extracts updates about raft replica set and if there
// are changes updates cluster membership as well
func (c *Chain) writeConfigBlock(block *common.Block, index uint64) {
	hdr, err := ConfigChannelHeader(block)
	if err != nil {
		c.logger.Panicf("Failed to get config header type from config block: %s", err)
	}

	c.configInflight = false

	switch common.HeaderType(hdr.Type) {
	case common.HeaderType_CONFIG:
		configMembership := c.detectConfChange(block)

		c.raftMetadataLock.Lock()
		c.opts.BlockMetadata.RaftIndex = index
		if configMembership != nil {
			c.opts.BlockMetadata = configMembership.NewBlockMetadata
			c.opts.Consenters = configMembership.NewConsenters
		}
		c.raftMetadataLock.Unlock()

		blockMetadataBytes := utils.MarshalOrPanic(c.opts.BlockMetadata)
		// write block with metadata
		c.support.WriteConfigBlock(block, blockMetadataBytes)

		if configMembership == nil {
			return
		}

		// update membership
		if configMembership.ConfChange != nil {
			// We need to propose conf change in a go routine, because it may be blocked if raft node
			// becomes leaderless, and we should not block `serveRequest` so it can keep consuming applyC,
			// otherwise we have a deadlock.
			go func() {
				// ProposeConfChange returns error only if node being stopped.
				// This proposal is dropped by followers because DisableProposalForwarding is enabled.
				if err := c.Node.ProposeConfChange(context.TODO(), *configMembership.ConfChange); err != nil {
					c.logger.Warnf("Failed to propose configuration update to Raft node: %s", err)
				}
			}()

			c.confChangeInProgress = configMembership.ConfChange

			switch configMembership.ConfChange.Type {
			case raftpb.ConfChangeAddNode:
				c.logger.Infof("Config block just committed adds node %d, pause accepting transactions till config change is applied", configMembership.ConfChange.NodeID)
			case raftpb.ConfChangeRemoveNode:
				c.logger.Infof("Config block just committed removes node %d, pause accepting transactions till config change is applied", configMembership.ConfChange.NodeID)
			default:
				c.logger.Panic("Programming error, encountered unsupported raft config change")
			}

			c.configInflight = true
		} else if configMembership.Rotated() {
			lead := atomic.LoadUint64(&c.lastKnownLeader)
			if configMembership.RotatedNode == lead {
				c.logger.Infof("Certificate of Raft leader is being rotated, attempt leader transfer before reconfiguring communication")
				go func() {
					c.Node.abdicateLeader(lead)
					if err := c.configureComm(); err != nil {
						c.logger.Panicf("Failed to configure communication: %s", err)
					}
				}()
			} else {
				if err := c.configureComm(); err != nil {
					c.logger.Panicf("Failed to configure communication: %s", err)
				}
			}
		}

	case common.HeaderType_ORDERER_TRANSACTION:
		// If this config is channel creation, no extra inspection is needed
		c.raftMetadataLock.Lock()
		c.opts.BlockMetadata.RaftIndex = index
		m := utils.MarshalOrPanic(c.opts.BlockMetadata)
		c.raftMetadataLock.Unlock()

		c.support.WriteConfigBlock(block, m)

	default:
		c.logger.Panicf("Programming error: unexpected config type: %s", common.HeaderType(hdr.Type))
	}
}

// getInFlightConfChange returns ConfChange in-flight if any.
// It returns confChangeInProgress if it is not nil. Otherwise
// it returns ConfChange from the last committed block (might be nil).
func (c *Chain) getInFlightConfChange() *raftpb.ConfChange {
	if c.confChangeInProgress != nil {
		return c.confChangeInProgress
	}

	if c.lastBlock.Header.Number == 0 {
		return nil // nothing to failover just started the chain
	}

	if !utils.IsConfigBlock(c.lastBlock) {
		return nil
	}

	// extracting current Raft configuration state
	confState := c.Node.ApplyConfChange(raftpb.ConfChange{})

	if len(confState.Nodes) == len(c.opts.BlockMetadata.ConsenterIds) {
		// Raft configuration change could only add one node or
		// remove one node at a time, if raft conf state size is
		// equal to membership stored in block metadata field,
		// that means everything is in sync and no need to propose
		// config update.
		return nil
	}

	return ConfChange(c.opts.BlockMetadata, confState)
}

// newMetadata extract config metadata from the configuration block
func (c *Chain) newConfigMetadata(block *common.Block) *etcdraft.ConfigMetadata {
	metadata, err := ConsensusMetadataFromConfigBlock(block)
	if err != nil {
		c.logger.Panicf("error reading consensus metadata: %s", err)
	}
	return metadata
}

func (c *Chain) suspectEviction() bool {
	if c.isRunning() != nil {
		return false
	}

	return atomic.LoadUint64(&c.lastKnownLeader) == uint64(0)
}

func (c *Chain) newEvictionSuspector() *evictionSuspector {
	return &evictionSuspector{
		amIInChannel:               ConsenterCertificate(c.opts.Cert).IsConsenterOfChannel,
		evictionSuspicionThreshold: c.opts.EvictionSuspicion,
		writeBlock:                 c.support.Append,
		createPuller:               c.createPuller,
		height:                     c.support.Height,
		triggerCatchUp:             c.triggerCatchup,
		logger:                     c.logger,
		halt: func() {
			c.Halt()
		},
	}
}

func (c *Chain) triggerCatchup(sn *raftpb.Snapshot) {
	select {
	case c.snapC <- sn:
	case <-c.doneC:
	}
}





// /*
// Copyright IBM Corp. All Rights Reserved.

// SPDX-License-Identifier: Apache-2.0
// */

// package etcdraft

// import (
// 	"context"
// 	"encoding/pem"
// 	"fmt"
// 	"sync"
// 	"sync/atomic"
// 	"time"

// 	"code.cloudfoundry.org/clock"
// 	"github.com/golang/protobuf/proto"
// 	"github.com/hyperledger/fabric/common/configtx"
// 	"github.com/hyperledger/fabric/common/flogging"
// 	"github.com/hyperledger/fabric/orderer/common/cluster"
// 	"github.com/hyperledger/fabric/orderer/consensus"
// 	"github.com/hyperledger/fabric/protos/common"
// 	"github.com/hyperledger/fabric/protos/orderer"
// 	"github.com/hyperledger/fabric/protos/orderer/etcdraft"
// 	"github.com/hyperledger/fabric/protos/utils"
// 	"github.com/pkg/errors"
// 	"go.etcd.io/etcd/raft"
// 	"go.etcd.io/etcd/raft/raftpb"
// 	"go.etcd.io/etcd/wal"
// )

// const (
// 	BYTE = 1 << (10 * iota)
// 	KILOBYTE
// 	MEGABYTE
// 	GIGABYTE
// 	TERABYTE
// )

// const (
// 	// DefaultSnapshotCatchUpEntries is the default number of entries
// 	// to preserve in memory when a snapshot is taken. This is for
// 	// slow followers to catch up.
// 	DefaultSnapshotCatchUpEntries = uint64(20)

// 	// DefaultSnapshotIntervalSize is the default snapshot interval. It is
// 	// used if SnapshotIntervalSize is not provided in channel config options.
// 	// It is needed to enforce snapshot being set.
// 	DefaultSnapshotIntervalSize = 20 * MEGABYTE // 20 MB

// 	// DefaultEvictionSuspicion is the threshold that a node will start
// 	// suspecting its own eviction if it has been leaderless for this
// 	// period of time.
// 	DefaultEvictionSuspicion = time.Minute * 10

// 	// DefaultLeaderlessCheckInterval is the interval that a chain checks
// 	// its own leadership status.
// 	DefaultLeaderlessCheckInterval = time.Second * 10
// )

// //go:generate counterfeiter -o mocks/configurator.go . Configurator

// // Configurator is used to configure the communication layer
// // when the chain starts.
// type Configurator interface {
// 	Configure(channel string, newNodes []cluster.RemoteNode)
// }

// //go:generate counterfeiter -o mocks/mock_rpc.go . RPC

// // RPC is used to mock the transport layer in tests.
// type RPC interface {
// 	SendConsensus(dest uint64, msg *orderer.ConsensusRequest) error
// 	SendSubmit(dest uint64, request *orderer.SubmitRequest) error
// }

// //go:generate counterfeiter -o mocks/mock_blockpuller.go . BlockPuller

// // BlockPuller is used to pull blocks from other OSN
// type BlockPuller interface {
// 	PullBlock(seq uint64) *common.Block
// 	HeightsByEndpoints() (map[string]uint64, error)
// 	Close()
// }

// // CreateBlockPuller is a function to create BlockPuller on demand.
// // It is passed into chain initializer so that tests could mock this.
// type CreateBlockPuller func() (BlockPuller, error)

// // Options contains all the configurations relevant to the chain.
// type Options struct {
// 	RaftID uint64

// 	Clock clock.Clock

// 	WALDir               string
// 	SnapDir              string
// 	SnapshotIntervalSize uint32

// 	// This is configurable mainly for testing purpose. Users are not
// 	// expected to alter this. Instead, DefaultSnapshotCatchUpEntries is used.
// 	SnapshotCatchUpEntries uint64

// 	MemoryStorage MemoryStorage
// 	Logger        *flogging.FabricLogger

// 	TickInterval      time.Duration
// 	ElectionTick      int
// 	HeartbeatTick     int
// 	MaxSizePerMsg     uint64
// 	MaxInflightBlocks int

// 	// BlockMetdata and Consenters should only be modified while under lock
// 	// of raftMetadataLock
// 	BlockMetadata *etcdraft.BlockMetadata
// 	Consenters    map[uint64]*etcdraft.Consenter

// 	// MigrationInit is set when the node starts right after consensus-type migration
// 	MigrationInit bool

// 	Metrics *Metrics
// 	Cert    []byte

// 	EvictionSuspicion   time.Duration
// 	LeaderCheckInterval time.Duration
// }

// type submit struct {
// 	req    *orderer.SubmitRequest
// 	leader chan uint64
// }

// type gc struct {
// 	index uint64
// 	state raftpb.ConfState
// 	data  []byte
// }

// // Chain implements consensus.Chain interface.
// type Chain struct {
// 	configurator Configurator

// 	rpc RPC

// 	raftID    uint64
// 	channelID string

// 	lastKnownLeader uint64

// 	submitC  chan *submit
// 	applyC   chan apply
// 	observeC chan<- raft.SoftState // Notifies external observer on leader change (passed in optionally as an argument for tests)
// 	haltC    chan struct{}         // Signals to goroutines that the chain is halting
// 	doneC    chan struct{}         // Closes when the chain halts
// 	startC   chan struct{}         // Closes when the node is started
// 	snapC    chan *raftpb.Snapshot // Signal to catch up with snapshot
// 	gcC      chan *gc              // Signal to take snapshot

// 	errorCLock sync.RWMutex
// 	errorC     chan struct{} // returned by Errored()

// 	raftMetadataLock     sync.RWMutex
// 	confChangeInProgress *raftpb.ConfChange
// 	justElected          bool // this is true when node has just been elected
// 	configInflight       bool // this is true when there is config block or ConfChange in flight
// 	blockInflight        int  // number of in flight blocks

// 	clock clock.Clock // Tests can inject a fake clock

// 	support consensus.ConsenterSupport

// 	lastBlock    *common.Block
// 	appliedIndex uint64

// 	// needed by snapshotting
// 	sizeLimit        uint32 // SnapshotIntervalSize in bytes
// 	accDataSize      uint32 // accumulative data size since last snapshot
// 	lastSnapBlockNum uint64
// 	confState        raftpb.ConfState // Etcdraft requires ConfState to be persisted within snapshot

// 	createPuller CreateBlockPuller // func used to create BlockPuller on demand

// 	fresh bool // indicate if this is a fresh raft node

// 	// this is exported so that test can use `Node.Status()` to get raft node status.
// 	Node *node
// 	opts Options

// 	Metrics *Metrics
// 	logger  *flogging.FabricLogger

// 	periodicChecker *PeriodicCheck

// 	haltCallback func()
// }

// // NewChain constructs a chain object.
// func NewChain(
// 	support consensus.ConsenterSupport,
// 	opts Options,
// 	conf Configurator,
// 	rpc RPC,
// 	f CreateBlockPuller,
// 	haltCallback func(),
// 	observeC chan<- raft.SoftState) (*Chain, error) {

// 	lg := opts.Logger.With("channel", support.ChainID(), "node", opts.RaftID)

// 	fresh := !wal.Exist(opts.WALDir)
// 	storage, err := CreateStorage(lg, opts.WALDir, opts.SnapDir, opts.MemoryStorage)
// 	if err != nil {
// 		return nil, errors.Errorf("failed to restore persisted raft data: %s", err)
// 	}

// 	if opts.SnapshotCatchUpEntries == 0 {
// 		storage.SnapshotCatchUpEntries = DefaultSnapshotCatchUpEntries
// 	} else {
// 		storage.SnapshotCatchUpEntries = opts.SnapshotCatchUpEntries
// 	}

// 	sizeLimit := opts.SnapshotIntervalSize
// 	if sizeLimit == 0 {
// 		sizeLimit = DefaultSnapshotIntervalSize
// 	}

// 	// get block number in last snapshot, if exists
// 	var snapBlkNum uint64
// 	var cc raftpb.ConfState
// 	if s := storage.Snapshot(); !raft.IsEmptySnap(s) {
// 		b := utils.UnmarshalBlockOrPanic(s.Data)
// 		snapBlkNum = b.Header.Number
// 		cc = s.Metadata.ConfState
// 	}

// 	b := support.Block(support.Height() - 1)
// 	if b == nil {
// 		return nil, errors.Errorf("failed to get last block")
// 	}

// 	c := &Chain{
// 		configurator:     conf,
// 		rpc:              rpc,
// 		channelID:        support.ChainID(),
// 		raftID:           opts.RaftID,
// 		submitC:          make(chan *submit),
// 		applyC:           make(chan apply),
// 		haltC:            make(chan struct{}),
// 		doneC:            make(chan struct{}),
// 		startC:           make(chan struct{}),
// 		snapC:            make(chan *raftpb.Snapshot),
// 		errorC:           make(chan struct{}),
// 		gcC:              make(chan *gc),
// 		observeC:         observeC,
// 		support:          support,
// 		fresh:            fresh,
// 		appliedIndex:     opts.BlockMetadata.RaftIndex,
// 		lastBlock:        b,
// 		sizeLimit:        sizeLimit,
// 		lastSnapBlockNum: snapBlkNum,
// 		confState:        cc,
// 		createPuller:     f,
// 		clock:            opts.Clock,
// 		haltCallback:     haltCallback,
// 		Metrics: &Metrics{
// 			ClusterSize:             opts.Metrics.ClusterSize.With("channel", support.ChainID()),
// 			IsLeader:                opts.Metrics.IsLeader.With("channel", support.ChainID()),
// 			CommittedBlockNumber:    opts.Metrics.CommittedBlockNumber.With("channel", support.ChainID()),
// 			SnapshotBlockNumber:     opts.Metrics.SnapshotBlockNumber.With("channel", support.ChainID()),
// 			LeaderChanges:           opts.Metrics.LeaderChanges.With("channel", support.ChainID()),
// 			ProposalFailures:        opts.Metrics.ProposalFailures.With("channel", support.ChainID()),
// 			DataPersistDuration:     opts.Metrics.DataPersistDuration.With("channel", support.ChainID()),
// 			NormalProposalsReceived: opts.Metrics.NormalProposalsReceived.With("channel", support.ChainID()),
// 			ConfigProposalsReceived: opts.Metrics.ConfigProposalsReceived.With("channel", support.ChainID()),
// 		},
// 		logger: lg,
// 		opts:   opts,
// 	}

// 	// Sets initial values for metrics
// 	c.Metrics.ClusterSize.Set(float64(len(c.opts.BlockMetadata.ConsenterIds)))
// 	c.Metrics.IsLeader.Set(float64(0)) // all nodes start out as followers
// 	c.Metrics.CommittedBlockNumber.Set(float64(c.lastBlock.Header.Number))
// 	c.Metrics.SnapshotBlockNumber.Set(float64(c.lastSnapBlockNum))

// 	// DO NOT use Applied option in config, see https://github.com/etcd-io/etcd/issues/10217
// 	// We guard against replay of written blocks with `appliedIndex` instead.
// 	config := &raft.Config{
// 		ID:              c.raftID,
// 		ElectionTick:    c.opts.ElectionTick,
// 		HeartbeatTick:   c.opts.HeartbeatTick,
// 		MaxSizePerMsg:   c.opts.MaxSizePerMsg,
// 		MaxInflightMsgs: c.opts.MaxInflightBlocks,
// 		Logger:          c.logger,
// 		Storage:         c.opts.MemoryStorage,
// 		// PreVote prevents reconnected node from disturbing network.
// 		// See etcd/raft doc for more details.
// 		PreVote:                   true,
// 		CheckQuorum:               true,
// 		DisableProposalForwarding: true, // This prevents blocks from being accidentally proposed by followers
// 	}

// 	c.Node = &node{
// 		chainID:      c.channelID,
// 		chain:        c,
// 		logger:       c.logger,
// 		metrics:      c.Metrics,
// 		storage:      storage,
// 		rpc:          c.rpc,
// 		config:       config,
// 		tickInterval: c.opts.TickInterval,
// 		clock:        c.clock,
// 		metadata:     c.opts.BlockMetadata,
// 	}

// 	return c, nil
// }

// // Start instructs the orderer to begin serving the chain and keep it current.
// func (c *Chain) Start() {
// 	c.logger.Infof("Starting Raft node")

// 	if err := c.configureComm(); err != nil {
// 		c.logger.Errorf("Failed to start chain, aborting: +%v", err)
// 		close(c.doneC)
// 		return
// 	}

// 	isJoin := c.support.Height() > 1
// 	if isJoin && c.opts.MigrationInit {
// 		isJoin = false
// 		c.logger.Infof("Consensus-type migration detected, starting new raft node on an existing channel; height=%d", c.support.Height())
// 	}
// 	c.Node.start(c.fresh, isJoin)

// 	close(c.startC)
// 	close(c.errorC)

// 	go c.gc()
// 	go c.serveRequest()

// 	es := c.newEvictionSuspector()

// 	interval := DefaultLeaderlessCheckInterval
// 	if c.opts.LeaderCheckInterval != 0 {
// 		interval = c.opts.LeaderCheckInterval
// 	}

// 	c.periodicChecker = &PeriodicCheck{
// 		Logger:        c.logger,
// 		Report:        es.confirmSuspicion,
// 		ReportCleared: es.clearSuspicion,
// 		CheckInterval: interval,
// 		Condition:     c.suspectEviction,
// 	}
// 	c.periodicChecker.Run()
// }

// // Order submits normal type transactions for ordering.
// func (c *Chain) Order(env *common.Envelope, configSeq uint64) error {
// 	c.Metrics.NormalProposalsReceived.Add(1)
// 	return c.Submit(&orderer.SubmitRequest{LastValidationSeq: configSeq, Payload: env, Channel: c.channelID}, 0)
// }

// // Configure submits config type transactions for ordering.
// func (c *Chain) Configure(env *common.Envelope, configSeq uint64) error {
// 	c.Metrics.ConfigProposalsReceived.Add(1)
// 	if err := c.checkConfigUpdateValidity(env); err != nil {
// 		c.logger.Warnf("Rejected config: %s", err)
// 		c.Metrics.ProposalFailures.Add(1)
// 		return err
// 	}
// 	return c.Submit(&orderer.SubmitRequest{LastValidationSeq: configSeq, Payload: env, Channel: c.channelID}, 0)
// }

// // Validate the config update for being of Type A or Type B as described in the design doc.
// func (c *Chain) checkConfigUpdateValidity(ctx *common.Envelope) error {
// 	var err error
// 	payload, err := utils.UnmarshalPayload(ctx.Payload)
// 	if err != nil {
// 		return err
// 	}
// 	chdr, err := utils.UnmarshalChannelHeader(payload.Header.ChannelHeader)
// 	if err != nil {
// 		return err
// 	}

// 	if chdr.Type != int32(common.HeaderType_ORDERER_TRANSACTION) &&
// 		chdr.Type != int32(common.HeaderType_CONFIG) {
// 		return errors.Errorf("config transaction has unknown header type: %s", common.HeaderType(chdr.Type))
// 	}

// 	if chdr.Type == int32(common.HeaderType_ORDERER_TRANSACTION) {
// 		newChannelConfig, err := utils.UnmarshalEnvelope(payload.Data)
// 		if err != nil {
// 			return err
// 		}

// 		payload, err = utils.UnmarshalPayload(newChannelConfig.Payload)
// 		if err != nil {
// 			return err
// 		}
// 	}

// 	configUpdate, err := configtx.UnmarshalConfigUpdateFromPayload(payload)
// 	if err != nil {
// 		return err
// 	}

// 	metadata, err := MetadataFromConfigUpdate(configUpdate)
// 	if err != nil {
// 		return err
// 	}

// 	if metadata == nil {
// 		return nil // ConsensusType is not updated
// 	}

// 	if err = CheckConfigMetadata(metadata); err != nil {
// 		return err
// 	}

// 	switch chdr.Type {
// 	case int32(common.HeaderType_ORDERER_TRANSACTION):
// 		c.raftMetadataLock.RLock()
// 		set := MembershipByCert(c.opts.Consenters)
// 		c.raftMetadataLock.RUnlock()

// 		for _, c := range metadata.Consenters {
// 			if _, exits := set[string(c.ClientTlsCert)]; !exits {
// 				return errors.Errorf("new channel has consenter that is not part of system consenter set")
// 			}
// 		}

// 		return nil

// 	case int32(common.HeaderType_CONFIG):
// 		c.raftMetadataLock.RLock()
// 		_, err = ComputeMembershipChanges(c.opts.BlockMetadata, c.opts.Consenters, metadata.Consenters)
// 		c.raftMetadataLock.RUnlock()

// 		return err

// 	default:
// 		// panic here because we have just check header type and return early
// 		c.logger.Panicf("Programming error, unknown header type")
// 	}

// 	return nil
// }

// // WaitReady blocks when the chain:
// // - is catching up with other nodes using snapshot
// //
// // In any other case, it returns right away.
// func (c *Chain) WaitReady() error {
// 	if err := c.isRunning(); err != nil {
// 		return err
// 	}

// 	select {
// 	case c.submitC <- nil:
// 	case <-c.doneC:
// 		return errors.Errorf("chain is stopped")
// 	}

// 	return nil
// }

// // Errored returns a channel that closes when the chain stops.
// func (c *Chain) Errored() <-chan struct{} {
// 	c.errorCLock.RLock()
// 	defer c.errorCLock.RUnlock()
// 	return c.errorC
// }

// // Halt stops the chain.
// func (c *Chain) Halt() {
// 	select {
// 	case <-c.startC:
// 	default:
// 		c.logger.Warnf("Attempted to halt a chain that has not started")
// 		return
// 	}

// 	select {
// 	case c.haltC <- struct{}{}:
// 	case <-c.doneC:
// 		return
// 	}
// 	<-c.doneC

// 	if c.haltCallback != nil {
// 		c.haltCallback()
// 	}
// }

// func (c *Chain) isRunning() error {
// 	select {
// 	case <-c.startC:
// 	default:
// 		return errors.Errorf("chain is not started")
// 	}

// 	select {
// 	case <-c.doneC:
// 		return errors.Errorf("chain is stopped")
// 	default:
// 	}

// 	return nil
// }

// // Consensus passes the given ConsensusRequest message to the raft.Node instance
// func (c *Chain) Consensus(req *orderer.ConsensusRequest, sender uint64) error {
// 	if err := c.isRunning(); err != nil {
// 		return err
// 	}

// 	stepMsg := &raftpb.Message{}
// 	if err := proto.Unmarshal(req.Payload, stepMsg); err != nil {
// 		return fmt.Errorf("failed to unmarshal StepRequest payload to Raft Message: %s", err)
// 	}

// 	if err := c.Node.Step(context.TODO(), *stepMsg); err != nil {
// 		return fmt.Errorf("failed to process Raft Step message: %s", err)
// 	}

// 	return nil
// }

// // Submit forwards the incoming request to:
// // - the local serveRequest goroutine if this is leader
// // - the actual leader via the transport mechanism
// // The call fails if there's no leader elected yet.
// func (c *Chain) Submit(req *orderer.SubmitRequest, sender uint64) error {
// 	if err := c.isRunning(); err != nil {
// 		c.Metrics.ProposalFailures.Add(1)
// 		return err
// 	}

// 	leadC := make(chan uint64, 1)
// 	select {
// 	case c.submitC <- &submit{req, leadC}:
// 		lead := <-leadC
// 		if lead == raft.None {
// 			c.Metrics.ProposalFailures.Add(1)
// 			return errors.Errorf("no Raft leader")
// 		}

// 		if lead != c.raftID {
// 			if err := c.rpc.SendSubmit(lead, req); err != nil {
// 				c.Metrics.ProposalFailures.Add(1)
// 				return err
// 			}
// 		}

// 	case <-c.doneC:
// 		c.Metrics.ProposalFailures.Add(1)
// 		return errors.Errorf("chain is stopped")
// 	}

// 	return nil
// }

// type apply struct {
// 	entries []raftpb.Entry
// 	soft    *raft.SoftState
// }

// func isCandidate(state raft.StateType) bool {
// 	return state == raft.StatePreCandidate || state == raft.StateCandidate
// }

// func (c *Chain) serveRequest() {
// 	ticking := false
// 	timer := c.clock.NewTimer(time.Second)
// 	// we need a stopped timer rather than nil,
// 	// because we will be select waiting on timer.C()
// 	if !timer.Stop() {
// 		<-timer.C()
// 	}

// 	// if timer is already started, this is a no-op
// 	startTimer := func() {
// 		if !ticking {
// 			ticking = true
// 			timer.Reset(c.support.SharedConfig().BatchTimeout())
// 		}
// 	}

// 	stopTimer := func() {
// 		if !timer.Stop() && ticking {
// 			// we only need to drain the channel if the timer expired (not explicitly stopped)
// 			<-timer.C()
// 		}
// 		ticking = false
// 	}

// 	var soft raft.SoftState
// 	submitC := c.submitC
// 	var bc *blockCreator

// 	var propC chan<- *common.Block
// 	var cancelProp context.CancelFunc
// 	cancelProp = func() {} // no-op as initial value

// 	becomeLeader := func() (chan<- *common.Block, context.CancelFunc) {
// 		c.Metrics.IsLeader.Set(1)

// 		c.blockInflight = 0
// 		c.justElected = true
// 		submitC = nil
// 		ch := make(chan *common.Block, c.opts.MaxInflightBlocks)

// 		// if there is unfinished ConfChange, we should resume the effort to propose it as
// 		// new leader, and wait for it to be committed before start serving new requests.
// 		if cc := c.getInFlightConfChange(); cc != nil {
// 			// The reason `ProposeConfChange` should be called in go routine is documented in `writeConfigBlock` method.
// 			go func() {
// 				if err := c.Node.ProposeConfChange(context.TODO(), *cc); err != nil {
// 					c.logger.Warnf("Failed to propose configuration update to Raft node: %s", err)
// 				}
// 			}()

// 			c.confChangeInProgress = cc
// 			c.configInflight = true
// 		}

// 		// Leader should call Propose in go routine, because this method may be blocked
// 		// if node is leaderless (this can happen when leader steps down in a heavily
// 		// loaded network). We need to make sure applyC can still be consumed properly.
// 		ctx, cancel := context.WithCancel(context.Background())
// 		go func(ctx context.Context, ch <-chan *common.Block) {
// 			for {
// 				select {
// 				case b := <-ch:
// 					data := utils.MarshalOrPanic(b)
// 					if err := c.Node.Propose(ctx, data); err != nil {
// 						c.logger.Errorf("Failed to propose block [%d] to raft and discard %d blocks in queue: %s", b.Header.Number, len(ch), err)
// 						return
// 					}
// 					c.logger.Debugf("Proposed block [%d] to raft consensus", b.Header.Number)

// 				case <-ctx.Done():
// 					c.logger.Debugf("Quit proposing blocks, discarded %d blocks in the queue", len(ch))
// 					return
// 				}
// 			}
// 		}(ctx, ch)

// 		return ch, cancel
// 	}

// 	becomeFollower := func() {
// 		cancelProp()
// 		c.blockInflight = 0
// 		_ = c.support.BlockCutter().Cut()
// 		stopTimer()
// 		submitC = c.submitC
// 		bc = nil
// 		c.Metrics.IsLeader.Set(0)
// 	}

// 	for {
// 		select {
// 		case s := <-submitC:
// 			if s == nil {
// 				// polled by `WaitReady`
// 				continue
// 			}

// 			if soft.RaftState == raft.StatePreCandidate || soft.RaftState == raft.StateCandidate {
// 				s.leader <- raft.None
// 				continue
// 			}

// 			s.leader <- soft.Lead
// 			if soft.Lead != c.raftID {
// 				continue
// 			}

// 			batches, pending, err := c.ordered(s.req)
// 			if err != nil {
// 				c.logger.Errorf("Failed to order message: %s", err)
// 				continue
// 			}
// 			if pending {
// 				startTimer() // no-op if timer is already started
// 			} else {
// 				stopTimer()
// 			}

// 			c.propose(propC, bc, batches...)

// 			if c.configInflight {
// 				c.logger.Info("Received config transaction, pause accepting transaction till it is committed")
// 				submitC = nil
// 			} else if c.blockInflight >= c.opts.MaxInflightBlocks {
// 				c.logger.Debugf("Number of in-flight blocks (%d) reaches limit (%d), pause accepting transaction",
// 					c.blockInflight, c.opts.MaxInflightBlocks)
// 				submitC = nil
// 			}

// 		case app := <-c.applyC:
// 			if app.soft != nil {
// 				newLeader := atomic.LoadUint64(&app.soft.Lead) // etcdraft requires atomic access
// 				if newLeader != soft.Lead {
// 					c.logger.Infof("Raft leader changed: %d -> %d", soft.Lead, newLeader)
// 					c.Metrics.LeaderChanges.Add(1)

// 					atomic.StoreUint64(&c.lastKnownLeader, newLeader)

// 					if newLeader == c.raftID {
// 						propC, cancelProp = becomeLeader()
// 					}

// 					if soft.Lead == c.raftID {
// 						becomeFollower()
// 					}
// 				}

// 				foundLeader := soft.Lead == raft.None && newLeader != raft.None
// 				quitCandidate := isCandidate(soft.RaftState) && !isCandidate(app.soft.RaftState)

// 				if foundLeader || quitCandidate {
// 					c.errorCLock.Lock()
// 					c.errorC = make(chan struct{})
// 					c.errorCLock.Unlock()
// 				}

// 				if isCandidate(app.soft.RaftState) || newLeader == raft.None {
// 					atomic.StoreUint64(&c.lastKnownLeader, raft.None)
// 					select {
// 					case <-c.errorC:
// 					default:
// 						nodeCount := len(c.opts.BlockMetadata.ConsenterIds)
// 						// Only close the error channel (to signal the broadcast/deliver front-end a consensus backend error)
// 						// If we are a cluster of size 3 or more, otherwise we can't expand a cluster of size 1 to 2 nodes.
// 						if nodeCount > 2 {
// 							close(c.errorC)
// 						} else {
// 							c.logger.Warningf("No leader is present, cluster size is %d", nodeCount)
// 						}
// 					}
// 				}

// 				soft = raft.SoftState{Lead: newLeader, RaftState: app.soft.RaftState}

// 				// notify external observer
// 				select {
// 				case c.observeC <- soft:
// 				default:
// 				}
// 			}

// 			c.apply(app.entries)

// 			if c.justElected {
// 				msgInflight := c.Node.lastIndex() > c.appliedIndex
// 				if msgInflight {
// 					c.logger.Debugf("There are in flight blocks, new leader should not serve requests")
// 					continue
// 				}

// 				if c.configInflight {
// 					c.logger.Debugf("There is config block in flight, new leader should not serve requests")
// 					continue
// 				}

// 				c.logger.Infof("Start accepting requests as Raft leader at block [%d]", c.lastBlock.Header.Number)
// 				bc = &blockCreator{
// 					hash:   c.lastBlock.Header.Hash(),
// 					number: c.lastBlock.Header.Number,
// 					logger: c.logger,
// 				}
// 				submitC = c.submitC
// 				c.justElected = false
// 			} else if c.configInflight {
// 				c.logger.Info("Config block or ConfChange in flight, pause accepting transaction")
// 				submitC = nil
// 			} else if c.blockInflight < c.opts.MaxInflightBlocks {
// 				submitC = c.submitC
// 			}

// 		case <-timer.C():
// 			ticking = false

// 			batch := c.support.BlockCutter().Cut()
// 			if len(batch) == 0 {
// 				c.logger.Warningf("Batch timer expired with no pending requests, this might indicate a bug")
// 				continue
// 			}

// 			c.logger.Debugf("Batch timer expired, creating block")
// 			c.propose(propC, bc, batch) // we are certain this is normal block, no need to block

// 		case sn := <-c.snapC:
// 			if sn.Metadata.Index != 0 {
// 				if sn.Metadata.Index <= c.appliedIndex {
// 					c.logger.Debugf("Skip snapshot taken at index %d, because it is behind current applied index %d", sn.Metadata.Index, c.appliedIndex)
// 					break
// 				}

// 				c.confState = sn.Metadata.ConfState
// 				c.appliedIndex = sn.Metadata.Index
// 			} else {
// 				c.logger.Infof("Received artificial snapshot to trigger catchup")
// 			}

// 			if err := c.catchUp(sn); err != nil {
// 				c.logger.Panicf("Failed to recover from snapshot taken at Term %d and Index %d: %s",
// 					sn.Metadata.Term, sn.Metadata.Index, err)
// 			}

// 		case <-c.doneC:
// 			cancelProp()

// 			select {
// 			case <-c.errorC: // avoid closing closed channel
// 			default:
// 				close(c.errorC)
// 			}

// 			c.logger.Infof("Stop serving requests")
// 			c.periodicChecker.Stop()
// 			return
// 		}
// 	}
// }

// func (c *Chain) writeBlock(block *common.Block, index uint64) {
// 	if block.Header.Number > c.lastBlock.Header.Number+1 {
// 		c.logger.Panicf("Got block [%d], expect block [%d]", block.Header.Number, c.lastBlock.Header.Number+1)
// 	} else if block.Header.Number < c.lastBlock.Header.Number+1 {
// 		c.logger.Infof("Got block [%d], expect block [%d], this node was forced to catch up", block.Header.Number, c.lastBlock.Header.Number+1)
// 		return
// 	}

// 	if c.blockInflight > 0 {
// 		c.blockInflight-- // only reduce on leader
// 	}
// 	c.lastBlock = block

// 	c.logger.Infof("Writing block [%d] (Raft index: %d) to ledger", block.Header.Number, index)

// 	if utils.IsConfigBlock(block) {
// 		c.writeConfigBlock(block, index)
// 		return
// 	}

// 	c.raftMetadataLock.Lock()
// 	c.opts.BlockMetadata.RaftIndex = index
// 	m := utils.MarshalOrPanic(c.opts.BlockMetadata)
// 	c.raftMetadataLock.Unlock()

// 	c.support.WriteBlock(block, m)
// }

// // Orders the envelope in the `msg` content. SubmitRequest.
// // Returns
// //   -- batches [][]*common.Envelope; the batches cut,
// //   -- pending bool; if there are envelopes pending to be ordered,
// //   -- err error; the error encountered, if any.
// // It takes care of config messages as well as the revalidation of messages if the config sequence has advanced.
// func (c *Chain) ordered(msg *orderer.SubmitRequest) (batches [][]*common.Envelope, pending bool, err error) {
// 	seq := c.support.Sequence()

// 	if c.isConfig(msg.Payload) {
// 		// ConfigMsg
// 		if msg.LastValidationSeq < seq {
// 			c.logger.Warnf("Config message was validated against %d, although current config seq has advanced (%d)", msg.LastValidationSeq, seq)
// 			msg.Payload, _, err = c.support.ProcessConfigMsg(msg.Payload)
// 			if err != nil {
// 				c.Metrics.ProposalFailures.Add(1)
// 				return nil, true, errors.Errorf("bad config message: %s", err)
// 			}

// 			if err = c.checkConfigUpdateValidity(msg.Payload); err != nil {
// 				c.Metrics.ProposalFailures.Add(1)
// 				return nil, true, errors.Errorf("bad config message: %s", err)
// 			}
// 		}
// 		batch := c.support.BlockCutter().Cut()
// 		batches = [][]*common.Envelope{}
// 		if len(batch) != 0 {
// 			batches = append(batches, batch)
// 		}
// 		batches = append(batches, []*common.Envelope{msg.Payload})
// 		return batches, false, nil
// 	}
// 	// it is a normal message
// 	if msg.LastValidationSeq < seq {
// 		c.logger.Warnf("Normal message was validated against %d, although current config seq has advanced (%d)", msg.LastValidationSeq, seq)
// 		if _, err := c.support.ProcessNormalMsg(msg.Payload); err != nil {
// 			c.Metrics.ProposalFailures.Add(1)
// 			return nil, true, errors.Errorf("bad normal message: %s", err)
// 		}
// 	}
// 	batches, pending = c.support.BlockCutter().Ordered(msg.Payload)
// 	return batches, pending, nil

// }

// func (c *Chain) propose(ch chan<- *common.Block, bc *blockCreator, batches ...[]*common.Envelope) {
// 	for _, batch := range batches {
// 		b := bc.createNextBlock(batch)
// 		c.logger.Infof("Created block [%d], there are %d blocks in flight", b.Header.Number, c.blockInflight)

// 		select {
// 		case ch <- b:
// 		default:
// 			c.logger.Panic("Programming error: limit of in-flight blocks does not properly take effect or block is proposed by follower")
// 		}

// 		// if it is config block, then we should wait for the commit of the block
// 		if utils.IsConfigBlock(b) {
// 			c.configInflight = true
// 		}

// 		c.blockInflight++
// 	}

// 	return
// }

// func (c *Chain) catchUp(snap *raftpb.Snapshot) error {
// 	b, err := utils.UnmarshalBlock(snap.Data)
// 	if err != nil {
// 		return errors.Errorf("failed to unmarshal snapshot data to block: %s", err)
// 	}

// 	if c.lastBlock.Header.Number >= b.Header.Number {
// 		c.logger.Warnf("Snapshot is at block [%d], local block number is %d, no sync needed", b.Header.Number, c.lastBlock.Header.Number)
// 		return nil
// 	}

// 	puller, err := c.createPuller()
// 	if err != nil {
// 		return errors.Errorf("failed to create block puller: %s", err)
// 	}
// 	defer puller.Close()

// 	next := c.lastBlock.Header.Number + 1

// 	c.logger.Infof("Catching up with snapshot taken at block [%d], starting from block [%d]", b.Header.Number, next)

// 	for next <= b.Header.Number {
// 		block := puller.PullBlock(next)
// 		if block == nil {
// 			return errors.Errorf("failed to fetch block [%d] from cluster", next)
// 		}
// 		if utils.IsConfigBlock(block) {
// 			c.support.WriteConfigBlock(block, nil)

// 			configMembership := c.detectConfChange(block)

// 			if configMembership != nil && configMembership.Changed() {
// 				c.logger.Infof("Config block [%d] changes consenter set, communication should be reconfigured", block.Header.Number)

// 				c.raftMetadataLock.Lock()
// 				c.opts.BlockMetadata = configMembership.NewBlockMetadata
// 				c.opts.Consenters = configMembership.NewConsenters
// 				c.raftMetadataLock.Unlock()

// 				if err := c.configureComm(); err != nil {
// 					c.logger.Panicf("Failed to configure communication: %s", err)
// 				}
// 			}
// 		} else {
// 			c.support.WriteBlock(block, nil)
// 		}

// 		c.lastBlock = block
// 		next++
// 	}

// 	c.logger.Infof("Finished syncing with cluster up to and including block [%d]", b.Header.Number)
// 	return nil
// }

// func (c *Chain) detectConfChange(block *common.Block) *MembershipChanges {
// 	// If config is targeting THIS channel, inspect consenter set and
// 	// propose raft ConfChange if it adds/removes node.
// 	configMetadata := c.newConfigMetadata(block)

// 	if configMetadata == nil {
// 		return nil
// 	}

// 	if configMetadata.Options != nil &&
// 		configMetadata.Options.SnapshotIntervalSize != 0 &&
// 		configMetadata.Options.SnapshotIntervalSize != c.sizeLimit {
// 		c.logger.Infof("Update snapshot interval size to %d bytes (was %d)",
// 			configMetadata.Options.SnapshotIntervalSize, c.sizeLimit)
// 		c.sizeLimit = configMetadata.Options.SnapshotIntervalSize
// 	}

// 	changes, err := ComputeMembershipChanges(c.opts.BlockMetadata, c.opts.Consenters, configMetadata.Consenters)
// 	if err != nil {
// 		c.logger.Panicf("illegal configuration change detected: %s", err)
// 	}

// 	if changes.Rotated() {
// 		c.logger.Infof("Config block [%d] rotates TLS certificate of node %d", block.Header.Number, changes.RotatedNode)
// 	}

// 	return changes
// }

// func (c *Chain) apply(ents []raftpb.Entry) {
// 	if len(ents) == 0 {
// 		return
// 	}

// 	if ents[0].Index > c.appliedIndex+1 {
// 		c.logger.Panicf("first index of committed entry[%d] should <= appliedIndex[%d]+1", ents[0].Index, c.appliedIndex)
// 	}

// 	var position int
// 	for i := range ents {
// 		switch ents[i].Type {
// 		case raftpb.EntryNormal:
// 			if len(ents[i].Data) == 0 {
// 				break
// 			}

// 			position = i
// 			c.accDataSize += uint32(len(ents[i].Data))

// 			// We need to strictly avoid re-applying normal entries,
// 			// otherwise we are writing the same block twice.
// 			if ents[i].Index <= c.appliedIndex {
// 				c.logger.Debugf("Received block with raft index (%d) <= applied index (%d), skip", ents[i].Index, c.appliedIndex)
// 				break
// 			}

// 			block := utils.UnmarshalBlockOrPanic(ents[i].Data)
// 			c.writeBlock(block, ents[i].Index)
// 			c.Metrics.CommittedBlockNumber.Set(float64(block.Header.Number))

// 		case raftpb.EntryConfChange:
// 			var cc raftpb.ConfChange
// 			if err := cc.Unmarshal(ents[i].Data); err != nil {
// 				c.logger.Warnf("Failed to unmarshal ConfChange data: %s", err)
// 				continue
// 			}

// 			c.confState = *c.Node.ApplyConfChange(cc)

// 			switch cc.Type {
// 			case raftpb.ConfChangeAddNode:
// 				c.logger.Infof("Applied config change to add node %d, current nodes in channel: %+v", cc.NodeID, c.confState.Nodes)
// 			case raftpb.ConfChangeRemoveNode:
// 				c.logger.Infof("Applied config change to remove node %d, current nodes in channel: %+v", cc.NodeID, c.confState.Nodes)
// 			default:
// 				c.logger.Panic("Programming error, encountered unsupported raft config change")
// 			}

// 			// This ConfChange was introduced by a previously committed config block,
// 			// we can now unblock submitC to accept envelopes.
// 			if c.confChangeInProgress != nil &&
// 				c.confChangeInProgress.NodeID == cc.NodeID &&
// 				c.confChangeInProgress.Type == cc.Type {

// 				if err := c.configureComm(); err != nil {
// 					c.logger.Panicf("Failed to configure communication: %s", err)
// 				}

// 				c.confChangeInProgress = nil
// 				c.configInflight = false
// 				// report the new cluster size
// 				c.Metrics.ClusterSize.Set(float64(len(c.opts.BlockMetadata.ConsenterIds)))
// 			}

// 			if cc.Type == raftpb.ConfChangeRemoveNode && cc.NodeID == c.raftID {
// 				c.logger.Infof("Current node removed from replica set for channel %s", c.channelID)
// 				// calling goroutine, since otherwise it will be blocked
// 				// trying to write into haltC
// 				lead := atomic.LoadUint64(&c.lastKnownLeader)
// 				if lead == c.raftID {
// 					c.logger.Info("This node is being removed as current leader, halt with delay")
// 					c.configInflight = true // toggle the flag so this node does not accept further tx
// 					go func() {
// 						select {
// 						case <-c.clock.After(time.Duration(c.opts.ElectionTick) * c.opts.TickInterval):
// 						case <-c.doneC:
// 						}

// 						c.Halt()
// 					}()
// 				} else {
// 					go c.Halt()
// 				}
// 			}
// 		}

// 		if ents[i].Index > c.appliedIndex {
// 			c.appliedIndex = ents[i].Index
// 		}
// 	}

// 	if c.accDataSize >= c.sizeLimit {
// 		b := utils.UnmarshalBlockOrPanic(ents[position].Data)

// 		select {
// 		case c.gcC <- &gc{index: c.appliedIndex, state: c.confState, data: ents[position].Data}:
// 			c.logger.Infof("Accumulated %d bytes since last snapshot, exceeding size limit (%d bytes), "+
// 				"taking snapshot at block [%d] (index: %d), last snapshotted block number is %d, current nodes: %+v",
// 				c.accDataSize, c.sizeLimit, b.Header.Number, c.appliedIndex, c.lastSnapBlockNum, c.confState.Nodes)
// 			c.accDataSize = 0
// 			c.lastSnapBlockNum = b.Header.Number
// 			c.Metrics.SnapshotBlockNumber.Set(float64(b.Header.Number))
// 		default:
// 			c.logger.Warnf("Snapshotting is in progress, it is very likely that SnapshotIntervalSize is too small")
// 		}
// 	}

// 	return
// }

// func (c *Chain) gc() {
// 	for {
// 		select {
// 		case g := <-c.gcC:
// 			c.Node.takeSnapshot(g.index, g.state, g.data)
// 		case <-c.doneC:
// 			c.logger.Infof("Stop garbage collecting")
// 			return
// 		}
// 	}
// }

// func (c *Chain) isConfig(env *common.Envelope) bool {
// 	h, err := utils.ChannelHeader(env)
// 	if err != nil {
// 		c.logger.Panicf("failed to extract channel header from envelope")
// 	}

// 	return h.Type == int32(common.HeaderType_CONFIG) || h.Type == int32(common.HeaderType_ORDERER_TRANSACTION)
// }

// func (c *Chain) configureComm() error {
// 	// Reset unreachable map when communication is reconfigured
// 	c.Node.unreachableLock.Lock()
// 	c.Node.unreachable = make(map[uint64]struct{})
// 	c.Node.unreachableLock.Unlock()

// 	nodes, err := c.remotePeers()
// 	if err != nil {
// 		return err
// 	}

// 	c.configurator.Configure(c.channelID, nodes)
// 	return nil
// }

// func (c *Chain) remotePeers() ([]cluster.RemoteNode, error) {
// 	c.raftMetadataLock.RLock()
// 	defer c.raftMetadataLock.RUnlock()

// 	var nodes []cluster.RemoteNode
// 	for raftID, consenter := range c.opts.Consenters {
// 		// No need to know yourself
// 		if raftID == c.raftID {
// 			continue
// 		}
// 		serverCertAsDER, err := pemToDER(consenter.ServerTlsCert, raftID, "server", c.logger)
// 		if err != nil {
// 			return nil, errors.WithStack(err)
// 		}
// 		clientCertAsDER, err := pemToDER(consenter.ClientTlsCert, raftID, "client", c.logger)
// 		if err != nil {
// 			return nil, errors.WithStack(err)
// 		}
// 		nodes = append(nodes, cluster.RemoteNode{
// 			ID:            raftID,
// 			Endpoint:      fmt.Sprintf("%s:%d", consenter.Host, consenter.Port),
// 			ServerTLSCert: serverCertAsDER,
// 			ClientTLSCert: clientCertAsDER,
// 		})
// 	}
// 	return nodes, nil
// }

// func pemToDER(pemBytes []byte, id uint64, certType string, logger *flogging.FabricLogger) ([]byte, error) {
// 	bl, _ := pem.Decode(pemBytes)
// 	if bl == nil {
// 		logger.Errorf("Rejecting PEM block of %s TLS cert for node %d, offending PEM is: %s", certType, id, string(pemBytes))
// 		return nil, errors.Errorf("invalid PEM block")
// 	}
// 	return bl.Bytes, nil
// }

// // writeConfigBlock writes configuration blocks into the ledger in
// // addition extracts updates about raft replica set and if there
// // are changes updates cluster membership as well
// func (c *Chain) writeConfigBlock(block *common.Block, index uint64) {
// 	hdr, err := ConfigChannelHeader(block)
// 	if err != nil {
// 		c.logger.Panicf("Failed to get config header type from config block: %s", err)
// 	}

// 	c.configInflight = false

// 	switch common.HeaderType(hdr.Type) {
// 	case common.HeaderType_CONFIG:
// 		configMembership := c.detectConfChange(block)

// 		c.raftMetadataLock.Lock()
// 		c.opts.BlockMetadata.RaftIndex = index
// 		if configMembership != nil {
// 			c.opts.BlockMetadata = configMembership.NewBlockMetadata
// 			c.opts.Consenters = configMembership.NewConsenters
// 		}
// 		c.raftMetadataLock.Unlock()

// 		blockMetadataBytes := utils.MarshalOrPanic(c.opts.BlockMetadata)
// 		// write block with metadata
// 		c.support.WriteConfigBlock(block, blockMetadataBytes)

// 		if configMembership == nil {
// 			return
// 		}

// 		// update membership
// 		if configMembership.ConfChange != nil {
// 			// We need to propose conf change in a go routine, because it may be blocked if raft node
// 			// becomes leaderless, and we should not block `serveRequest` so it can keep consuming applyC,
// 			// otherwise we have a deadlock.
// 			go func() {
// 				// ProposeConfChange returns error only if node being stopped.
// 				// This proposal is dropped by followers because DisableProposalForwarding is enabled.
// 				if err := c.Node.ProposeConfChange(context.TODO(), *configMembership.ConfChange); err != nil {
// 					c.logger.Warnf("Failed to propose configuration update to Raft node: %s", err)
// 				}
// 			}()

// 			c.confChangeInProgress = configMembership.ConfChange

// 			switch configMembership.ConfChange.Type {
// 			case raftpb.ConfChangeAddNode:
// 				c.logger.Infof("Config block just committed adds node %d, pause accepting transactions till config change is applied", configMembership.ConfChange.NodeID)
// 			case raftpb.ConfChangeRemoveNode:
// 				c.logger.Infof("Config block just committed removes node %d, pause accepting transactions till config change is applied", configMembership.ConfChange.NodeID)
// 			default:
// 				c.logger.Panic("Programming error, encountered unsupported raft config change")
// 			}

// 			c.configInflight = true
// 		} else if configMembership.Rotated() {
// 			lead := atomic.LoadUint64(&c.lastKnownLeader)
// 			if configMembership.RotatedNode == lead {
// 				c.logger.Infof("Certificate of Raft leader is being rotated, attempt leader transfer before reconfiguring communication")
// 				go func() {
// 					c.Node.abdicateLeader(lead)
// 					if err := c.configureComm(); err != nil {
// 						c.logger.Panicf("Failed to configure communication: %s", err)
// 					}
// 				}()
// 			} else {
// 				if err := c.configureComm(); err != nil {
// 					c.logger.Panicf("Failed to configure communication: %s", err)
// 				}
// 			}
// 		}

// 	case common.HeaderType_ORDERER_TRANSACTION:
// 		// If this config is channel creation, no extra inspection is needed
// 		c.raftMetadataLock.Lock()
// 		c.opts.BlockMetadata.RaftIndex = index
// 		m := utils.MarshalOrPanic(c.opts.BlockMetadata)
// 		c.raftMetadataLock.Unlock()

// 		c.support.WriteConfigBlock(block, m)

// 	default:
// 		c.logger.Panicf("Programming error: unexpected config type: %s", common.HeaderType(hdr.Type))
// 	}
// }

// // getInFlightConfChange returns ConfChange in-flight if any.
// // It returns confChangeInProgress if it is not nil. Otherwise
// // it returns ConfChange from the last committed block (might be nil).
// func (c *Chain) getInFlightConfChange() *raftpb.ConfChange {
// 	if c.confChangeInProgress != nil {
// 		return c.confChangeInProgress
// 	}

// 	if c.lastBlock.Header.Number == 0 {
// 		return nil // nothing to failover just started the chain
// 	}

// 	if !utils.IsConfigBlock(c.lastBlock) {
// 		return nil
// 	}

// 	// extracting current Raft configuration state
// 	confState := c.Node.ApplyConfChange(raftpb.ConfChange{})

// 	if len(confState.Nodes) == len(c.opts.BlockMetadata.ConsenterIds) {
// 		// Raft configuration change could only add one node or
// 		// remove one node at a time, if raft conf state size is
// 		// equal to membership stored in block metadata field,
// 		// that means everything is in sync and no need to propose
// 		// config update.
// 		return nil
// 	}

// 	return ConfChange(c.opts.BlockMetadata, confState)
// }

// // newMetadata extract config metadata from the configuration block
// func (c *Chain) newConfigMetadata(block *common.Block) *etcdraft.ConfigMetadata {
// 	metadata, err := ConsensusMetadataFromConfigBlock(block)
// 	if err != nil {
// 		c.logger.Panicf("error reading consensus metadata: %s", err)
// 	}
// 	return metadata
// }

// func (c *Chain) suspectEviction() bool {
// 	if c.isRunning() != nil {
// 		return false
// 	}

// 	return atomic.LoadUint64(&c.lastKnownLeader) == uint64(0)
// }

// func (c *Chain) newEvictionSuspector() *evictionSuspector {
// 	return &evictionSuspector{
// 		amIInChannel:               ConsenterCertificate(c.opts.Cert).IsConsenterOfChannel,
// 		evictionSuspicionThreshold: c.opts.EvictionSuspicion,
// 		writeBlock:                 c.support.Append,
// 		createPuller:               c.createPuller,
// 		height:                     c.support.Height,
// 		triggerCatchUp:             c.triggerCatchup,
// 		logger:                     c.logger,
// 		halt: func() {
// 			c.Halt()
// 		},
// 	}
// }

// func (c *Chain) triggerCatchup(sn *raftpb.Snapshot) {
// 	select {
// 	case c.snapC <- sn:
// 	case <-c.doneC:
// 	}
// }
