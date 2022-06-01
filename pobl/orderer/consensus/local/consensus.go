
package local

import (
	"fmt"
	"github.com/hyperledger/fabric/core/ledger/kvledger/txmgmt/rwsetutil"
	//"github.com/hyperledger/fabric/protos/utils"
	"strings"
	"time"

	"github.com/hyperledger/fabric/common/flogging"
	"github.com/hyperledger/fabric/orderer/consensus"
	cb "github.com/hyperledger/fabric/protos/common"
)

var logger = flogging.MustGetLogger("orderer.consensus.local")

type consenter struct{}





//type chain struct {
//	support  consensus.ConsenterSupport
//	sendChan chan *message
//	exitChan chan struct{}
//}
//zs
type ReadKV struct {
	Key string
	Value []byte
	Type int64     //1: read-write, must wait or false; 2: only-write, must wait or false; 3: only-read, no wait or ture;
	ParentTxId string
}
type WriteKV struct {
	Key string
	Value []byte
	Type int64     //1: inReadSet; 0: OutofReadset
}
type GNode struct {
	TxResult *cb.Envelope
	TxRWSet *rwsetutil.TxRwSet
	TxId string
	TxCount int64
	PeerId string
	Votes int64
	ParentTxs map[string] *ReadKV
	ReadSet map[string] *ReadKV
	WriteSet map[string]*WriteKV
	State int64 //1.committed; 2.wait; 3:false
	NumParentsOfRW int64  //must wait or false
	NumParentsOfOW int64  //must wait or false
	NumParentsOfOR int64  //no wait or true

	IsCommitted int64//1.committed;2.wait
	NumPGChooseNode int64//if ==0 commit



}
type GEdge struct {
	ChildNode  *GNode
	ChildNodeType int64    //1: read-write, must wait or false; 2: only-write, must wait or false; 3: only-read, no wait or ture;
	Next *GEdge
	State int64 //1:success; 2:wait; 3:false
}
type GNodeResult struct {
	GnodeResult *GNode
	Next *GNodeResult
	State int64 //1:success; 2:wait; 3:false
}
type CommittedGnode struct {
	State int64   //1: success commit; 2: wait enough weight or parents; 3: false consensus
	committedGnode *GNode
	IsCommitted int64//1.has committed;0 not committed
}

type GChooseEdge struct {
	ChildNode  *GNode
	Next *GChooseEdge
	State int64 //1:success; 2:wait; 3:false
	IsCommitted int64//1.committed;2.wait
}



type GConsensus struct {
	GChooseEdgeSet map[string]*GChooseEdge
	GChooseNode map[string]*GNode
	//GKVTable map[string] string
	//GEdgeSet map[string] *GEdge
	GNodeInBlock map[string] *CommittedGnode
	GTxWeight map[string]int64 //all notes
	GTxRelusts map[string]*GNodeResult //votes of each results
	GnodeMaxResults map[string] *GNodeResult
	NumExeNodes int64
	NumKWeight int64
	NumTxwithAllvotes int64
	NumSendInBlock int64

	NumVotes2 int64
	NumVotes3 int64
	NumVotes4 int64
	NumGChooseNode int64
	NumGChooseEdge int64
	LastNumGChooseEdge int64

}

type chain struct {
	support  consensus.ConsenterSupport
	sendChan chan *message
	exitChan chan struct{}
	GCon GConsensus
}
//zs

type message struct {
	configSeq uint64
	normalMsg *cb.Envelope
	configMsg *cb.Envelope
}

// New creates a new consenter for the solo consensus scheme.
// The solo consensus scheme is very simple, and allows only one consenter for a given chain (this process).
// It accepts messages being delivered via Order/Configure, orders them, and then uses the blockcutter to form the messages
// into blocks before writing to the given ledger
func New() consensus.Consenter {
	return &consenter{}
}

func (solo *consenter) HandleChain(support consensus.ConsenterSupport, metadata *cb.Metadata) (consensus.Chain, error) {
	return newChain(support), nil
}

//func newChain(support consensus.ConsenterSupport) *chain {
//	return &chain{
//		support:  support,
//		sendChan: make(chan *message,1000),
//		exitChan: make(chan struct{}),
//	}
//}
func newChain(support consensus.ConsenterSupport) *chain {
	return &chain{
		support:  support,
		sendChan: make(chan *message,80000),
		exitChan: make(chan struct{}),
		GCon: GConsensus{
			GNodeInBlock: map[string]*CommittedGnode{},
			GTxWeight: map[string]int64{},
			GTxRelusts: map[string]*GNodeResult{},
			GnodeMaxResults: map[string]*GNodeResult{},
			NumExeNodes: 4,
			NumKWeight: 3,
			NumTxwithAllvotes: 0,
			NumSendInBlock: 0,

			NumVotes2: 0,
			NumVotes3: 0,
			NumVotes4: 0,
			GChooseEdgeSet: map[string]*GChooseEdge{},
			GChooseNode: map[string]*GNode{},
			NumGChooseEdge: 0,
			NumGChooseNode: 0,
			LastNumGChooseEdge: 0,
		},
	}
}

func (ch *chain) Start() {
	go ch.main()
}

func (ch *chain) Halt() {
	select {
	case <-ch.exitChan:
		// Allow multiple halts without panic
	default:
		close(ch.exitChan)
	}
}

func (ch *chain) WaitReady() error {
	return nil
}

// Order accepts normal messages for ordering
func (ch *chain) Order(env *cb.Envelope, configSeq uint64) error {
	logger.Infof("zs: LocalOrder SOLO.main() before Ordered msg")
	lenchcan:= len(ch.sendChan)
	if lenchcan>75000{
		logger.Infof("zs: case ch.sendChan sssschanlen:%d !!!@@@!!!",lenchcan)
	}else{
		logger.Infof("zs: case ch.sendChan chanlen:%d ",lenchcan)
	}

	select {
	case ch.sendChan <- &message{
		configSeq: 0,
		normalMsg: env,
	}:
		logger.Infof("zs: LocalOrder SOLO.main() after Ordered msg")
		return nil
	case <-ch.exitChan:
		logger.Infof("zs: LocalOrder SOLO.main() after Ordered msg exit")
		return fmt.Errorf("Exiting")
	}
}
// Configure accepts configuration update messages for ordering
func (ch *chain) Configure(config *cb.Envelope, configSeq uint64) error {
	select {
	case ch.sendChan <- &message{
		configSeq: configSeq,
		configMsg: config,
	}:
		return nil
	case <-ch.exitChan:
		return fmt.Errorf("Exiting")
	}
}

// Errored only closes on exit
func (ch *chain) Errored() <-chan struct{} {
	return ch.exitChan
}

func (ch *chain) GnodeCompare(gnodeNew *GNode, gnodeOld *GNode) bool {

	txRWSet:=gnodeNew.TxRWSet
	txId:=gnodeNew.TxId

	for _,rwset:=range txRWSet.NsRwSets{
		logger.Infof("zs: gettx txid:%s namespace:%s ",txId,rwset.NameSpace)
		if strings.Compare(rwset.NameSpace,"lscc")!=0{
			for _,rkv:=range rwset.KvRwSet.Reads{
				rkValue:=string(rkv.Value)
				if strings.Compare(rkValue,"onlywrite")==0{

					logger.Infof("zs: gettx txid:%s onlywrite rtxid:%s rtxcount:%d namespace:%s rk:%s rv:%s rtxid:%s",txId,rkv.Txid,rkv.Version.TxNum,rwset.NameSpace,rkv.Key,string(rkv.Value),rkv.Txid)
				}else if strings.Compare(rkValue,"onlyread")==0{

					logger.Infof("zs: gettx txid:%s onlyread rtxid:%s rtxcount:%d namespace:%s rk:%s rv:%s rtxid:%s",txId,rkv.Txid,rkv.Version.TxNum,rwset.NameSpace,rkv.Key,string(rkv.Value),rkv.Txid)
				}else{
					logger.Infof("zs: gettx txid:%s normal namespace:%s rk:%s rv:%s rtxid:%s",txId,rwset.NameSpace,rkv.Key,string(rkv.Value),rkv.Txid)


					rkvOld,isExist:=gnodeOld.ReadSet[rkv.Key]
					if isExist{
						//if strings.Compare(string(rkv.Value),string(rkvOld.Value))!=0|| rkvOld.Type!=1{
						//	logger.Infof("zs: compare newtxid:%s k:%s v:%s ptxid:%s withExistTx rptxid:%s v:%s rkvBType:%d ",
						//		gnodeNew.TxId,rkv.Key,string(rkv.Value),rkv.Txid,rkvOld.ParentTxId,string(rkvOld.Value),rkvOld.Type)
						//	return false
						//}
						if strings.Compare(string(rkv.Value),string(rkvOld.Value))!=0 || strings.Compare(rkv.Txid,rkvOld.ParentTxId)!=0 || rkvOld.Type!=1{
							logger.Infof("zs: compare newtxid:%s k:%s v:%s ptxid:%s withExistTx rptxid:%s v:%s rkvBType:%d ",
								gnodeNew.TxId,rkv.Key,string(rkv.Value),rkv.Txid,rkvOld.ParentTxId,string(rkvOld.Value),rkvOld.Type)
							return false
						}
					}else{
						logger.Infof("zs: compare newtxid:%s k:%s v:%s  with NotExistTx in Oldtx",gnodeNew.TxId,rkv.Key,string(rkv.Value))
						return false
					}


				}
			}




		}
	}
	return true
}
func (ch *chain) GConAddNode(gnodeA *GNode) bool {

	txRWSet:=gnodeA.TxRWSet
	txId:=gnodeA.TxId
	for _,rwset:=range txRWSet.NsRwSets{
		logger.Infof("zs: gettx txid:%s namespace:%s ",txId,rwset.NameSpace)
		if strings.Compare(rwset.NameSpace,"lscc")!=0{
			for _,rkv:=range rwset.KvRwSet.Reads{
				rkValue:=string(rkv.Value)
				childNodeType:=int64(0)
				if strings.Compare(rkValue,"onlywrite")==0{
					childNodeType=2
					logger.Infof("zs: gettx txid:%s onlywrite rtxid:%s rtxcount:%d namespace:%s rk:%s rv:%s rtxid:%s",txId,rkv.Txid,rkv.Version.TxNum,rwset.NameSpace,rkv.Key,string(rkv.Value),rkv.Txid)
				}else if strings.Compare(rkValue,"onlyread")==0{
					childNodeType=3
					logger.Infof("zs: gettx txid:%s onlyread rtxid:%s rtxcount:%d namespace:%s rk:%s rv:%s rtxid:%s",txId,rkv.Txid,rkv.Version.TxNum,rwset.NameSpace,rkv.Key,string(rkv.Value),rkv.Txid)
				}else{
					childNodeType=1
					logger.Infof("zs: gettx txid:%s normal namespace:%s rk:%s rv:%s rtxid:%s",txId,rwset.NameSpace,rkv.Key,string(rkv.Value),rkv.Txid)
				}
				readKV:=&ReadKV{
					Key:        rkv.Key,
					Value:      rkv.Value,
					Type:       childNodeType,
					ParentTxId: rkv.Txid,
				}



				if childNodeType!=1{
					readKV.Key=rkv.Key[:len(rkv.Key)-len(rkv.Txid)-1]
				}


				gnodeA.ReadSet[rkv.Key]=readKV








			}

			for _,wkv:=range rwset.KvRwSet.Writes{
				_,isInR:=gnodeA.ReadSet[wkv.Key]
				if isInR==true{
					logger.Infof("zs: gettx txid:%s normal namespace:%s wk:%s wv:%s InR",txId,rwset.NameSpace,wkv.Key,string(wkv.Value))
					gnodeA.WriteSet[wkv.Key]=&WriteKV{
						Key:   wkv.Key,
						Value: wkv.Value,
						Type:  1,
					}
				}else{
					if len(ch.GCon.GTxWeight)>5{
						logger.Infof("zs: gettx txid:%s normal namespace:%s wk:%s wv:%s OutR",txId,rwset.NameSpace,wkv.Key,string(wkv.Value))
					}
					gnodeA.WriteSet[wkv.Key]=&WriteKV{
						Key:   wkv.Key,
						Value: wkv.Value,
						Type:  0,
					}
				}

			}


		}
	}



	gnodeResult,isExistgnode:=ch.GCon.GTxRelusts[txId]
	if isExistgnode==false{
		//zs:chuanxing
		ch.GCon.GTxRelusts[txId]=&GNodeResult{
			GnodeResult: gnodeA,
			Next:        nil,
			State: 2,
		}
		ch.GCon.GnodeMaxResults[txId]=&GNodeResult{
			GnodeResult: gnodeA,
			Next:        nil,
			State:       2,
		}
	}else{
		ch.GCon.GTxRelusts[txId].Next=&GNodeResult{
			GnodeResult: gnodeResult.GnodeResult,
			Next:        gnodeResult.Next,
			State:       gnodeResult.State,
		}
		ch.GCon.GTxRelusts[txId].GnodeResult=gnodeA

		if ch.GCon.GnodeMaxResults[txId].GnodeResult.State==3{
			logger.Infof("zs: h.GCon.GnodeMaxResultsChange txid:%s txcount:%d  with oldTx txcount:%d",gnodeA.TxId,gnodeA.TxCount,ch.GCon.GnodeMaxResults[txId].GnodeResult.TxCount)
			ch.GCon.GnodeMaxResults[txId].GnodeResult=gnodeA
		}
	}
	return true

}


func (ch *chain) DeleteGChooseNode(gnode *GNode)  {

	if gnode.IsCommitted==0{
		flagCommit:=true
		for _,rkv:=range gnode.ReadSet{
			ptxGChooseNode,isInGChooseNode:=ch.GCon.GChooseNode[rkv.ParentTxId]
			flagAddGChooseEdge:=false
			if isInGChooseNode==false{
				logger.Infof("zs: DeleteGChooseNode txid:%s rk:%s v:%s ptxid is not in GChooseNode ptxid:%s",gnode.TxId,rkv.Key,string(rkv.Value),rkv.ParentTxId)
				flagAddGChooseEdge=true
			}else{
				logger.Infof("zs: DeleteGChooseNode txid:%s rk:%s v:%s ptxid is already in GChooseNode ptxid:%s",gnode.TxId,rkv.Key,string(rkv.Value),rkv.ParentTxId)
				if ptxGChooseNode.IsCommitted==1{
					logger.Infof("zs: DeleteGChooseNode txid:%s rk:%s v:%s ptxid is already in GChooseNode already committed ptxid:%s",gnode.TxId,rkv.Key,string(rkv.Value),rkv.ParentTxId)
				}else{
					logger.Infof("zs: DeleteGChooseNode txid:%s rk:%s v:%s ptxid is already in GChooseNode but not committed ptxid:%s",gnode.TxId,rkv.Key,string(rkv.Value),rkv.ParentTxId)
					flagAddGChooseEdge=true
				}
			}
			if flagAddGChooseEdge==true{
				flagCommit=false
				gnode.NumPGChooseNode++
				childnode, isExist := ch.GCon.GChooseEdgeSet[rkv.ParentTxId]
				if isExist ==true{
					ch.GCon.GChooseEdgeSet[rkv.ParentTxId].Next=&GChooseEdge{
						ChildNode:   childnode.ChildNode,
						Next:        childnode.Next,
						State:       2,
						IsCommitted: 0,
					}

					ch.GCon.GChooseEdgeSet[rkv.ParentTxId].ChildNode=gnode
				} else {
					ch.GCon.GChooseEdgeSet[rkv.ParentTxId]=&GChooseEdge{
						ChildNode:   gnode,
						Next:        nil,
						State:       2,
						IsCommitted: 0,
					}
				}
			}

		}
		if flagCommit==true{

			logger.Infof("zs: committed inblock success txid:%s ", gnode.TxId)
			batches, _ := ch.support.BlockCutter().Ordered(gnode.TxResult)
			ch.GCon.NumSendInBlock++
			for _, batch := range batches {
				block := ch.support.CreateNextBlock(batch)
				ch.support.WriteBlock(block, nil)
			}
			gnode.IsCommitted=1
			ch.GCon.NumGChooseEdge++
			ch.DeleteGChooseEdge(gnode)
		}
	}

}

func (ch *chain) DeleteGChooseEdge(gnode *GNode)  {
	childTx,isExist:=ch.GCon.GChooseEdgeSet[gnode.TxId]
	logger.Infof("zs: DeleteGChooseEdge Ptxid:%s BE",gnode.TxId)
	if isExist==true && ch.GCon.GChooseEdgeSet[gnode.TxId].IsCommitted!=1{
		ch.GCon.GChooseEdgeSet[gnode.TxId].IsCommitted=1
		for childTx!=nil{
			childTx.ChildNode.NumPGChooseNode--
			logger.Infof("zs: DeleteGChooseEdge Ptxid:%s childTx:%s childPtxNum:%d",gnode.TxId,childTx.ChildNode.TxId,childTx.ChildNode.NumPGChooseNode)
			if childTx.ChildNode.NumPGChooseNode==0 && childTx.ChildNode.IsCommitted!=1{
				logger.Infof("zs: DeleteGChooseEdge Ptxid:%s DeleteGChoosenode BE childTx:%s childPtxNum:%d",gnode.TxId,childTx.ChildNode.TxId,childTx.ChildNode.NumPGChooseNode)
				ch.DeleteGChooseNode(childTx.ChildNode)
				logger.Infof("zs: DeleteGChooseEdge Ptxid:%s DeleteGChoosenode EN childTx:%s childPtxNum:%d",gnode.TxId,childTx.ChildNode.TxId,childTx.ChildNode.NumPGChooseNode)
			}
			childTx=childTx.Next
		}
	}

	logger.Infof("zs: DeleteGChooseEdge Ptxid:%s EN",gnode.TxId)
}

func (ch *chain) main() {
	var timer <-chan time.Time

	var err error

	for {
		seq := ch.support.Sequence()
		err = nil
		select {
		case msg := <-ch.sendChan:
			if msg.configMsg == nil {


				// env:=msg.normalMsg
				// txCount,txId,_,respPayload,_:=utils.GetTxnResultFromEnvelopeMsg(env)

				// logger.Infof("zs:### txid:%s txcount:%d all tx:%d txwithallvotes:%d NumsendInblcok:%d 2=Votes:%d 3=Votes:%d 4=Votes:%d GChooseNode:%d GChooseEdge:%d",
				// 	txId,txCount, len(ch.GCon.GNodeInBlock), ch.GCon.NumTxwithAllvotes,ch.GCon.NumSendInBlock,ch.GCon.NumVotes2,ch.GCon.NumVotes3,ch.GCon.NumVotes4,
				// 	ch.GCon.NumGChooseNode,ch.GCon.NumGChooseEdge)

				// txState,isExistBefore:=ch.GCon.GNodeInBlock[txId]

				// if isExistBefore==false{

				// 	ch.GCon.GTxWeight[txId]=1
				// 	ch.GCon.GNodeInBlock[txId]=&CommittedGnode{
				// 		State:          2,
				// 		committedGnode: nil,
				// 		IsCommitted: 0,
				// 	}
				// 	logger.Infof("zs: gettx times:%d in block txid:%s txcount:%d firsttimes",ch.GCon.GTxWeight[txId],txId,txCount)


				// 	txRWSet := &rwsetutil.TxRwSet{}
				// 	if err = txRWSet.FromProtoBytes(respPayload.Results); err != nil {
				// 		logger.Infof("zs: gettx txid:%s txcount:%d txRWSet.FromProtoBytes failed",txId,txCount)
				// 	}
				// 	gnode:=&GNode{
				// 		TxResult: 	env,
				// 		TxRWSet: txRWSet,
				// 		TxId:      txId,
				// 		TxCount:   txCount,
				// 		PeerId:    "",
				// 		Votes:  1,
				// 		ParentTxs: map[string]*ReadKV{},
				// 		ReadSet: map[string]*ReadKV{},
				// 		WriteSet: map[string]*WriteKV{},
				// 		State: 2,
				// 		NumParentsOfRW: 0,
				// 		NumParentsOfOW: 0,
				// 		NumParentsOfOR: 0,
				// 		IsCommitted: 0,
				// 		NumPGChooseNode: 0,

				// 	}
				// 	flagAddNode:=ch.GConAddNode(gnode)
				// 	if flagAddNode==true{
				// 		logger.Infof("zs: gettx txid:%s txcount:%d addnode success",txId,txCount)
				// 	}else{
				// 		logger.Infof("zs: gettx txid:%s txcount:%d addnode false",txId,txCount)
				// 	}


				// 	if len(ch.GCon.GTxWeight)<=1{
				// 		gnode.State=1
				// 		ch.GCon.GNodeInBlock[txId].State=1
				// 		ch.GCon.GNodeInBlock[txId].IsCommitted=1
				// 		ch.GCon.GNodeInBlock[txId].committedGnode=gnode
				// 		ch.GCon.GnodeMaxResults[txId].GnodeResult=gnode
				// 		ch.GCon.GnodeMaxResults[txId].State=1
				// 		ch.GCon.GTxRelusts[txId].State=1
				// 		logger.Infof("zs: committed inblock txid:%s txcount:%d",txId,txCount)
				// 		batches, pending := ch.support.BlockCutter().Ordered(gnode.TxResult)
				// 		ch.GCon.NumSendInBlock++
				// 		for _, batch := range batches {
				// 			block := ch.support.CreateNextBlock(batch)
				// 			ch.support.WriteBlock(block, nil)
				// 		}

				// 		switch {
				// 		case timer != nil && !pending:
				// 			// Timer is already running but there are no messages pending, stop the timer
				// 			timer = nil
				// 		case timer == nil && pending:
				// 			// Timer is not already running and there are messages pending, so start it
				// 			timer = time.After(ch.support.SharedConfig().BatchTimeout())
				// 			logger.Debugf("Just began %s batch timer", ch.support.SharedConfig().BatchTimeout().String())
				// 		default:
				// 			// Do nothing when:
				// 			// 1. Timer is already running and there are messages pending
				// 			// 2. Timer is not set and there are no messages pending
				// 		}
				// 	}

				// }else if txState.IsCommitted==0{

				// 	txTimes:=ch.GCon.GTxWeight[txId]
				// 	txTimes++
				// 	ch.GCon.GTxWeight[txId]=txTimes
				// 	logger.Infof("zs: gettx times:%d in block txid:%s txcount:%d ",ch.GCon.GTxWeight[txId],txId,txCount)

				// 	if txTimes==ch.GCon.NumExeNodes{
				// 		ch.GCon.NumTxwithAllvotes++
				// 	}


				// 	txRWSet := &rwsetutil.TxRwSet{}
				// 	if err = txRWSet.FromProtoBytes(respPayload.Results); err != nil {
				// 		logger.Infof("zs: gettx txid:%s txcount:%d txRWSet.FromProtoBytes failed",txId,txCount)
				// 	}

				// 	gnode:=&GNode{
				// 		TxResult: 	env,
				// 		TxRWSet: txRWSet,
				// 		TxId:      txId,
				// 		TxCount:   txCount,
				// 		PeerId:    "",
				// 		Votes:  1,
				// 		ParentTxs: map[string]*ReadKV{},
				// 		ReadSet: map[string]*ReadKV{},
				// 		WriteSet: map[string]*WriteKV{},
				// 		State: 2,
				// 		NumParentsOfRW: 0,
				// 		NumParentsOfOW: 0,
				// 		NumParentsOfOR: 0,
				// 		IsCommitted: 0,
				// 		NumPGChooseNode: 0,
				// 	}


				// 	var ccInvoke=5
				// //	ccInvoke=ccInvoke+4 //createTx=4,if no createTx, zhushi
				// 	if false&&len(ch.GCon.GTxWeight)<=ccInvoke{
				// 		if txTimes==5{
				// 			gnode.State=1
				// 			ch.GCon.GNodeInBlock[txId].State=1
				// 			ch.GCon.GNodeInBlock[txId].IsCommitted=1
				// 			//ch.GCon.GNodeInBlock[txId].committedGnode=gnode
				// 			//ch.GCon.GnodeMaxResults[txId].GnodeResult=gnode
				// 			ch.GCon.GNodeInBlock[txId].committedGnode=ch.GCon.GnodeMaxResults[txId].GnodeResult
				// 			ch.GCon.GnodeMaxResults[txId].State=1
				// 			ch.GCon.GTxRelusts[txId].State=1




				// 			ch.GCon.GChooseNode[gnode.TxId]=ch.GCon.GnodeMaxResults[txId].GnodeResult
				// 			ch.GCon.NumGChooseNode++
				// 			logger.Infof("zs: GChooseNode txid:%s",gnode.TxId)
				// 			ch.DeleteGChooseNode(ch.GCon.GnodeMaxResults[txId].GnodeResult)


				// 		}


				// 	}else{
				// 		txResult:=ch.GCon.GTxRelusts[gnode.TxId]
				// 		flagIsSame:=false
				// 		for txResult!=nil{
				// 			if ch.GnodeCompare(gnode,txResult.GnodeResult)==true{
				// 				flagIsSame=true
				// 				break
				// 			}
				// 			txResult=txResult.Next
				// 		}
				// 		if flagIsSame==true {
				// 			txResult.GnodeResult.Votes++

				// 			if txResult.GnodeResult.Votes == 2 {
				// 				ch.GCon.NumVotes2++
				// 			} else if txResult.GnodeResult.Votes == 3 {
				// 				ch.GCon.NumVotes3++
				// 			} else if txResult.GnodeResult.Votes == 4 {
				// 				ch.GCon.NumVotes4++
				// 			}

				// 			if txResult.GnodeResult.Votes >= ch.GCon.NumKWeight {
				// 				ch.GCon.GChooseNode[txResult.GnodeResult.TxId]=txResult.GnodeResult

				// 				ch.GCon.NumGChooseNode++





				// 				txResult.GnodeResult.State=1
				// 				ch.GCon.GnodeMaxResults[txId].GnodeResult=txResult.GnodeResult
				// 				ch.GCon.GNodeInBlock[txId].State = 1
				// 				ch.GCon.GNodeInBlock[txId].IsCommitted = 1
				// 				ch.GCon.GNodeInBlock[txId].committedGnode = txResult.GnodeResult

				// 				ch.GCon.GnodeMaxResults[txId].State = 1
				// 				ch.GCon.GTxRelusts[txId].State = 1

				// 				logger.Infof("zs: GChooseNode txid:%s",txResult.GnodeResult.TxId)

				// 				ch.DeleteGChooseNode(txResult.GnodeResult)


				// 				//logger.Infof("zs: committed inblock success txid:%s txcount:%d", txResult.GnodeResult.TxId, txResult.GnodeResult.TxCount)
				// 				//batches, pending := ch.support.BlockCutter().Ordered(txResult.GnodeResult.TxResult)
				// 				//ch.GCon.NumSendInBlock++
				// 				//for _, batch := range batches {
				// 				//	block := ch.support.CreateNextBlock(batch)
				// 				//	ch.support.WriteBlock(block, nil)
				// 				//}
				// 				//switch {
				// 				//case timer != nil && !pending:
				// 				//	// Timer is already running but there are no messages pending, stop the timer
				// 				//	timer = nil
				// 				//case timer == nil && pending:
				// 				//	// Timer is not already running and there are messages pending, so start it
				// 				//	timer = time.After(ch.support.SharedConfig().BatchTimeout())
				// 				//	logger.Debugf("Just began %s batch timer", ch.support.SharedConfig().BatchTimeout().String())
				// 				//default:
				// 				//	// Do nothing when:
				// 				//	// 1. Timer is already running and there are messages pending
				// 				//	// 2. Timer is not set and there are no messages pending
				// 				//}

				// 			}
				// 		}else{
				// 			flagAddNode:=ch.GConAddNode(gnode)
				// 			if flagAddNode==true{
				// 				logger.Infof("zs: gettx txid:%s txcount:%d addnode success in state==2",txId,txCount)
				// 			}else{
				// 				logger.Infof("zs: gettx txid:%s txcount:%d addnode false in state==2",txId,txCount)
				// 			}

				// 		}
				// 	}

				// 	switch {
				// 	case timer != nil && ch.GCon.LastNumGChooseEdge==ch.GCon.NumGChooseEdge:
				// 		// Timer is already running but there are no messages pending, stop the timer
				// 		timer = nil
				// 	case timer == nil && ch.GCon.LastNumGChooseEdge!=ch.GCon.NumGChooseEdge:
				// 		// Timer is not already running and there are messages pending, so start it
				// 		timer = time.After(ch.support.SharedConfig().BatchTimeout()*2)
				// 		logger.Debugf("Just began %s batch timer", ch.support.SharedConfig().BatchTimeout().String())
				// 	default:
				// 		// Do nothing when:
				// 		// 1. Timer is already running and there are messages pending
				// 		// 2. Timer is not set and there are no messages pending
				// 	}
				// }else{

				// 	txTimes:=ch.GCon.GTxWeight[txId]
				// 	txTimes++
				// 	ch.GCon.GTxWeight[txId]=txTimes
				// 	logger.Infof("zs: gettx times:%d in block txid:%s txcount:%d is already committed",ch.GCon.GTxWeight[txId],txId,txCount)
				// 	if txTimes==ch.GCon.NumExeNodes{
				// 		ch.GCon.NumTxwithAllvotes++
				// 	}


				// 	txRWSet := &rwsetutil.TxRwSet{}
				// 	if err = txRWSet.FromProtoBytes(respPayload.Results); err != nil {
				// 		logger.Infof("zs: gettx txid:%s txcount:%d txRWSet.FromProtoBytes failed",txId,txCount)
				// 	}

				// 	gnode:=&GNode{
				// 		TxResult: 	env,
				// 		TxRWSet: txRWSet,
				// 		TxId:      txId,
				// 		TxCount:   txCount,
				// 		PeerId:    "",
				// 		Votes:  1,
				// 		ParentTxs: map[string]*ReadKV{},
				// 		ReadSet: map[string]*ReadKV{},
				// 		WriteSet: map[string]*WriteKV{},
				// 		State: 1,
				// 		NumParentsOfRW: 0,
				// 		NumParentsOfOW: 0,
				// 		NumParentsOfOR: 0,
				// 		IsCommitted: 0,
				// 		NumPGChooseNode: 0,
				// 	}
				// 	txResult:=ch.GCon.GTxRelusts[gnode.TxId]
				// 	flagIsSame:=false
				// 	for txResult!=nil{
				// 		if ch.GnodeCompare(gnode,txResult.GnodeResult)==true{
				// 			flagIsSame=true
				// 			break
				// 		}
				// 		txResult=txResult.Next
				// 	}
				// 	if flagIsSame==true {
				// 		txResult.GnodeResult.Votes++

				// 		if txResult.GnodeResult.Votes == 2 {
				// 			ch.GCon.NumVotes2++
				// 		} else if txResult.GnodeResult.Votes == 3 {
				// 			ch.GCon.NumVotes3++
				// 		} else if txResult.GnodeResult.Votes == 4 {
				// 			ch.GCon.NumVotes4++
				// 		}
				// 	}



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
				// 				}
				// 			}

				// 			for _,wkv:=range rwset.KvRwSet.Writes{

				// 				logger.Infof("zs: gettx txid:%s normal namespace:%s wk:%s wv:%s InR",txId,rwset.NameSpace,wkv.Key,string(wkv.Value))


				// 			}


				// 		}
				// 	}
				// 	switch {
				// 	case timer != nil && ch.GCon.LastNumGChooseEdge==ch.GCon.NumGChooseEdge:
				// 		// Timer is already running but there are no messages pending, stop the timer
				// 		timer = nil
				// 	case timer == nil && ch.GCon.LastNumGChooseEdge!=ch.GCon.NumGChooseEdge:
				// 		// Timer is not already running and there are messages pending, so start it
				// 		timer = time.After(ch.support.SharedConfig().BatchTimeout()*2)
				// 		logger.Debugf("Just began %s batch timer", ch.support.SharedConfig().BatchTimeout().String())
				// 	default:
				// 		// Do nothing when:
				// 		// 1. Timer is already running and there are messages pending
				// 		// 2. Timer is not set and there are no messages pending
				// 	}
				// }







			} else {
				logger.Infof("zs: local SOLO.main()  Ordered ConfigMsg")
				// ConfigMsg
				if msg.configSeq < seq {
					msg.configMsg, _, err = ch.support.ProcessConfigMsg(msg.configMsg)
					if err != nil {
						logger.Warningf("Discarding bad config message: %s", err)
						continue
					}
				}
				batch := ch.support.BlockCutter().Cut()
				if batch != nil {
					block := ch.support.CreateNextBlock(batch)
					ch.support.WriteBlock(block, nil)
				}

				block := ch.support.CreateNextBlock([]*cb.Envelope{msg.configMsg})
				ch.support.WriteConfigBlock(block, nil)
				timer = nil
			}
		case <-timer:
			//clear the timer
			timer = nil
			ch.GCon.LastNumGChooseEdge=ch.GCon.NumGChooseEdge

			batch := ch.support.BlockCutter().Cut()
			if len(batch) == 0 {
				logger.Warningf("Batch timer expired with no pending requests, this might indicate a bug")
				continue
			}
			logger.Debugf("Batch timer expired, creating block")
			block := ch.support.CreateNextBlock(batch)
			ch.support.WriteBlock(block, nil)
		case <-ch.exitChan:
			logger.Debugf("Exiting")
			return
		}
	}
}




//
//package local
//
//import (
//	"fmt"
//	"github.com/hyperledger/fabric/core/ledger/kvledger/txmgmt/rwsetutil"
//	"github.com/hyperledger/fabric/protos/utils"
//	"strings"
//	"time"
//
//	"github.com/hyperledger/fabric/common/flogging"
//	"github.com/hyperledger/fabric/orderer/consensus"
//	cb "github.com/hyperledger/fabric/protos/common"
//)
//
//var logger = flogging.MustGetLogger("orderer.consensus.local")
//
//type consenter struct{}
//
//
//
//
//
////type chain struct {
////	support  consensus.ConsenterSupport
////	sendChan chan *message
////	exitChan chan struct{}
////}
////zs
//type ReadKV struct {
//	Key string
//	Value []byte
//	Type int64     //1: read-write, must wait or false; 2: only-write, must wait or false; 3: only-read, no wait or ture;
//	ParentTxId string
//}
//type WriteKV struct {
//	Key string
//	Value []byte
//	Type int64     //1: inReadSet; 0: OutofReadset
//}
//type GNode struct {
//	TxResult *cb.Envelope
//	TxRWSet *rwsetutil.TxRwSet
//	TxId string
//	TxCount int64
//	PeerId string
//	Votes int64
//	ParentTxs map[string] *ReadKV
//	ReadSet map[string] *ReadKV
//	WriteSet map[string]*WriteKV
//	State int64 //1.committed; 2.wait; 3:false
//	NumParentsOfRW int64  //must wait or false
//	NumParentsOfOW int64  //must wait or false
//	NumParentsOfOR int64  //no wait or true
//
//}
//type GEdge struct {
//	ChildNode  *GNode
//	ChildNodeType int64    //1: read-write, must wait or false; 2: only-write, must wait or false; 3: only-read, no wait or ture;
//	Next *GEdge
//	State int64 //1:success; 2:wait; 3:false
//}
//type GNodeResult struct {
//	GnodeResult *GNode
//	Next *GNodeResult
//	State int64 //1:success; 2:wait; 3:false
//}
//type CommittedGnode struct {
//	State int64   //1: success commit; 2: wait enough weight or parents; 3: false consensus
//	committedGnode *GNode
//	IsCommitted int64//1.has committed;0 not committed
//}
//
//
//type GConsensus struct {
//	GKVTable map[string] string
//	GEdgeSet map[string] *GEdge
//	GNodeInBlock map[string] *CommittedGnode
//	GTxWeight map[string]int64 //all notes
//	GTxRelusts map[string]*GNodeResult //votes of each results
//	GnodeMaxResults map[string] *GNodeResult
//	NumExeNodes int64
//	NumKWeight int64
//	NumTxwithAllvotes int64
//	NumSendInBlock int64
//
//	NumVotes2 int64
//	NumVotes3 int64
//	NumVotes4 int64
//
//}
//
//type chain struct {
//	support  consensus.ConsenterSupport
//	sendChan chan *message
//	exitChan chan struct{}
//	GCon GConsensus
//}
////zs
//
//type message struct {
//	configSeq uint64
//	normalMsg *cb.Envelope
//	configMsg *cb.Envelope
//}
//
//// New creates a new consenter for the solo consensus scheme.
//// The solo consensus scheme is very simple, and allows only one consenter for a given chain (this process).
//// It accepts messages being delivered via Order/Configure, orders them, and then uses the blockcutter to form the messages
//// into blocks before writing to the given ledger
//func New() consensus.Consenter {
//	return &consenter{}
//}
//
//func (solo *consenter) HandleChain(support consensus.ConsenterSupport, metadata *cb.Metadata) (consensus.Chain, error) {
//	return newChain(support), nil
//}
//
////func newChain(support consensus.ConsenterSupport) *chain {
////	return &chain{
////		support:  support,
////		sendChan: make(chan *message,1000),
////		exitChan: make(chan struct{}),
////	}
////}
//func newChain(support consensus.ConsenterSupport) *chain {
//	return &chain{
//		support:  support,
//		sendChan: make(chan *message,80000),
//		exitChan: make(chan struct{}),
//		GCon: GConsensus{
//			GKVTable: map[string]string{},
//			//GNodeSet: map[string]*GNode{},
//			GEdgeSet: map[string]*GEdge{},
//			GNodeInBlock: map[string]*CommittedGnode{},
//			GTxWeight: map[string]int64{},
//			GTxRelusts: map[string]*GNodeResult{},
//			GnodeMaxResults: map[string]*GNodeResult{},
//			NumExeNodes: 4,
//			NumKWeight: 2,
//			NumTxwithAllvotes: 0,
//			NumSendInBlock: 0,
//
//			NumVotes2: 0,
//			NumVotes3: 0,
//			NumVotes4: 0,
//		},
//	}
//}
//
//func (ch *chain) Start() {
//	go ch.main()
//}
//
//func (ch *chain) Halt() {
//	select {
//	case <-ch.exitChan:
//		// Allow multiple halts without panic
//	default:
//		close(ch.exitChan)
//	}
//}
//
//func (ch *chain) WaitReady() error {
//	return nil
//}
//
//// Order accepts normal messages for ordering
//func (ch *chain) Order(env *cb.Envelope, configSeq uint64) error {
//	logger.Infof("zs: LocalOrder SOLO.main() before Ordered msg")
//	lenchcan:= len(ch.sendChan)
//	if lenchcan>75000{
//		logger.Infof("zs: case ch.sendChan sssschanlen:%d !!!@@@!!!",lenchcan)
//	}else{
//		logger.Infof("zs: case ch.sendChan chanlen:%d ",lenchcan)
//	}
//
//	select {
//	case ch.sendChan <- &message{
//		configSeq: 0,
//		normalMsg: env,
//	}:
//		logger.Infof("zs: LocalOrder SOLO.main() after Ordered msg")
//		return nil
//	case <-ch.exitChan:
//		logger.Infof("zs: LocalOrder SOLO.main() after Ordered msg exit")
//		return fmt.Errorf("Exiting")
//	}
//}
//// Configure accepts configuration update messages for ordering
//func (ch *chain) Configure(config *cb.Envelope, configSeq uint64) error {
//	select {
//	case ch.sendChan <- &message{
//		configSeq: configSeq,
//		configMsg: config,
//	}:
//		return nil
//	case <-ch.exitChan:
//		return fmt.Errorf("Exiting")
//	}
//}
//
//// Errored only closes on exit
//func (ch *chain) Errored() <-chan struct{} {
//	return ch.exitChan
//}
//
//
//
//func (ch *chain) GnodeCompare(gnodeNew *GNode, gnodeOld *GNode) bool {
//
//txRWSet:=gnodeNew.TxRWSet
//txId:=gnodeNew.TxId
//
//	for _,rwset:=range txRWSet.NsRwSets{
//		logger.Infof("zs: gettx txid:%s namespace:%s ",txId,rwset.NameSpace)
//		if strings.Compare(rwset.NameSpace,"lscc")!=0{
//			for _,rkv:=range rwset.KvRwSet.Reads{
//				rkValue:=string(rkv.Value)
//				if strings.Compare(rkValue,"onlywrite")==0{
//
//					logger.Infof("zs: gettx txid:%s onlywrite rtxid:%s rtxcount:%d namespace:%s rk:%s rv:%s rtxid:%s",txId,rkv.Txid,rkv.Version.TxNum,rwset.NameSpace,rkv.Key,string(rkv.Value),rkv.Txid)
//				}else if strings.Compare(rkValue,"onlyread")==0{
//
//					logger.Infof("zs: gettx txid:%s onlyread rtxid:%s rtxcount:%d namespace:%s rk:%s rv:%s rtxid:%s",txId,rkv.Txid,rkv.Version.TxNum,rwset.NameSpace,rkv.Key,string(rkv.Value),rkv.Txid)
//				}else{
//					logger.Infof("zs: gettx txid:%s normal namespace:%s rk:%s rv:%s rtxid:%s",txId,rwset.NameSpace,rkv.Key,string(rkv.Value),rkv.Txid)
//
//
//					rkvOld,isExist:=gnodeOld.ReadSet[rkv.Key]
//					if isExist{
//						if strings.Compare(string(rkv.Value),string(rkvOld.Value))!=0 || rkvOld.Type!=1{
//							logger.Infof("zs: compare newtxid:%s k:%s v:%s  withExistTx v:%s rkvBType:%d ",gnodeNew.TxId,rkv.Key,string(rkv.Value),string(rkvOld.Value),rkvOld.Type)
//							return false
//						}
//					}else{
//						logger.Infof("zs: compare newtxid:%s k:%s v:%s  with NotExistTx in Oldtx",gnodeNew.TxId,rkv.Key,string(rkv.Value))
//						return false
//					}
//
//
//				}
//			}
//
//
//
//
//		}
//	}
//	return true
//}
//
//
//
//func (ch *chain) GConAddEdge(gnodeA *GNode, rkv *ReadKV) int64 { //0:addedge false; 1: addedge true but ptx is committed; 2: addedge true and ptx is wait
//
//	ParentTx,isParentTxInBlock:=ch.GCon.GNodeInBlock[rkv.ParentTxId]
//	//if isParentTxInBlock==true{
//	//	logger.Infof("zs: txid:%s AddEdge  rk:%s rv:%s rPtxid:%s type:%d state:%d",
//	//		gnodeA.TxId,rkv.Key,string(rkv.Value),rkv.ParentTxId,rkv.Type,ParentTx.State)
//	//}else{
//	//	logger.Infof("zs: txid:%s AddEdge  rk:%s rv:%s rPtxid:%s type:%d not exist",
//	//		gnodeA.TxId,rkv.Key,string(rkv.Value),rkv.ParentTxId,rkv.Type)
//	//}
//
//	if isParentTxInBlock==true{
//
//		if ParentTx.State==1{//ptx is committed
//			if rkv.Type==1{
//				wkv,isExist:=ParentTx.committedGnode.WriteSet[rkv.Key]
//				if isExist==true{
//						if strings.Compare(string(rkv.Value),string(wkv.Value))!=0{
//							logger.Infof("zs: txid:%s AddEdge false rk:%s rv:%s rPtxid:%s wv:%s type:%d state:%d is InBlock committed with diff wkv",
//								gnodeA.TxId,rkv.Key,string(rkv.Value),rkv.ParentTxId,string(wkv.Value),rkv.Type,ParentTx.State)
//							return 0
//							//return false
//						}else{
//
//
//
//
//								kvalue:=ch.GCon.GKVTable[rkv.Key]
//								newvalue:=kvalue[:len(kvalue)-len(rkv.ParentTxId)]
//
//								if strings.Compare(string(rkv.Value),newvalue)!=0{
//									logger.Infof("zs: txid:%s AddEdge true rk:%s rv:%s rPtxid:%s wv:%s type:%d state:%d is InBlock committed with same wkv but newvalue:%s changed",
//										gnodeA.TxId,rkv.Key,string(rkv.Value),rkv.ParentTxId,string(wkv.Value),rkv.Type,ParentTx.State,newvalue)
//									return 0
//								}else{
//									logger.Infof("zs: txid:%s AddEdge true rk:%s rv:%s rPtxid:%s wv:%s type:%d state:%d is InBlock committed with same wkv",
//										gnodeA.TxId,rkv.Key,string(rkv.Value),rkv.ParentTxId,string(wkv.Value),rkv.Type,ParentTx.State)
//									return 1
//								}
//							//return true
//						}
//				}else{
//					logger.Infof("zs: txid:%s AddEdge false rk:%s rv:%s rPtxid:%s state:%d is InBlock committed but without this key",
//						gnodeA.TxId,rkv.Key,string(rkv.Value),rkv.ParentTxId,ParentTx.State)
//					return 0
//					//return false
//				}
//
//			}else {
//				logger.Infof("zs: txid:%s AddEdge true rk:%s rv:%s rPtxid:%s  type:%d state:%d is InBlock committed",
//					gnodeA.TxId,rkv.Key,string(rkv.Value),rkv.ParentTxId,rkv.Type,ParentTx.State)
//				return 1
//				//return true
//			}
//		}else if ParentTx.State==3{//ptx is false
//			if rkv.Type==1{
//				logger.Infof("zs: txid:%s AddEdge false rk:%s rv:%s rPtxid:%s type:%d state:%d is InBlock false",
//					gnodeA.TxId,rkv.Key,string(rkv.Value),rkv.ParentTxId,rkv.Type,ParentTx.State)
//				return 0
//				//return false
//			}else {
//				logger.Infof("zs: txid:%s AddEdge true rk:%s rv:%s rPtxid:%s type:%d state:%d is InBlock false",
//					gnodeA.TxId,rkv.Key,string(rkv.Value),rkv.ParentTxId,rkv.Type,ParentTx.State)
//				return 1
//				//return true
//			}
//
//		} else{// else ParentTx.State==2 is the same as isParentTxInBlock==false
//			logger.Infof("zs: txid:%s AddEdge state==2 rk:%s rv:%s rPtxid:%s type:%d state:%d is InBlock wait",
//				gnodeA.TxId,rkv.Key,string(rkv.Value),rkv.ParentTxId,rkv.Type,ParentTx.State)
//		}
//	}
//
//
//	logger.Infof("zs: txid:%s AddEdge state==2 or no exist rk:%s rv:%s rPtxid:%s type:%d is InBlock wait",
//		gnodeA.TxId,rkv.Key,string(rkv.Value),rkv.ParentTxId,rkv.Type)
//
//
//	_,isExistParentTxs:=gnodeA.ParentTxs[rkv.ParentTxId]
//	if isExistParentTxs==false {
//		if rkv.Type==1{
//			gnodeA.NumParentsOfRW++
//		}else if rkv.Type==2{
//			gnodeA.NumParentsOfOW++
//		}else{
//			gnodeA.NumParentsOfOR++
//		}
//
//
//		gnodeA.ParentTxs[rkv.ParentTxId] = rkv
//		rkvPTxid := rkv.ParentTxId +"#"+rkv.Key+"#"+string(rkv.Value)
//		childnode, isExist := ch.GCon.GEdgeSet[rkvPTxid]
//		if isExist {
//			ch.GCon.GEdgeSet[rkvPTxid].Next=&GEdge{
//				ChildNode:     childnode.ChildNode,
//				ChildNodeType: childnode.ChildNodeType,
//				Next:          childnode.Next,
//				State:         childnode.State,
//			}
//			ch.GCon.GEdgeSet[rkvPTxid].ChildNode=gnodeA
//			ch.GCon.GEdgeSet[rkvPTxid].ChildNodeType=rkv.Type
//		} else {
//			//zs:chuanxing
//			ch.GCon.GEdgeSet[rkvPTxid] = &GEdge{
//				ChildNode:     gnodeA,
//				Next:          nil,
//				ChildNodeType: rkv.Type,
//				State: 2,
//			}
//
//		}
//		logger.Infof("zs: txid:%s AddEdge true rk:%s rv:%s rPtxid:%s type:%d edge:%s is InBlock wait",
//			gnodeA.TxId,rkv.Key,string(rkv.Value),rkv.ParentTxId,rkv.Type,rkvPTxid)
//	}
//	return 2
//	//return true
//
//}
//func (ch *chain) GConAddNode(gnodeA *GNode) bool {
//
//	flagAddNode:=false
//	flagAddEdge:=true
//	txRWSet:=gnodeA.TxRWSet
//	txId:=gnodeA.TxId
//	txCount:=gnodeA.TxCount
//	for _,rwset:=range txRWSet.NsRwSets{
//		logger.Infof("zs: gettx txid:%s namespace:%s ",txId,rwset.NameSpace)
//		if strings.Compare(rwset.NameSpace,"lscc")!=0{
//			for _,rkv:=range rwset.KvRwSet.Reads{
//				rkValue:=string(rkv.Value)
//				childNodeType:=int64(0)
//				if strings.Compare(rkValue,"onlywrite")==0{
//					childNodeType=2
//					logger.Infof("zs: gettx txid:%s onlywrite rtxid:%s rtxcount:%d namespace:%s rk:%s rv:%s rtxid:%s",txId,rkv.Txid,rkv.Version.TxNum,rwset.NameSpace,rkv.Key,string(rkv.Value),rkv.Txid)
//				}else if strings.Compare(rkValue,"onlyread")==0{
//					childNodeType=3
//					logger.Infof("zs: gettx txid:%s onlyread rtxid:%s rtxcount:%d namespace:%s rk:%s rv:%s rtxid:%s",txId,rkv.Txid,rkv.Version.TxNum,rwset.NameSpace,rkv.Key,string(rkv.Value),rkv.Txid)
//				}else{
//					childNodeType=1
//					logger.Infof("zs: gettx txid:%s normal namespace:%s rk:%s rv:%s rtxid:%s",txId,rwset.NameSpace,rkv.Key,string(rkv.Value),rkv.Txid)
//				}
//				readKV:=&ReadKV{
//					Key:        rkv.Key,
//					Value:      rkv.Value,
//					Type:       childNodeType,
//					ParentTxId: rkv.Txid,
//				}
//
//				//zs:In localstate.go
//				////lts.FinalReadSet=append(lts.FinalReadSet,&kvrwset.KVRead{Key: newKV.Key+"#"+oldKV.FromTxnid,Version: &kvrwset.Version{
//				////	BlockNum: 0,
//				////	TxNum: uint64(oldKV.FromTxncount),
//				////},Value: []byte("onlywrite"),IsRead: false,Txid: oldKV.FromTxnid})
//				////lts.FinalReadSet=append(lts.FinalReadSet,&kvrwset.KVRead{Key: newKV.Key+"#"+txnInCh.Txid,Version: &kvrwset.Version{
//				////	BlockNum: 0,
//				////	TxNum: uint64(txnInCh.TxidCount),
//				////},Value: []byte("onlyread"),IsRead: false,Txid: txnInCh.Txid})
//				//zs:In localstate.go
//
//				if childNodeType!=1{
//					readKV.Key=rkv.Key[:len(rkv.Key)-len(rkv.Txid)-1]
//				}
//
//
//				gnodeA.ReadSet[rkv.Key]=readKV
//
//
//				if gnodeA.State!=3{
//					flagAddEdgeState:= ch.GConAddEdge(gnodeA,readKV)//0:addedge false; 1: addedge true but ptx is committed; 2: addedge true and ptx is wait
//					if flagAddEdgeState==0{
//						flagAddEdge=false
//						gnodeA.State=3
//						//ch.GCon.GNodeInBlock[gnodeA.TxId].State=3
//						//logger.Infof("zs: gettx txid:%s deleteForFalse before",txId)
//						//ch.DeleteGnodeForFalse(gnodeA)
//						//logger.Infof("zs: gettx txid:%s deleteForFalse end",txId)
//						//break
//					}else if flagAddEdgeState==2{
//						flagAddNode=true
//					}
//				}
//
//
//
//
//
//			}
//
//			for _,wkv:=range rwset.KvRwSet.Writes{
//				_,isInR:=gnodeA.ReadSet[wkv.Key]
//				if isInR==true{
//					logger.Infof("zs: gettx txid:%s normal namespace:%s wk:%s wv:%s InR",txId,rwset.NameSpace,wkv.Key,string(wkv.Value))
//					gnodeA.WriteSet[wkv.Key]=&WriteKV{
//						Key:   wkv.Key,
//						Value: wkv.Value,
//						Type:  1,
//					}
//				}else{
//					logger.Infof("zs: gettx txid:%s normal namespace:%s wk:%s wv:%s OutR",txId,rwset.NameSpace,wkv.Key,string(wkv.Value))
//					gnodeA.WriteSet[wkv.Key]=&WriteKV{
//						Key:   wkv.Key,
//						Value: wkv.Value,
//						Type:  0,
//					}
//				}
//
//			}
//
//
//		}
//	}
// 	//if len(ch.GCon.GTxWeight)>5{
//
//
//
//		gnodeResult,isExistgnode:=ch.GCon.GTxRelusts[txId]
//		if isExistgnode==false{
//			//zs:chuanxing
//			ch.GCon.GTxRelusts[txId]=&GNodeResult{
//				GnodeResult: gnodeA,
//				Next:        nil,
//				State: 2,
//			}
//			ch.GCon.GnodeMaxResults[txId]=&GNodeResult{
//				GnodeResult: gnodeA,
//				Next:        nil,
//				State:       2,
//			}
//		}else{
//			ch.GCon.GTxRelusts[txId].Next=&GNodeResult{
//				GnodeResult: gnodeResult.GnodeResult,
//				Next:        gnodeResult.Next,
//				State:       gnodeResult.State,
//			}
//			ch.GCon.GTxRelusts[txId].GnodeResult=gnodeA
//
//			if ch.GCon.GnodeMaxResults[txId].GnodeResult.State==3{
//				logger.Infof("zs: h.GCon.GnodeMaxResultsChange txid:%s txcount:%d  with oldTx txcount:%d",gnodeA.TxId,gnodeA.TxCount,ch.GCon.GnodeMaxResults[txId].GnodeResult.TxCount)
//				ch.GCon.GnodeMaxResults[txId].GnodeResult=gnodeA
//			}
//		}
//
//	if flagAddEdge==true{
//		if flagAddNode==true{
//
//			logger.Infof("zs: inblock wait weights and parents txid:%s txcount:%d",txId,txCount)
//		}else{
//			logger.Infof("zs: inblock wait weights txid:%s txcount:%d",txId,txCount)
//		}
//	}else{
//		logger.Infof("zs: inblock false addedge txid:%s txcount:%d",txId,txCount)
//		return false
//	}
//
//
//
//
// 	//}
//	return true
//
//}
//
//
//
//func (ch *chain) DeleteGnodeForFalse(gnode *GNode) {
//	txId:=gnode.TxId
//	logger.Infof("zs: deleteFalseInFun  be txid:%s txcount:%d",gnode.TxId,gnode.TxCount)
//	for _,wkv:= range  gnode.WriteSet{
//		logger.Infof("zs: deleteFalseInFun bewkv txid:%s wkv k:%s v:%s",txId,wkv.Key,string(wkv.Value))
//		wkvTxid := txId +"#"+wkv.Key+"#"+string(wkv.Value)
//
//
//		childnode,isExist:=ch.GCon.GEdgeSet[wkvTxid]
//		if isExist==false{
//			logger.Infof("zs: deleteFalseInFun bewkv txid:%s wkv k:%s v:%s is noexist Edge",txId,wkv.Key,string(wkv.Value))
//		}else if ch.GCon.GEdgeSet[wkvTxid].State==1{
//			logger.Infof("zs: deleteFalseInFun bewkv txid:%s wkv k:%s v:%s is state==1 Edge ZSerror",txId,wkv.Key,string(wkv.Value))
//		}else if ch.GCon.GEdgeSet[wkvTxid].State==3{
//			logger.Infof("zs: deleteFalseInFun bewkv txid:%s wkv k:%s v:%s is already deleteforfalse Edge",txId,wkv.Key,string(wkv.Value))
//		}else{
//			logger.Infof("zs: deleteFalseInFun bewkv txid:%s wkv k:%s v:%s is state==2 first delete",txId,wkv.Key,string(wkv.Value))
//			ch.GCon.GEdgeSet[wkvTxid].State=3
//			for childnode!=nil{
//				childStateInBlock:=ch.GCon.GNodeInBlock[childnode.ChildNode.TxId].State
//				if childStateInBlock==1{
//					logger.Infof("zs: deleteFalseInFun bewkv txid:%s wkv k:%s v:%s childnode txid:%s is committed ZSerror",
//						txId,wkv.Key,string(wkv.Value),childnode.ChildNode.TxId)
//				}else if childStateInBlock==3{
//					logger.Infof("zs: deleteFalseInFun bewkv txid:%s wkv k:%s v:%s childnode txid:%s is false",
//						txId,wkv.Key,string(wkv.Value),childnode.ChildNode.TxId)
//				}else{
//
//					logger.Infof("zs: deleteFalseInFun bewkv txid:%s wkv k:%s v:%s childnode txid:%s is wait",
//						txId,wkv.Key,string(wkv.Value),childnode.ChildNode.TxId)
//
//
//
//					if childnode.ChildNode.State==1{
//						logger.Infof("zs: deleteFalseInFun bewkv txid:%s wkv k:%s v:%s childnode txid:%s is wait childState== %d is already commit",
//							txId,wkv.Key,string(wkv.Value),childnode.ChildNode.TxId,childnode.ChildNode.State)
//					}else if childnode.ChildNode.State==3{
//						logger.Infof("zs: deleteFalseInFun bewkv txid:%s wkv k:%s v:%s childnode txid:%s is wait childState== %d is already false",
//							txId,wkv.Key,string(wkv.Value),childnode.ChildNode.TxId,childnode.ChildNode.State)
//					}else{
//						logger.Infof("zs: deleteFalseInFun bewkv txid:%s wkv k:%s v:%s childnode txid:%s is wait childState== %d is wait",
//							txId,wkv.Key,string(wkv.Value),childnode.ChildNode.TxId,childnode.ChildNode.State)
//
//						if childnode.ChildNodeType==1{
//							childnode.ChildNode.NumParentsOfRW--
//							logger.Infof("zs: deleteFalseInFun bewkv txid:%s wkv k:%s v:%s RWchildnode txid:%s PRWnum:%d",
//								txId,wkv.Key,string(wkv.Value),childnode.ChildNode.TxId,childnode.ChildNode.NumParentsOfRW)
//						}else if childnode.ChildNodeType==2{
//							childnode.ChildNode.NumParentsOfOW--
//							logger.Infof("zs: deleteFalseInFun bewkv txid:%s wkv k:%s v:%s OWchildnode txid:%s POWnum:%d",
//								txId,wkv.Key,string(wkv.Value),childnode.ChildNode.TxId,childnode.ChildNode.NumParentsOfOW)
//						}else{
//							childnode.ChildNode.NumParentsOfOR--
//							logger.Infof("zs: deleteFalseInFun bewkv txid:%s wkv k:%s v:%s ORchildnode txid:%s PORnum:%d",
//								txId,wkv.Key,string(wkv.Value),childnode.ChildNode.TxId,childnode.ChildNode.NumParentsOfOR)
//						}
//
//						if childnode.ChildNodeType==3{
//
//
//							//if childnode.ChildNode.NumParentsOfRW==0 &&
//							//	childnode.ChildNode.NumParentsOfOW==0 &&
//							//	childnode.ChildNode.NumParentsOfOR==0 {
//							if childnode.ChildNode.NumParentsOfRW==0 {
//								logger.Infof("zs: deleteFalseInFun bewkv txid:%s wkv k:%s v:%s ORchildnode txid:%s get all Ptx",
//									txId,wkv.Key,string(wkv.Value),childnode.ChildNode.TxId)
//
//								if childnode.ChildNode.Votes>=ch.GCon.NumKWeight{
//									logger.Infof("zs: deleteFalseInFun bewkv txid:%s wkv k:%s v:%s ORchildnode txid:%s has enough votes",
//										txId,wkv.Key,string(wkv.Value),childnode.ChildNode.TxId)
//
//
//									flagIsSameInKV:=true
//									for _,rkv:=range childnode.ChildNode.ReadSet {
//
//										kvalue:=ch.GCon.GKVTable[rkv.Key]
//										newvalue:=kvalue[:len(kvalue)-len(rkv.ParentTxId)]
//										newtxid:=kvalue[len(kvalue)-len(rkv.ParentTxId):]
//
//										if strings.Compare(string(rkv.Value),newvalue)!=0{
//											logger.Infof("zs: txid:%s before commmit false rk:%s rv:%s newvalue:%s changed by txid:%s",
//												childnode.ChildNode.TxId,rkv.Key,string(rkv.Value),newvalue,newtxid)
//											flagIsSameInKV=false
//											break
//										}
//									}
//									if flagIsSameInKV==true{
//										childnode.ChildNode.State=1
//										ch.GCon.GNodeInBlock[childnode.ChildNode.TxId].State=1
//										ch.GCon.GNodeInBlock[childnode.ChildNode.TxId].IsCommitted=1
//										ch.GCon.GNodeInBlock[childnode.ChildNode.TxId].committedGnode=childnode.ChildNode
//										ch.GCon.GnodeMaxResults[childnode.ChildNode.TxId].GnodeResult=childnode.ChildNode
//										ch.GCon.GnodeMaxResults[childnode.ChildNode.TxId].State=1
//										ch.GCon.GTxRelusts[childnode.ChildNode.TxId].State=1
//
//
//										for _,rwset:=range childnode.ChildNode.TxRWSet.NsRwSets{
//											logger.Infof("zs: gettx txid:%s namespace:%s ",childnode.ChildNode.TxId,rwset.NameSpace)
//											if strings.Compare(rwset.NameSpace,"lscc")!=0{
//												for _,wkvchild:=range rwset.KvRwSet.Writes{
//													ch.GCon.GKVTable[wkvchild.Key]=string(wkvchild.Value)+txId
//													logger.Infof("zs: gettx txid:%s ch.GCon.GKVTable[%s]=%s",childnode.ChildNode.TxId,wkvchild.Key,string(wkvchild.Value))
//												}
//											}
//										}
//
//
//
//
//
//
//										logger.Infof("zs: committed inblock txid:%s txcount:%d",childnode.ChildNode.TxId,childnode.ChildNode.TxCount)
//										batches, _ := ch.support.BlockCutter().Ordered(childnode.ChildNode.TxResult)
//										ch.GCon.NumSendInBlock++
//										for _, batch := range batches {
//											block := ch.support.CreateNextBlock(batch)
//											ch.support.WriteBlock(block, nil)
//										}
//										gnodeResult:=ch.GCon.GTxRelusts[childnode.ChildNode.TxId]
//										logger.Infof("zs: deleteSuccess as child be txid:%s txcount:%d",childnode.ChildNode.TxId,childnode.ChildNode.TxCount)
//										ch.DeleteGnodeForSuccess(childnode.ChildNode)
//										logger.Infof("zs: deleteSuccess as child en txid:%s txcount:%d",childnode.ChildNode.TxId,childnode.ChildNode.TxCount)
//										for gnodeResult!=nil {
//											if gnodeResult.GnodeResult.State==1{
//												logger.Infof("zs: deleteSuccess as child is already txid:%s txcount:%d",gnodeResult.GnodeResult.TxId,gnodeResult.GnodeResult.TxCount)
//											}else if gnodeResult.GnodeResult.State==2{
//												gnodeResult.GnodeResult.State=3
//												logger.Infof("zs: deleteFalseBecauseOtherSucc as child be txid:%s txcount:%d",gnodeResult.GnodeResult.TxId,gnodeResult.GnodeResult.TxCount)
//												ch.DeleteGnodeForFalse(gnodeResult.GnodeResult)
//												logger.Infof("zs: deleteFalseBecauseOtherSucc as child en txid:%s txcount:%d",gnodeResult.GnodeResult.TxId,gnodeResult.GnodeResult.TxCount)
//											}else{
//												logger.Infof("zs: deleteFalseBecauseOtherSucc as child already 3 txid:%s txcount:%d",gnodeResult.GnodeResult.TxId,gnodeResult.GnodeResult.TxCount)
//											}
//											gnodeResult=gnodeResult.Next
//										}
//
//									}else{
//										logger.Infof("zs: deleteFalseInFun bewkv txid:%s wkv k:%s v:%s ORchildnode txid:%s has enough votes but rkv has changed",
//											txId,wkv.Key,string(wkv.Value),childnode.ChildNode.TxId)
//
//										if ch.GCon.GTxWeight[childnode.ChildNode.TxId]==ch.GCon.NumExeNodes{
//											logger.Infof("zs: deleteFalseInFun bewkv txid:%s wkv k:%s v:%s ORchildnode txid:%s has enough votes but rkv has changed with enough weights",
//												txId,wkv.Key,string(wkv.Value),childnode.ChildNode.TxId)
//
//
//
//
//
//										}
//
//
//
//
//									}
//
//
//
//
//
//
//								}else{
//									logger.Infof("zs: deleteFalseInFun bewkv txid:%s wkv k:%s v:%s ORchildnode txid:%s wait votes :%d",
//										txId,wkv.Key,string(wkv.Value),childnode.ChildNode.TxId,childnode.ChildNode.Votes)
//
//								}
//							}
//
//						}else{
//							logger.Infof("zs: deleteFalse as childtx be txid:%s txcount:%d ptxid:%s",
//								childnode.ChildNode.TxId,childnode.ChildNode.TxCount,txId)
//							childnode.ChildNode.State=3
//							ch.DeleteGnodeForFalse(childnode.ChildNode)
//							logger.Infof("zs: deleteFalse as childtx en txid:%s txcount:%d ptxid:%s",
//								childnode.ChildNode.TxId,childnode.ChildNode.TxCount,txId)
//						}
//					}
//				}
//				childnode=childnode.Next
//			}
//
//		}
//		logger.Infof("zs: deleteFalseInFun enwkv txid:%s wkv k:%s v:%s",txId,wkv.Key,string(wkv.Value))
//	}
//	logger.Infof("zs: deleteFalseInFun  en txid:%s txcount:%d",gnode.TxId,gnode.TxCount)
//
//
//
//}
//func (ch *chain) DeleteGnodeForSuccess(gnode *GNode)  {
//	txId:=gnode.TxId
//	logger.Infof("zs: deleteSuccessInFun  be txid:%s",txId)
//	for _,wkv:= range  gnode.WriteSet{
//		logger.Infof("zs: deleteSuccessInFun bewkv txid:%s wkv k:%s v:%s",txId,wkv.Key,string(wkv.Value))
//		wkvTxid := txId +"#"+wkv.Key+"#"+string(wkv.Value)
//
//		childnode,isExist:=ch.GCon.GEdgeSet[wkvTxid]
//
//if isExist==true{
//	for childnode!=nil{
//		logger.Infof("zs: ????? deleteSuccessInFun bewkv txid:%s wkv k:%s v:%s childnode txid:%s is committed",
//			txId,wkv.Key,string(wkv.Value),childnode.ChildNode.TxId)
//		childnode=childnode.Next
//	}
//}
//
//		childnode,isExist=ch.GCon.GEdgeSet[wkvTxid]
//
//		if isExist==false{
//			logger.Infof("zs: deleteSuccessInFun bewkv txid:%s wkv k:%s v:%s is noexist Edge",txId,wkv.Key,string(wkv.Value))
//		}else if ch.GCon.GEdgeSet[wkvTxid].State==3{
//			logger.Infof("zs: deleteSuccessInFun bewkv txid:%s wkv k:%s v:%s is state==3 Edge ZSerror",txId,wkv.Key,string(wkv.Value))
//		}else if ch.GCon.GEdgeSet[wkvTxid].State==1{
//			logger.Infof("zs: deleteSuccessInFun bewkv txid:%s wkv k:%s v:%s is already deleteforsuccess Edge",txId,wkv.Key,string(wkv.Value))
//		}else{
//			logger.Infof("zs: deleteSuccessInFun bewkv txid:%s wkv k:%s v:%s is state==2 first deleteforsuccess",txId,wkv.Key,string(wkv.Value))
//			ch.GCon.GEdgeSet[wkvTxid].State=1
//			for childnode!=nil{
//				childStateInBlock:=ch.GCon.GNodeInBlock[childnode.ChildNode.TxId].State
//				if childStateInBlock==1{
//
//					logger.Infof("zs: deleteSuccessInFun bewkv txid:%s wkv k:%s v:%s childnode txid:%s is committed",
//						txId,wkv.Key,string(wkv.Value),childnode.ChildNode.TxId)
//				}else if childStateInBlock==3{
//					logger.Infof("zs: deleteSuccessInFun bewkv txid:%s wkv k:%s v:%s childnode txid:%s is false",
//						txId,wkv.Key,string(wkv.Value),childnode.ChildNode.TxId)
//				}else{
//
//					logger.Infof("zs: deleteSuccessInFun bewkv txid:%s wkv k:%s v:%s childnode txid:%s is wait",
//						txId,wkv.Key,string(wkv.Value),childnode.ChildNode.TxId)
//
//
//
//					if childnode.ChildNode.State==1{
//						logger.Infof("zs: deleteSuccessInFun bewkv txid:%s wkv k:%s v:%s childnode txid:%s is wait childState== %d is already commit",
//							txId,wkv.Key,string(wkv.Value),childnode.ChildNode.TxId,childnode.ChildNode.State)
//					}else if childnode.ChildNode.State==3{
//						logger.Infof("zs: deleteSuccessInFun bewkv txid:%s wkv k:%s v:%s childnode txid:%s is wait childState== %d is already false",
//							txId,wkv.Key,string(wkv.Value),childnode.ChildNode.TxId,childnode.ChildNode.State)
//					}else{
//
//						logger.Infof("zs: deleteSuccessInFun bewkv txid:%s wkv k:%s v:%s childnode txid:%s is wait childState== %d is wait",
//							txId,wkv.Key,string(wkv.Value),childnode.ChildNode.TxId,childnode.ChildNode.State)
//
//
//
//						if childnode.ChildNodeType==1{
//							childnode.ChildNode.NumParentsOfRW--
//							logger.Infof("zs: deleteSuccessInFun bewkv txid:%s wkv k:%s v:%s RWchildnode txid:%s PRWnum:%d",
//								txId,wkv.Key,string(wkv.Value),childnode.ChildNode.TxId,childnode.ChildNode.NumParentsOfRW)
//						}else if childnode.ChildNodeType==2{
//							childnode.ChildNode.NumParentsOfOW--
//							logger.Infof("zs: deleteSuccessInFun bewkv txid:%s wkv k:%s v:%s RWchildnode txid:%s POWnum:%d",
//								txId,wkv.Key,string(wkv.Value),childnode.ChildNode.TxId,childnode.ChildNode.NumParentsOfOW)
//						}else{
//							childnode.ChildNode.NumParentsOfOR--
//							logger.Infof("zs: deleteSuccessInFun bewkv txid:%s wkv k:%s v:%s RWchildnode txid:%s PORnum:%d",
//								txId,wkv.Key,string(wkv.Value),childnode.ChildNode.TxId,childnode.ChildNode.NumParentsOfOR)
//						}
//						//if childnode.ChildNode.NumParentsOfRW==0 &&
//						//	childnode.ChildNode.NumParentsOfOW==0 &&
//						//	childnode.ChildNode.NumParentsOfOR==0 {
//						if childnode.ChildNode.NumParentsOfRW==0{
//							logger.Infof("zs: deleteSuccessInFun bewkv txid:%s wkv k:%s v:%s childnode txid:%s get all Ptx",
//								txId,wkv.Key,string(wkv.Value),childnode.ChildNode.TxId)
//
//							if childnode.ChildNode.Votes>=ch.GCon.NumKWeight{
//
//								logger.Infof("zs: deleteSuccessInFun bewkv txid:%s wkv k:%s v:%s childnode txid:%s get votes:%d",
//									txId,wkv.Key,string(wkv.Value),childnode.ChildNode.TxId,childnode.ChildNode.Votes)
//
//
//								flagIsSameInKV:=true
//								for _,rkv:=range childnode.ChildNode.ReadSet {
//
//									kvalue:=ch.GCon.GKVTable[rkv.Key]
//									newvalue:=kvalue[:len(kvalue)-len(rkv.ParentTxId)]
//									newtxid:=kvalue[len(kvalue)-len(rkv.ParentTxId):]
//
//									if strings.Compare(string(rkv.Value),newvalue)!=0{
//										logger.Infof("zs: txid:%s before commmit false rk:%s rv:%s newvalue:%s changed by txid:%s",
//											childnode.ChildNode.TxId,rkv.Key,string(rkv.Value),newvalue,newtxid)
//										flagIsSameInKV=false
//										break
//									}
//								}
//								if flagIsSameInKV==true{
//									logger.Infof("zs: deleteSuccessInFun bewkv txid:%s wkv k:%s v:%s childnode txid:%s committed",
//										txId,wkv.Key,string(wkv.Value),childnode.ChildNode.TxId)
//
//
//									childnode.ChildNode.State=1
//									ch.GCon.GNodeInBlock[childnode.ChildNode.TxId].State=1
//									ch.GCon.GNodeInBlock[childnode.ChildNode.TxId].IsCommitted=1
//									ch.GCon.GNodeInBlock[childnode.ChildNode.TxId].committedGnode=childnode.ChildNode
//									ch.GCon.GnodeMaxResults[childnode.ChildNode.TxId].GnodeResult=childnode.ChildNode
//									ch.GCon.GnodeMaxResults[childnode.ChildNode.TxId].State=1
//									ch.GCon.GTxRelusts[childnode.ChildNode.TxId].State=1
//
//									for _,rwset:=range childnode.ChildNode.TxRWSet.NsRwSets{
//										logger.Infof("zs: gettx txid:%s namespace:%s ",childnode.ChildNode.TxId,rwset.NameSpace)
//										if strings.Compare(rwset.NameSpace,"lscc")!=0{
//											for _,wkvchild:=range rwset.KvRwSet.Writes{
//												ch.GCon.GKVTable[wkvchild.Key]=string(wkvchild.Value)+txId
//												logger.Infof("zs: gettx txid:%s ch.GCon.GKVTable[%s]=%s",childnode.ChildNode.TxId,wkvchild.Key,string(wkvchild.Value))
//											}
//										}
//									}
//
//
//									logger.Infof("zs: committed inblock txid:%s txcount:%d",childnode.ChildNode.TxId,childnode.ChildNode.TxCount)
//									batches, _ := ch.support.BlockCutter().Ordered(childnode.ChildNode.TxResult)
//									ch.GCon.NumSendInBlock++
//									for _, batch := range batches {
//										block := ch.support.CreateNextBlock(batch)
//										ch.support.WriteBlock(block, nil)
//									}
//
//									logger.Infof("zs: deleteSuccess as child be txid:%s txcount:%d",childnode.ChildNode.TxId,childnode.ChildNode.TxCount)
//									ch.DeleteGnodeForSuccess(childnode.ChildNode)
//									logger.Infof("zs: deleteSuccess as child en txid:%s txcount:%d",childnode.ChildNode.TxId,childnode.ChildNode.TxCount)
//
//									gnodeResult:=ch.GCon.GTxRelusts[childnode.ChildNode.TxId]
//
//									for gnodeResult!=nil {
//										if gnodeResult.GnodeResult.State==1{
//											logger.Infof("zs: deleteSuccess as child is already txid:%s txcount:%d",gnodeResult.GnodeResult.TxId,gnodeResult.GnodeResult.TxCount)
//										}else if gnodeResult.GnodeResult.State==2{
//											gnodeResult.GnodeResult.State=3
//											logger.Infof("zs: deleteFalseBecauseOtherSucc as child be txid:%s txcount:%d",gnodeResult.GnodeResult.TxId,gnodeResult.GnodeResult.TxCount)
//											ch.DeleteGnodeForFalse(gnodeResult.GnodeResult)
//											logger.Infof("zs: deleteFalseBecauseOtherSucc as child en txid:%s txcount:%d",gnodeResult.GnodeResult.TxId,gnodeResult.GnodeResult.TxCount)
//										}else{
//											logger.Infof("zs: deleteFalseBecauseOtherSucc as child already 3 txid:%s txcount:%d",gnodeResult.GnodeResult.TxId,gnodeResult.GnodeResult.TxCount)
//										}
//										gnodeResult=gnodeResult.Next
//									}
//								}else{
//									childnode.ChildNode.State=3
//									logger.Infof("zs: deleteSuccessInFun bewkv txid:%s wkv k:%s v:%s childnode txid:%s get votes:%d but rkv has changed",
//										txId,wkv.Key,string(wkv.Value),childnode.ChildNode.TxId,childnode.ChildNode.Votes)
//
//								}
//
//
//
//
//
//							}else{
//								logger.Infof("zs: deleteSuccessInFun bewkv txid:%s wkv k:%s v:%s childnode txid:%s wait votes :%d",
//									txId,wkv.Key,string(wkv.Value),childnode.ChildNode.TxId,childnode.ChildNode.Votes)
//
//							}
//						}
//
//					}
//				}
//				childnode=childnode.Next
//			}
//		}
//		logger.Infof("zs: deleteSuccessInFun enwkv txid:%s wkv k:%s v:%s",txId,wkv.Key,string(wkv.Value))
//	}
//}
//
//func (ch *chain) main() {
//	var timer <-chan time.Time
//
//	var err error
//
//	for {
//		seq := ch.support.Sequence()
//		err = nil
//		select {
//		case msg := <-ch.sendChan:
//			if msg.configMsg == nil {
//				// NormalMsg
//				//if msg.configSeq < seq {
//				//	_, err = ch.support.ProcessNormalMsg(msg.normalMsg)
//				//	if err != nil {
//				//		logger.Warningf("Discarding bad normal message: %s", err)
//				//		continue
//				//	}
//				//}
//
//				env:=msg.normalMsg
//				txCount,txId,_,respPayload,_:=utils.GetTxnResultFromEnvelopeMsg(env)
//
//				logger.Infof("zs:### txid:%s txcount:%d all tx:%d txwithallvotes:%d NumsendInblcok:%d 2=Votes:%d 3=Votes:%d 4=Votes:%d",
//					txId,txCount, len(ch.GCon.GNodeInBlock), ch.GCon.NumTxwithAllvotes,ch.GCon.NumSendInBlock,ch.GCon.NumVotes2,ch.GCon.NumVotes3,ch.GCon.NumVotes4)
//
//				txState,isExistBefore:=ch.GCon.GNodeInBlock[txId]
//
//				if isExistBefore==false{
//
//					ch.GCon.GTxWeight[txId]=1
//					ch.GCon.GNodeInBlock[txId]=&CommittedGnode{
//						State:          2,
//						committedGnode: nil,
//						IsCommitted: 0,
//					}
//					logger.Infof("zs: gettx times:%d in block txid:%s txcount:%d firsttimes",ch.GCon.GTxWeight[txId],txId,txCount)
//
//
//					txRWSet := &rwsetutil.TxRwSet{}
//					if err = txRWSet.FromProtoBytes(respPayload.Results); err != nil {
//						logger.Infof("zs: gettx txid:%s txcount:%d txRWSet.FromProtoBytes failed",txId,txCount)
//					}
//					gnode:=&GNode{
//						TxResult: 	env,
//						TxRWSet: txRWSet,
//						TxId:      txId,
//						TxCount:   txCount,
//						PeerId:    "",
//						Votes:  1,
//						ParentTxs: map[string]*ReadKV{},
//						ReadSet: map[string]*ReadKV{},
//						WriteSet: map[string]*WriteKV{},
//						State: 2,
//						NumParentsOfRW: 0,
//						NumParentsOfOW: 0,
//						NumParentsOfOR: 0,
//
//					}
//					flagAddNode:=ch.GConAddNode(gnode)
//					if flagAddNode==true{
//						logger.Infof("zs: gettx txid:%s txcount:%d addnode success",txId,txCount)
//					}else{
//						logger.Infof("zs: gettx txid:%s txcount:%d addnode false",txId,txCount)
//					}
//
//
//					if len(ch.GCon.GTxWeight)<=1{
//						gnode.State=1
//						ch.GCon.GNodeInBlock[txId].State=1
//						ch.GCon.GNodeInBlock[txId].IsCommitted=1
//						ch.GCon.GNodeInBlock[txId].committedGnode=gnode
//						ch.GCon.GnodeMaxResults[txId].GnodeResult=gnode
//						ch.GCon.GnodeMaxResults[txId].State=1
//						ch.GCon.GTxRelusts[txId].State=1
//						logger.Infof("zs: committed inblock txid:%s txcount:%d",txId,txCount)
//
//						for _,rwset:=range txRWSet.NsRwSets{
//							logger.Infof("zs: gettx txid:%s namespace:%s ",txId,rwset.NameSpace)
//							if strings.Compare(rwset.NameSpace,"lscc")!=0{
//
//
//								for _,wkv:=range rwset.KvRwSet.Writes{
//									ch.GCon.GKVTable[wkv.Key]=string(wkv.Value)+txId
//										logger.Infof("zs: gettx txid:%s ch.GCon.GKVTable[%s]=%s",txId,wkv.Key,string(wkv.Value))
//								}
//
//
//							}
//						}
//
//
//
//						batches, pending := ch.support.BlockCutter().Ordered(gnode.TxResult)
//						ch.GCon.NumSendInBlock++
//						for _, batch := range batches {
//							block := ch.support.CreateNextBlock(batch)
//							ch.support.WriteBlock(block, nil)
//						}
//
//						//gnodeResult:=ch.GCon.GTxRelusts[txId]
//						//
//						//
//						//
//						//for gnodeResult!=nil {
//						//	if gnodeResult.GnodeResult.State==1{
//						//		logger.Infof("zs: deleteSuccess be txid:%s txcount:%d",gnodeResult.GnodeResult.TxId,gnodeResult.GnodeResult.TxCount)
//						//		ch.DeleteGnodeForSuccess(gnodeResult.GnodeResult)
//						//		logger.Infof("zs: deleteSuccess en txid:%s txcount:%d",gnodeResult.GnodeResult.TxId,gnodeResult.GnodeResult.TxCount)
//						//	}else if gnodeResult.GnodeResult.State==2{
//						//		gnodeResult.GnodeResult.State=3
//						//		logger.Infof("zs: deleteFalseBecauseOtherSucc be txid:%s txcount:%d",gnodeResult.GnodeResult.TxId,gnodeResult.GnodeResult.TxCount)
//						//		ch.DeleteGnodeForFalse(gnodeResult.GnodeResult)
//						//		logger.Infof("zs: deleteFalseBecauseOtherSucc en txid:%s txcount:%d",gnodeResult.GnodeResult.TxId,gnodeResult.GnodeResult.TxCount)
//						//	}else{
//						//		logger.Infof("zs: deleteFalseBecauseOtherSucc already 3 txid:%s txcount:%d",gnodeResult.GnodeResult.TxId,gnodeResult.GnodeResult.TxCount)
//						//	}
//						//	gnodeResult=gnodeResult.Next
//						//}
//
//
//
//
//
//
//						switch {
//						case timer != nil && !pending:
//							// Timer is already running but there are no messages pending, stop the timer
//							timer = nil
//						case timer == nil && pending:
//							// Timer is not already running and there are messages pending, so start it
//							timer = time.After(ch.support.SharedConfig().BatchTimeout())
//							logger.Debugf("Just began %s batch timer", ch.support.SharedConfig().BatchTimeout().String())
//						default:
//							// Do nothing when:
//							// 1. Timer is already running and there are messages pending
//							// 2. Timer is not set and there are no messages pending
//						}
//					}
//
//				}else if txState.State==2{
//					txTimes:=ch.GCon.GTxWeight[txId]
//					txTimes++
//					ch.GCon.GTxWeight[txId]=txTimes
//					logger.Infof("zs: gettx times:%d in block txid:%s txcount:%d ",ch.GCon.GTxWeight[txId],txId,txCount)
//
//					if txTimes==ch.GCon.NumExeNodes{
//						ch.GCon.NumTxwithAllvotes++
//					}
//
//
//
//
//
//
//
//
//					txRWSet := &rwsetutil.TxRwSet{}
//					if err = txRWSet.FromProtoBytes(respPayload.Results); err != nil {
//						logger.Infof("zs: gettx txid:%s txcount:%d txRWSet.FromProtoBytes failed",txId,txCount)
//					}
//
//					gnode:=&GNode{
//						TxResult: 	env,
//						TxRWSet: txRWSet,
//						TxId:      txId,
//						TxCount:   txCount,
//						PeerId:    "",
//						Votes:  1,
//						ParentTxs: map[string]*ReadKV{},
//						ReadSet: map[string]*ReadKV{},
//						WriteSet: map[string]*WriteKV{},
//						State: 2,
//						NumParentsOfRW: 0,
//						NumParentsOfOW: 0,
//						NumParentsOfOR: 0,
//					}
//
//
//
//
//
//					//for _,rwset:=range txRWSet.NsRwSets{
//					//	logger.Infof("zs: gettx txid:%s namespace:%s ",txId,rwset.NameSpace)
//					//	if strings.Compare(rwset.NameSpace,"lscc")!=0{
//					//		for _,rkv:=range rwset.KvRwSet.Reads{
//					//			rkValue:=string(rkv.Value)
//					//			if strings.Compare(rkValue,"onlywrite")==0{
//					//
//					//				logger.Infof("zs: gettx txid:%s onlywrite rtxid:%s rtxcount:%d namespace:%s rk:%s rv:%s rtxid:%s",txId,rkv.Txid,rkv.Version.TxNum,rwset.NameSpace,rkv.Key,string(rkv.Value),rkv.Txid)
//					//			}else if strings.Compare(rkValue,"onlyread")==0{
//					//
//					//				logger.Infof("zs: gettx txid:%s onlyread rtxid:%s rtxcount:%d namespace:%s rk:%s rv:%s rtxid:%s",txId,rkv.Txid,rkv.Version.TxNum,rwset.NameSpace,rkv.Key,string(rkv.Value),rkv.Txid)
//					//			}else{
//					//				logger.Infof("zs: gettx txid:%s normal namespace:%s rk:%s rv:%s rtxid:%s",txId,rwset.NameSpace,rkv.Key,string(rkv.Value),rkv.Txid)
//					//			}
//					//		}
//					//
//					//		for _,wkv:=range rwset.KvRwSet.Writes{
//					//
//					//				logger.Infof("zs: gettx txid:%s normal namespace:%s wk:%s wv:%s InR",txId,rwset.NameSpace,wkv.Key,string(wkv.Value))
//					//
//					//
//					//		}
//					//
//					//
//					//	}
//					//}
//
//
//
//
//
//
//					txResult:=ch.GCon.GTxRelusts[gnode.TxId]
//					flagIsSame:=false
//					for txResult!=nil{
//						if ch.GnodeCompare(gnode,txResult.GnodeResult)==true{
//							flagIsSame=true
//							break
//						}
//						txResult=txResult.Next
//					}
//					if flagIsSame==true{
//						txResult.GnodeResult.Votes++
//
//						if txResult.GnodeResult.Votes==2{
//							ch.GCon.NumVotes2++
//						}else if txResult.GnodeResult.Votes==3{
//							ch.GCon.NumVotes3++
//						}else if txResult.GnodeResult.Votes==4{
//							ch.GCon.NumVotes4++
//						}
//
//						if  txResult.GnodeResult.State==2 {
//
//							if ch.GCon.GnodeMaxResults[txId].GnodeResult.State==3{
//
//								logger.Infof("zs: gettx times:%d in block txid:%s txcount:%d flagIsSame=true max votes:%d  change maxresult vote:%d because state==3",
//									ch.GCon.GTxWeight[txId],txId,txCount,txResult.GnodeResult.Votes,ch.GCon.GnodeMaxResults[txId].GnodeResult.Votes)
//								ch.GCon.GnodeMaxResults[txId].GnodeResult=txResult.GnodeResult
//							}else if txResult.GnodeResult.Votes > ch.GCon.GnodeMaxResults[txId].GnodeResult.Votes{
//								ch.GCon.GnodeMaxResults[txId].GnodeResult=txResult.GnodeResult
//								logger.Infof("zs: gettx times:%d in block txid:%s txcount:%d flagIsSame=true max votes:%d",ch.GCon.GTxWeight[txId],txId,txCount,txResult.GnodeResult.Votes)
//							}else{
//								logger.Infof("zs: gettx times:%d in block txid:%s txcount:%d flagIsSame=true wait votes:%d ",ch.GCon.GTxWeight[txId],txId,txCount,txResult.GnodeResult.Votes)
//							}
//						}else if txResult.GnodeResult.State==3{
//							logger.Infof("zs: gettx times:%d in block txid:%s txcount:%d flagIsSame=true false state votes:%d ",ch.GCon.GTxWeight[txId],txId,txCount,txResult.GnodeResult.Votes)
//
//						}else{
//							logger.Infof("zs: gettx times:%d in block txid:%s txcount:%d flagIsSame=true error state=1 votes:%d ",ch.GCon.GTxWeight[txId],txId,txCount,txResult.GnodeResult.Votes)
//						}
//
//					}else{
//						flagAddNode:=ch.GConAddNode(gnode)
//						if flagAddNode==true{
//							logger.Infof("zs: gettx txid:%s txcount:%d addnode success in state==2",txId,txCount)
//						}else{
//							logger.Infof("zs: gettx txid:%s txcount:%d addnode false in state==2",txId,txCount)
//						}
//					}
//
//
//
//
//
//					_,isExistMaxresult:=ch.GCon.GnodeMaxResults[txId]
//
//
//
//
//					if len(ch.GCon.GTxWeight)<=5{
//						if txTimes==5{
//							gnode.State=1
//							ch.GCon.GNodeInBlock[txId].State=1
//							ch.GCon.GNodeInBlock[txId].IsCommitted=1
//							//ch.GCon.GNodeInBlock[txId].committedGnode=gnode
//							//ch.GCon.GnodeMaxResults[txId].GnodeResult=gnode
//							ch.GCon.GNodeInBlock[txId].committedGnode=ch.GCon.GnodeMaxResults[txId].GnodeResult
//							ch.GCon.GnodeMaxResults[txId].State=1
//							ch.GCon.GTxRelusts[txId].State=1
//							for _,rwset:=range txRWSet.NsRwSets{
//								logger.Infof("zs: gettx txid:%s namespace:%s ",txId,rwset.NameSpace)
//								if strings.Compare(rwset.NameSpace,"lscc")!=0{
//									for _,wkv:=range rwset.KvRwSet.Writes{
//										ch.GCon.GKVTable[wkv.Key]=string(wkv.Value)+txId
//										logger.Infof("zs: gettx txid:%s ch.GCon.GKVTable[%s]=%s",txId,wkv.Key,string(wkv.Value))
//									}
//								}
//							}
//							logger.Infof("zs: committed inblock txid:%s txcount:%d",txId,txCount)
//							batches, pending := ch.support.BlockCutter().Ordered(gnode.TxResult)
//							ch.GCon.NumSendInBlock++
//							for _, batch := range batches {
//								block := ch.support.CreateNextBlock(batch)
//								ch.support.WriteBlock(block, nil)
//							}
//
//							//gnodeResult:=ch.GCon.GTxRelusts[txId]
//
//
//
//							//for gnodeResult!=nil {
//							//	if gnodeResult.GnodeResult.State==1{
//							//		logger.Infof("zs: deleteSuccess be txid:%s txcount:%d",gnodeResult.GnodeResult.TxId,gnodeResult.GnodeResult.TxCount)
//							//		ch.DeleteGnodeForSuccess(gnodeResult.GnodeResult)
//							//		logger.Infof("zs: deleteSuccess en txid:%s txcount:%d",gnodeResult.GnodeResult.TxId,gnodeResult.GnodeResult.TxCount)
//							//	}else if gnodeResult.GnodeResult.State==2{
//							//		gnodeResult.GnodeResult.State=3
//							//		logger.Infof("zs: deleteFalseBecauseOtherSucc be txid:%s txcount:%d",gnodeResult.GnodeResult.TxId,gnodeResult.GnodeResult.TxCount)
//							//		ch.DeleteGnodeForFalse(gnodeResult.GnodeResult)
//							//		logger.Infof("zs: deleteFalseBecauseOtherSucc en txid:%s txcount:%d",gnodeResult.GnodeResult.TxId,gnodeResult.GnodeResult.TxCount)
//							//	}else{
//							//		logger.Infof("zs: deleteFalseBecauseOtherSucc already 3 txid:%s txcount:%d",gnodeResult.GnodeResult.TxId,gnodeResult.GnodeResult.TxCount)
//							//	}
//							//	gnodeResult=gnodeResult.Next
//							//}
//
//
//
//
//
//
//							switch {
//							case timer != nil && !pending:
//								// Timer is already running but there are no messages pending, stop the timer
//								timer = nil
//							case timer == nil && pending:
//								// Timer is not already running and there are messages pending, so start it
//								timer = time.After(ch.support.SharedConfig().BatchTimeout())
//								logger.Debugf("Just began %s batch timer", ch.support.SharedConfig().BatchTimeout().String())
//							default:
//								// Do nothing when:
//								// 1. Timer is already running and there are messages pending
//								// 2. Timer is not set and there are no messages pending
//							}
//						}
//
//
//					} else if isExistMaxresult==true{
//
//
//
//
//
//
//
//						if ch.GCon.GnodeMaxResults[txId].GnodeResult.Votes>=ch.GCon.NumKWeight{
//
//
//
//
//
//							logger.Infof("zs: gettx times:%d in block txid:%s txcount:%d success consenesus get %d votes with maxvotes: %d ",
//								ch.GCon.GTxWeight[txId],txId,txCount,txTimes,ch.GCon.GnodeMaxResults[txId].GnodeResult.Votes)
//
//
//							if ch.GCon.GnodeMaxResults[txId].GnodeResult.State==3{
//
//								if ch.GCon.GTxWeight[txId]==ch.GCon.NumExeNodes{
//									logger.Infof("zs: gettx times:%d in block txid:%s txcount:%d success consenesus get %d votes with maxvotes: %d but state==3 false",
//										ch.GCon.GTxWeight[txId],txId,txCount,txTimes,ch.GCon.GnodeMaxResults[txId].GnodeResult.Votes)
//
//
//
//									ch.GCon.GNodeInBlock[txId].State=3
//									ch.GCon.GNodeInBlock[txId].committedGnode=nil
//
//									ch.GCon.GnodeMaxResults[txId].State=3
//									ch.GCon.GTxRelusts[txId].State=3
//
//
//
//									gnodeResult:=ch.GCon.GTxRelusts[txId]
//									for gnodeResult!=nil {
//										if gnodeResult.GnodeResult.State==1{
//											logger.Infof("zs: deleteFasle but state==1 be txid:%s txcount:%d ZSerror",gnodeResult.GnodeResult.TxId,gnodeResult.GnodeResult.TxCount)
//										}else if gnodeResult.GnodeResult.State==2{
//											gnodeResult.GnodeResult.State=3
//											logger.Infof("zs: deleteFalseBecauseFalse be txid:%s txcount:%d",gnodeResult.GnodeResult.TxId,gnodeResult.GnodeResult.TxCount)
//											ch.DeleteGnodeForFalse(gnodeResult.GnodeResult)
//											logger.Infof("zs: deleteFalseBecauseFalse en txid:%s txcount:%d",gnodeResult.GnodeResult.TxId,gnodeResult.GnodeResult.TxCount)
//										}else{
//											logger.Infof("zs: deleteFalseBecauseOtherFalse already 3 txid:%s txcount:%d",gnodeResult.GnodeResult.TxId,gnodeResult.GnodeResult.TxCount)
//										}
//										gnodeResult=gnodeResult.Next
//									}
//								}else{
//									logger.Infof("zs: gettx times:%d in block txid:%s txcount:%d success consenesus get %d votes with maxvotes: %d but state==3 wait",
//										ch.GCon.GTxWeight[txId],txId,txCount,txTimes,ch.GCon.GnodeMaxResults[txId].GnodeResult.Votes)
//								}
//
//
//
//
//
//
//
//							}else{
//
//
//								//if ch.GCon.GnodeMaxResults[txId].GnodeResult.NumParentsOfRW==0 &&
//								//	ch.GCon.GnodeMaxResults[txId].GnodeResult.NumParentsOfOR==0 &&
//								//	ch.GCon.GnodeMaxResults[txId].GnodeResult.NumParentsOfOW==0{
//								if ch.GCon.GnodeMaxResults[txId].GnodeResult.NumParentsOfRW==0 {
//
//									logger.Infof("zs: gettx times:%d in block txid:%s txcount:%d success consenesus RW=0 get %d votes with maxvotes: %d waitPtx PRWtx:%d POWtx:%d PORtx:%d",
//										ch.GCon.GTxWeight[txId],txId,txCount,txTimes,ch.GCon.GnodeMaxResults[txId].GnodeResult.Votes,
//										ch.GCon.GnodeMaxResults[txId].GnodeResult.NumParentsOfRW,
//										ch.GCon.GnodeMaxResults[txId].GnodeResult.NumParentsOfOW,
//										ch.GCon.GnodeMaxResults[txId].GnodeResult.NumParentsOfOR)
//
//									flagIsSameInKV:=true
//									for _,rkv:=range ch.GCon.GnodeMaxResults[txId].GnodeResult.ReadSet {
//
//										kvalue:=ch.GCon.GKVTable[rkv.Key]
//										newvalue:=kvalue[:len(kvalue)-len(rkv.ParentTxId)]
//										newtxid:=kvalue[len(kvalue)-len(rkv.ParentTxId):]
//
//										if strings.Compare(string(rkv.Value),newvalue)!=0{
//											logger.Infof("zs: txid:%s before commmit false rk:%s rv:%s newvalue:%s changed by txid:%s",
//												txId,rkv.Key,string(rkv.Value),newvalue,newtxid)
//											flagIsSameInKV=false
//											break
//										}
//									}
//									if flagIsSameInKV==true{
//
//
//
//										ch.GCon.GnodeMaxResults[txId].GnodeResult.State=1
//										ch.GCon.GNodeInBlock[txId].State=1
//										ch.GCon.GNodeInBlock[txId].IsCommitted=1
//										ch.GCon.GNodeInBlock[txId].committedGnode=ch.GCon.GnodeMaxResults[txId].GnodeResult
//
//										ch.GCon.GnodeMaxResults[txId].State=1
//										ch.GCon.GTxRelusts[txId].State=1
//
//										for _,rwset:=range ch.GCon.GnodeMaxResults[txId].GnodeResult.TxRWSet.NsRwSets{
//											logger.Infof("zs: gettx txid:%s namespace:%s ",txId,rwset.NameSpace)
//											if strings.Compare(rwset.NameSpace,"lscc")!=0{
//												for _,wkv:=range rwset.KvRwSet.Writes{
//													ch.GCon.GKVTable[wkv.Key]=string(wkv.Value)+txId
//													logger.Infof("zs: gettx txid:%s ch.GCon.GKVTable[%s]=%s",txId,wkv.Key,string(wkv.Value))
//												}
//											}
//										}
//
//
//
//										logger.Infof("zs: committed inblock success txid:%s txcount:%d",txId,txCount)
//										batches, pending := ch.support.BlockCutter().Ordered(ch.GCon.GnodeMaxResults[txId].GnodeResult.TxResult)
//										ch.GCon.NumSendInBlock++
//										for _, batch := range batches {
//											block := ch.support.CreateNextBlock(batch)
//											ch.support.WriteBlock(block, nil)
//										}
//
//										logger.Infof("zs: deleteSuccess be txid:%s txcount:%d",ch.GCon.GnodeMaxResults[txId].GnodeResult.TxId,ch.GCon.GnodeMaxResults[txId].GnodeResult.TxCount)
//										ch.DeleteGnodeForSuccess(ch.GCon.GnodeMaxResults[txId].GnodeResult)
//										logger.Infof("zs: deleteSuccess en txid:%s txcount:%d",ch.GCon.GnodeMaxResults[txId].GnodeResult.TxId,ch.GCon.GnodeMaxResults[txId].GnodeResult.TxCount)
//
//
//										gnodeResult:=ch.GCon.GTxRelusts[txId]
//										for gnodeResult!=nil {
//											if gnodeResult.GnodeResult.State==1{
//												logger.Infof("zs: deleteSuccess is already txid:%s txcount:%d",gnodeResult.GnodeResult.TxId,gnodeResult.GnodeResult.TxCount)
//											}else if gnodeResult.GnodeResult.State==2{
//												gnodeResult.GnodeResult.State=3
//												logger.Infof("zs: deleteFalseBecauseOtherSucc be txid:%s txcount:%d",gnodeResult.GnodeResult.TxId,gnodeResult.GnodeResult.TxCount)
//												ch.DeleteGnodeForFalse(gnodeResult.GnodeResult)
//												logger.Infof("zs: deleteFalseBecauseOtherSucc en txid:%s txcount:%d",gnodeResult.GnodeResult.TxId,gnodeResult.GnodeResult.TxCount)
//											}else{
//												logger.Infof("zs: deleteFalseBecauseOtherSucc already 3 txid:%s txcount:%d",gnodeResult.GnodeResult.TxId,gnodeResult.GnodeResult.TxCount)
//											}
//											gnodeResult=gnodeResult.Next
//										}
//
//
//
//										switch {
//										case timer != nil && !pending:
//											// Timer is already running but there are no messages pending, stop the timer
//											timer = nil
//										case timer == nil && pending:
//											// Timer is not already running and there are messages pending, so start it
//											timer = time.After(ch.support.SharedConfig().BatchTimeout())
//											logger.Debugf("Just began %s batch timer", ch.support.SharedConfig().BatchTimeout().String())
//										default:
//											// Do nothing when:
//											// 1. Timer is already running and there are messages pending
//											// 2. Timer is not set and there are no messages pending
//										}
//
//									}else{
//										//ch.GCon.GnodeMaxResults[txId].GnodeResult.State=3
//										logger.Infof("zs: txid:%s before commmit false newvalue changed ", txId)
//									}
//
//
//								}else{
//									logger.Infof("zs: gettx times:%d in block txid:%s txcount:%d success consenesus get %d votes with maxvotes: %d waitPtx PRWtx:%d POWtx:%d PORtx:%d",
//										ch.GCon.GTxWeight[txId],txId,txCount,txTimes,ch.GCon.GnodeMaxResults[txId].GnodeResult.Votes,
//										ch.GCon.GnodeMaxResults[txId].GnodeResult.NumParentsOfRW,
//										ch.GCon.GnodeMaxResults[txId].GnodeResult.NumParentsOfOW,
//										ch.GCon.GnodeMaxResults[txId].GnodeResult.NumParentsOfOR)
//								}
//
//
//							}
//
//
//
//
//
//
//
//
//
//
//						}else if txTimes==ch.GCon.NumExeNodes{
//							logger.Infof("zs: gettx times:%d in block txid:%s txcount:%d false consenesus get %d votes with maxvotes: %d gnodestate:%d",
//								ch.GCon.GTxWeight[txId],txId,txCount,txTimes,ch.GCon.GnodeMaxResults[txId].GnodeResult.Votes,ch.GCon.GnodeMaxResults[txId].GnodeResult.State)
//
//							ch.GCon.GNodeInBlock[txId].State=3
//							ch.GCon.GNodeInBlock[txId].IsCommitted=1
//							ch.GCon.GNodeInBlock[txId].committedGnode=nil
//
//							ch.GCon.GnodeMaxResults[txId].State=3
//							ch.GCon.GTxRelusts[txId].State=3
//
//
//
//
//							logger.Infof("zs: committed inblock false txid:%s txcount:%d",ch.GCon.GnodeMaxResults[txId].GnodeResult.TxId,ch.GCon.GnodeMaxResults[txId].GnodeResult.TxCount)
//							batches, pending := ch.support.BlockCutter().Ordered(ch.GCon.GnodeMaxResults[txId].GnodeResult.TxResult)
//							ch.GCon.NumSendInBlock++
//							for _, batch := range batches {
//								block := ch.support.CreateNextBlock(batch)
//								ch.support.WriteBlock(block, nil)
//							}
//
//
//							gnodeResult:=ch.GCon.GTxRelusts[txId]
//							for gnodeResult!=nil {
//								if gnodeResult.GnodeResult.State==1{
//									logger.Infof("zs: deleteFasle but state==1 be txid:%s txcount:%d ZSerror",gnodeResult.GnodeResult.TxId,gnodeResult.GnodeResult.TxCount)
//								}else if gnodeResult.GnodeResult.State==2{
//									gnodeResult.GnodeResult.State=3
//									logger.Infof("zs: deleteFalseBecauseFalse be txid:%s txcount:%d",gnodeResult.GnodeResult.TxId,gnodeResult.GnodeResult.TxCount)
//									ch.DeleteGnodeForFalse(gnodeResult.GnodeResult)
//									logger.Infof("zs: deleteFalseBecauseFalse en txid:%s txcount:%d",gnodeResult.GnodeResult.TxId,gnodeResult.GnodeResult.TxCount)
//								}else{
//									logger.Infof("zs: deleteFalseBecauseOtherFalse already 3 txid:%s txcount:%d",gnodeResult.GnodeResult.TxId,gnodeResult.GnodeResult.TxCount)
//								}
//								gnodeResult=gnodeResult.Next
//							}
//
//
//							switch {
//							case timer != nil && !pending:
//								// Timer is already running but there are no messages pending, stop the timer
//								timer = nil
//							case timer == nil && pending:
//								// Timer is not already running and there are messages pending, so start it
//								timer = time.After(ch.support.SharedConfig().BatchTimeout())
//								logger.Debugf("Just began %s batch timer", ch.support.SharedConfig().BatchTimeout().String())
//							default:
//								// Do nothing when:
//								// 1. Timer is already running and there are messages pending
//								// 2. Timer is not set and there are no messages pending
//							}
//						}
//
//					}else if txTimes==ch.GCon.NumExeNodes{
//
//						logger.Infof("zs: gettx times:%d in block txid:%s txcount:%d false consenesus get %d votes with maxvotes: 0 maxresult not exist ",
//							ch.GCon.GTxWeight[txId],txId,txCount,txTimes)
//
//						ch.GCon.GNodeInBlock[txId].State=3
//						ch.GCon.GNodeInBlock[txId].IsCommitted=1
//						ch.GCon.GNodeInBlock[txId].committedGnode=nil
//
//						_,isExistMaxr:=ch.GCon.GnodeMaxResults[txId]
//						if isExistMaxr==true{
//							ch.GCon.GnodeMaxResults[txId].State=3
//						}
//
//
//
//							logger.Infof("zs: committed inblock false withnoseist txid:%s txcount:%d",txId,txCount)
//							batches, pending := ch.support.BlockCutter().Ordered(gnode.TxResult)
//						ch.GCon.NumSendInBlock++
//							for _, batch := range batches {
//								block := ch.support.CreateNextBlock(batch)
//								ch.support.WriteBlock(block, nil)
//							}
//
//
//
//
//
//						gnodeResult,isExistResult:=ch.GCon.GTxRelusts[txId]
//						if isExistResult==true{
//							ch.GCon.GTxRelusts[txId].State=3
//							for gnodeResult!=nil {
//								if gnodeResult.GnodeResult.State==1{
//									logger.Infof("zs: deleteFasle maxresult not exist but state==1 be txid:%s txcount:%d ZSerror",gnodeResult.GnodeResult.TxId,gnodeResult.GnodeResult.TxCount)
//								}else if gnodeResult.GnodeResult.State==2{
//									gnodeResult.GnodeResult.State=3
//									logger.Infof("zs: deleteFalseBecauseFalse be maxresult not exist txid:%s txcount:%d",gnodeResult.GnodeResult.TxId,gnodeResult.GnodeResult.TxCount)
//									ch.DeleteGnodeForFalse(gnodeResult.GnodeResult)
//									logger.Infof("zs: deleteFalseBecauseFalse en maxresult not exist txid:%s txcount:%d",gnodeResult.GnodeResult.TxId,gnodeResult.GnodeResult.TxCount)
//								}else{
//									logger.Infof("zs: deleteFalseBecauseOtherFalse already 3 maxresult not exist txid:%s txcount:%d",gnodeResult.GnodeResult.TxId,gnodeResult.GnodeResult.TxCount)
//								}
//								gnodeResult=gnodeResult.Next
//							}
//						}else{
//							logger.Infof("zs: gettx times:%d in block txid:%s txcount:%d false consenesus get %d votes with maxvotes: 0 maxresult not exist ZSerror",
//								ch.GCon.GTxWeight[txId],txId,txCount,txTimes)
//						}
//
//							switch {
//							case timer != nil && !pending:
//								// Timer is already running but there are no messages pending, stop the timer
//								timer = nil
//							case timer == nil && pending:
//								// Timer is not already running and there are messages pending, so start it
//								timer = time.After(ch.support.SharedConfig().BatchTimeout())
//								logger.Debugf("Just began %s batch timer", ch.support.SharedConfig().BatchTimeout().String())
//							default:
//								// Do nothing when:
//								// 1. Timer is already running and there are messages pending
//								// 2. Timer is not set and there are no messages pending
//							}
//
//					}
//
//
//
//
//
//
//
//
//
//
//
//				}else if txState.State==1{
//					txTimes:=ch.GCon.GTxWeight[txId]
//					txTimes++
//					ch.GCon.GTxWeight[txId]=txTimes
//					logger.Infof("zs: gettx times:%d in block txid:%s txcount:%d is already committed",ch.GCon.GTxWeight[txId],txId,txCount)
//					if txTimes==ch.GCon.NumExeNodes{
//						ch.GCon.NumTxwithAllvotes++
//					}
//
//
//					txRWSet := &rwsetutil.TxRwSet{}
//					if err = txRWSet.FromProtoBytes(respPayload.Results); err != nil {
//						logger.Infof("zs: gettx txid:%s txcount:%d txRWSet.FromProtoBytes failed",txId,txCount)
//					}
//
//					gnode:=&GNode{
//						TxResult: 	env,
//						TxRWSet: txRWSet,
//						TxId:      txId,
//						TxCount:   txCount,
//						PeerId:    "",
//						Votes:  1,
//						ParentTxs: map[string]*ReadKV{},
//						ReadSet: map[string]*ReadKV{},
//						WriteSet: map[string]*WriteKV{},
//						State: 1,
//						NumParentsOfRW: 0,
//						NumParentsOfOW: 0,
//						NumParentsOfOR: 0,
//					}
//
//
//
//
//
//
//
//
//
//					txResult:=ch.GCon.GTxRelusts[gnode.TxId]
//					flagIsSame:=false
//					for txResult!=nil{
//						if ch.GnodeCompare(gnode,txResult.GnodeResult)==true{
//							flagIsSame=true
//							break
//						}
//						txResult=txResult.Next
//					}
//					if flagIsSame==true {
//						txResult.GnodeResult.Votes++
//
//						if txResult.GnodeResult.Votes == 2 {
//							ch.GCon.NumVotes2++
//						} else if txResult.GnodeResult.Votes == 3 {
//							ch.GCon.NumVotes3++
//						} else if txResult.GnodeResult.Votes == 4 {
//							ch.GCon.NumVotes4++
//						}
//					}
//
//
//
//					for _,rwset:=range txRWSet.NsRwSets{
//						logger.Infof("zs: gettx txid:%s namespace:%s ",txId,rwset.NameSpace)
//						if strings.Compare(rwset.NameSpace,"lscc")!=0{
//							for _,rkv:=range rwset.KvRwSet.Reads{
//								rkValue:=string(rkv.Value)
//								if strings.Compare(rkValue,"onlywrite")==0{
//
//									logger.Infof("zs: gettx txid:%s onlywrite rtxid:%s rtxcount:%d namespace:%s rk:%s rv:%s rtxid:%s",txId,rkv.Txid,rkv.Version.TxNum,rwset.NameSpace,rkv.Key,string(rkv.Value),rkv.Txid)
//								}else if strings.Compare(rkValue,"onlyread")==0{
//
//									logger.Infof("zs: gettx txid:%s onlyread rtxid:%s rtxcount:%d namespace:%s rk:%s rv:%s rtxid:%s",txId,rkv.Txid,rkv.Version.TxNum,rwset.NameSpace,rkv.Key,string(rkv.Value),rkv.Txid)
//								}else{
//									logger.Infof("zs: gettx txid:%s normal namespace:%s rk:%s rv:%s rtxid:%s",txId,rwset.NameSpace,rkv.Key,string(rkv.Value),rkv.Txid)
//								}
//							}
//
//							for _,wkv:=range rwset.KvRwSet.Writes{
//
//									logger.Infof("zs: gettx txid:%s normal namespace:%s wk:%s wv:%s InR",txId,rwset.NameSpace,wkv.Key,string(wkv.Value))
//
//
//							}
//
//
//						}
//					}
//
//
//
//					//logger.Infof("zs: committed inblock false withnoseist txid:%s txcount:%d",txId,txCount)
//					//batches, pending := ch.support.BlockCutter().Ordered(env)
//					//for _, batch := range batches {
//					//	block := ch.support.CreateNextBlock(batch)
//					//	ch.support.WriteBlock(block, nil)
//					//}
//					//switch {
//					//case timer != nil && !pending:
//					//	// Timer is already running but there are no messages pending, stop the timer
//					//	timer = nil
//					//case timer == nil && pending:
//					//	// Timer is not already running and there are messages pending, so start it
//					//	timer = time.After(ch.support.SharedConfig().BatchTimeout())
//					//	logger.Debugf("Just began %s batch timer", ch.support.SharedConfig().BatchTimeout().String())
//					//default:
//					//	// Do nothing when:
//					//	// 1. Timer is already running and there are messages pending
//					//	// 2. Timer is not set and there are no messages pending
//					//}
//				}else{
//					txTimes:=ch.GCon.GTxWeight[txId]
//					txTimes++
//					ch.GCon.GTxWeight[txId]=txTimes
//					logger.Infof("zs: gettx times:%d in block txid:%s txcount:%d is already false",ch.GCon.GTxWeight[txId],txId,txCount)
//
//					if txTimes==ch.GCon.NumExeNodes{
//						ch.GCon.NumTxwithAllvotes++
//					}
//
//
//					txRWSet := &rwsetutil.TxRwSet{}
//					if err = txRWSet.FromProtoBytes(respPayload.Results); err != nil {
//						logger.Infof("zs: gettx txid:%s txcount:%d txRWSet.FromProtoBytes failed",txId,txCount)
//					}
//
//
//
//
//					gnode:=&GNode{
//						TxResult: 	env,
//						TxRWSet: txRWSet,
//						TxId:      txId,
//						TxCount:   txCount,
//						PeerId:    "",
//						Votes:  1,
//						ParentTxs: map[string]*ReadKV{},
//						ReadSet: map[string]*ReadKV{},
//						WriteSet: map[string]*WriteKV{},
//						State: 1,
//						NumParentsOfRW: 0,
//						NumParentsOfOW: 0,
//						NumParentsOfOR: 0,
//					}
//
//
//
//
//
//
//
//
//
//					txResult:=ch.GCon.GTxRelusts[gnode.TxId]
//					flagIsSame:=false
//					for txResult!=nil{
//						if ch.GnodeCompare(gnode,txResult.GnodeResult)==true{
//							flagIsSame=true
//							break
//						}
//						txResult=txResult.Next
//					}
//					if flagIsSame==true {
//						txResult.GnodeResult.Votes++
//
//						if txResult.GnodeResult.Votes == 2 {
//							ch.GCon.NumVotes2++
//						} else if txResult.GnodeResult.Votes == 3 {
//							ch.GCon.NumVotes3++
//						} else if txResult.GnodeResult.Votes == 4 {
//							ch.GCon.NumVotes4++
//						}
//					}
//
//
//
//					for _,rwset:=range txRWSet.NsRwSets{
//						logger.Infof("zs: gettx txid:%s namespace:%s ",txId,rwset.NameSpace)
//						if strings.Compare(rwset.NameSpace,"lscc")!=0{
//							for _,rkv:=range rwset.KvRwSet.Reads{
//								rkValue:=string(rkv.Value)
//								if strings.Compare(rkValue,"onlywrite")==0{
//
//									logger.Infof("zs: gettx txid:%s onlywrite rtxid:%s rtxcount:%d namespace:%s rk:%s rv:%s rtxid:%s",txId,rkv.Txid,rkv.Version.TxNum,rwset.NameSpace,rkv.Key,string(rkv.Value),rkv.Txid)
//								}else if strings.Compare(rkValue,"onlyread")==0{
//
//									logger.Infof("zs: gettx txid:%s onlyread rtxid:%s rtxcount:%d namespace:%s rk:%s rv:%s rtxid:%s",txId,rkv.Txid,rkv.Version.TxNum,rwset.NameSpace,rkv.Key,string(rkv.Value),rkv.Txid)
//								}else{
//									logger.Infof("zs: gettx txid:%s normal namespace:%s rk:%s rv:%s rtxid:%s",txId,rwset.NameSpace,rkv.Key,string(rkv.Value),rkv.Txid)
//								}
//							}
//
//							for _,wkv:=range rwset.KvRwSet.Writes{
//
//								logger.Infof("zs: gettx txid:%s normal namespace:%s wk:%s wv:%s InR",txId,rwset.NameSpace,wkv.Key,string(wkv.Value))
//
//
//							}
//
//
//						}
//					}
//
//					if txTimes==ch.GCon.NumExeNodes && ch.GCon.GNodeInBlock[txId].IsCommitted==0{
//						ch.GCon.GNodeInBlock[txId].IsCommitted=1
//						logger.Infof("zs: committed inblock false already==3 withnoseist txid:%s txcount:%d",txId,txCount)
//						batches, pending := ch.support.BlockCutter().Ordered(env)
//						ch.GCon.NumSendInBlock++
//						for _, batch := range batches {
//							block := ch.support.CreateNextBlock(batch)
//							ch.support.WriteBlock(block, nil)
//						}
//						switch {
//						case timer != nil && !pending:
//							// Timer is already running but there are no messages pending, stop the timer
//							timer = nil
//						case timer == nil && pending:
//							// Timer is not already running and there are messages pending, so start it
//							timer = time.After(ch.support.SharedConfig().BatchTimeout())
//							logger.Debugf("Just began %s batch timer", ch.support.SharedConfig().BatchTimeout().String())
//						default:
//							// Do nothing when:
//							// 1. Timer is already running and there are messages pending
//							// 2. Timer is not set and there are no messages pending
//						}
//					}
//
//
//				}
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//
//			} else {
//				logger.Infof("zs: local SOLO.main()  Ordered ConfigMsg")
//				// ConfigMsg
//				if msg.configSeq < seq {
//					msg.configMsg, _, err = ch.support.ProcessConfigMsg(msg.configMsg)
//					if err != nil {
//						logger.Warningf("Discarding bad config message: %s", err)
//						continue
//					}
//				}
//				batch := ch.support.BlockCutter().Cut()
//				if batch != nil {
//					block := ch.support.CreateNextBlock(batch)
//					ch.support.WriteBlock(block, nil)
//				}
//
//				block := ch.support.CreateNextBlock([]*cb.Envelope{msg.configMsg})
//				ch.support.WriteConfigBlock(block, nil)
//				timer = nil
//			}
//		case <-timer:
//			//clear the timer
//			timer = nil
//
//			batch := ch.support.BlockCutter().Cut()
//			if len(batch) == 0 {
//				logger.Warningf("Batch timer expired with no pending requests, this might indicate a bug")
//				continue
//			}
//			logger.Debugf("Batch timer expired, creating block")
//			block := ch.support.CreateNextBlock(batch)
//			ch.support.WriteBlock(block, nil)
//		case <-ch.exitChan:
//			logger.Debugf("Exiting")
//			return
//		}
//	}
//}
//
//
//
//
////package local
////import (
////	"fmt"
////	"time"
////
////	"github.com/hyperledger/fabric/common/flogging"
////	"github.com/hyperledger/fabric/orderer/consensus"
////	cb "github.com/hyperledger/fabric/protos/common"
////)
////
////var logger = flogging.MustGetLogger("orderer.consensus.local")
////
////type consenter struct{}
////
////type chain struct {
////	support  consensus.ConsenterSupport
////	sendChan chan *message
////	exitChan chan struct{}
////}
////
////type message struct {
////	configSeq uint64
////	normalMsg *cb.Envelope
////	configMsg *cb.Envelope
////}
////
////// New creates a new consenter for the solo consensus scheme.
////// The solo consensus scheme is very simple, and allows only one consenter for a given chain (this process).
////// It accepts messages being delivered via Order/Configure, orders them, and then uses the blockcutter to form the messages
////// into blocks before writing to the given ledger
////func New() consensus.Consenter {
////	return &consenter{}
////}
////
////func (solo *consenter) HandleChain(support consensus.ConsenterSupport, metadata *cb.Metadata) (consensus.Chain, error) {
////	return newChain(support), nil
////}
////
//////func newChain(support consensus.ConsenterSupport) *chain {
//////	return &chain{
//////		support:  support,
//////		sendChan: make(chan *message,1000),
//////		exitChan: make(chan struct{}),
//////	}
//////}
////func newChain(support consensus.ConsenterSupport) *chain {
////	return &chain{
////		support:  support,
////		sendChan: make(chan *message,8000),
////		exitChan: make(chan struct{}),
////	}
////}
////
////func (ch *chain) Start() {
////	go ch.main()
////}
////
////func (ch *chain) Halt() {
////	select {
////	case <-ch.exitChan:
////		// Allow multiple halts without panic
////	default:
////		close(ch.exitChan)
////	}
////}
////
////func (ch *chain) WaitReady() error {
////	return nil
////}
////
////// Order accepts normal messages for ordering
////func (ch *chain) Order(env *cb.Envelope, configSeq uint64) error {
////	logger.Infof("zs: LocalOrder SOLO.main() before Ordered msg")
////	select {
////	case ch.sendChan <- &message{
////		configSeq: 0,
////		normalMsg: env,
////	}:
////		logger.Infof("zs: LocalOrder SOLO.main() after Ordered msg")
////		return nil
////	case <-ch.exitChan:
////		logger.Infof("zs: LocalOrder SOLO.main() after Ordered msg exit")
////		return fmt.Errorf("Exiting")
////	}
////}
////// Configure accepts configuration update messages for ordering
////func (ch *chain) Configure(config *cb.Envelope, configSeq uint64) error {
////	select {
////	case ch.sendChan <- &message{
////		configSeq: configSeq,
////		configMsg: config,
////	}:
////		return nil
////	case <-ch.exitChan:
////		return fmt.Errorf("Exiting")
////	}
////}
////
////// Errored only closes on exit
////func (ch *chain) Errored() <-chan struct{} {
////	return ch.exitChan
////}
////
////func (ch *chain) main() {
////	var timer <-chan time.Time
////	var err error
////
////	for {
////		seq := ch.support.Sequence()
////		err = nil
////		select {
////		case msg := <-ch.sendChan:
////			if msg.configMsg == nil {
////				// NormalMsg
////				//if msg.configSeq < seq {
////				//	_, err = ch.support.ProcessNormalMsg(msg.normalMsg)
////				//	if err != nil {
////				//		logger.Warningf("Discarding bad normal message: %s", err)
////				//		continue
////				//	}
////				//}
////				logger.Infof("zs: local SOLO.main() before Ordered msg")
////				batches, pending := ch.support.BlockCutter().Ordered(msg.normalMsg)
////				logger.Infof("zs: local SOLO.main() after Ordered msg")
////				for _, batch := range batches {
////					block := ch.support.CreateNextBlock(batch)
////					ch.support.WriteBlock(block, nil)
////				}
////
////				switch {
////				case timer != nil && !pending:
////					// Timer is already running but there are no messages pending, stop the timer
////					timer = nil
////				case timer == nil && pending:
////					// Timer is not already running and there are messages pending, so start it
////					timer = time.After(ch.support.SharedConfig().BatchTimeout())
////					logger.Debugf("Just began %s batch timer", ch.support.SharedConfig().BatchTimeout().String())
////				default:
////					// Do nothing when:
////					// 1. Timer is already running and there are messages pending
////					// 2. Timer is not set and there are no messages pending
////				}
////
////			} else {
////				logger.Infof("zs: local SOLO.main()  Ordered ConfigMsg")
////				// ConfigMsg
////				if msg.configSeq < seq {
////					msg.configMsg, _, err = ch.support.ProcessConfigMsg(msg.configMsg)
////					if err != nil {
////						logger.Warningf("Discarding bad config message: %s", err)
////						continue
////					}
////				}
////				batch := ch.support.BlockCutter().Cut()
////				if batch != nil {
////					block := ch.support.CreateNextBlock(batch)
////					ch.support.WriteBlock(block, nil)
////				}
////
////				block := ch.support.CreateNextBlock([]*cb.Envelope{msg.configMsg})
////				ch.support.WriteConfigBlock(block, nil)
////				timer = nil
////			}
////		case <-timer:
////			//clear the timer
////			timer = nil
////
////			batch := ch.support.BlockCutter().Cut()
////			if len(batch) == 0 {
////				logger.Warningf("Batch timer expired with no pending requests, this might indicate a bug")
////				continue
////			}
////			logger.Debugf("Batch timer expired, creating block")
////			block := ch.support.CreateNextBlock(batch)
////			ch.support.WriteBlock(block, nil)
////		case <-ch.exitChan:
////			logger.Debugf("Exiting")
////			return
////		}
////	}
////}
////
