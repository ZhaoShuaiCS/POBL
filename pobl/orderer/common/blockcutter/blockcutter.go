/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package blockcutter

import (
	"time"
	"strings"

	"github.com/hyperledger/fabric/common/channelconfig"
	"github.com/hyperledger/fabric/common/flogging"
	cb "github.com/hyperledger/fabric/protos/common"
	"github.com/hyperledger/fabric/orderer/common/partialorder"
)

var logger = flogging.MustGetLogger("orderer.common.blockcutter")

type OrdererConfigFetcher interface {
	OrdererConfig() (channelconfig.Orderer, bool)
}

// Receiver defines a sink for the ordered broadcast messages
type Receiver interface {
	// Ordered should be invoked sequentially as messages are ordered
	// Each batch in `messageBatches` will be wrapped into a block.
	// `pending` indicates if there are still messages pending in the receiver.
	Ordered(msg *cb.Envelope) (messageBatches [][]*cb.Envelope, pending bool)
	OrderedRaft(gvnode *partialorder.GVNode ) (messageBatches [][]*cb.Envelope, pending bool)

	// Cut returns the current batch and starts a new one
	Cut() []*cb.Envelope
	//GetRaftGVNodeMsgC() chan *partialorder.GVNodeMsg
	GetRaftEnvMsgC() chan *partialorder.EnvMsg
}

type receiver struct {
	sharedConfigFetcher   OrdererConfigFetcher
	pendingBatch          []*cb.Envelope
	pendingBatchSizeBytes uint32

	PendingBatchStartTime time.Time
	ChannelID             string
	Metrics               *Metrics
	TCG         *partialorder.TxConsensusGraph
	NumCount int64
	//RaftGVNodeMsgC chan *partialorder.GVNodeMsg
	RaftEnvMsgC chan *partialorder.EnvMsg

	ZSBetime time.Time
	ZSEntime time.Time
	Timecount int64
	Txcount int64
	ProcessTi time.Duration
	LastProcessTi time.Duration
	ProcessBlock time.Duration
}



func NewReceiverImpl(channelID string, sharedConfigFetcher OrdererConfigFetcher, metrics *Metrics) Receiver {
	tempr:=&receiver{
		sharedConfigFetcher: sharedConfigFetcher,
		Metrics:             metrics,
		ChannelID:           channelID,
		TCG: &partialorder.TxConsensusGraph{
			GState: make(map[string]string),
			TxCNs: make(map[string]*partialorder.TxConsensusNode),
			CommittedTxCNs: make(map[string]*partialorder.TxConsensusNode),
			WaitUnChoosedTxCNs: make(map[string]*partialorder.WaitTxConsensusNode),
			Root: & partialorder.TxConsensusNode{
				TxId: "Root",
				ChoosedNode:nil,
				ParentNum: 0,
				ParentTxConsensusNode:nil,
				UNParentTxConsensusNode: nil,
				NChildNodes: &[]*partialorder.TxConsensusNode{},
				UNChildNodes: &[]*partialorder.TxConsensusNode{},
				WaitPTX: nil,
				State:2,//1.committed;2.ready;3.wait;4.changedstate
			},
			NumGCommittedNode: 0,
			NumChangedState:0,
			ReadyBatch: &[]*cb.Envelope{},
		},
		NumCount:0,
		//RaftGVNodeMsgC: make(chan *partialorder.GVNodeMsg,10000),
		RaftEnvMsgC:make(chan *partialorder.EnvMsg,10000),
		ZSBetime:time.Now(),
		ZSEntime:time.Now(),
		Timecount:0,
		Txcount :0,
	}
	//go tempr.ProcessGVNodeMsg()
	go tempr.ProcessEnvMsg()
	return tempr
}
//zs0513
// func (r *receiver) GetRaftGVNodeMsgC() chan *partialorder.GVNodeMsg {
// 	return r.RaftGVNodeMsgC
// }
func (r *receiver) GetRaftEnvMsgC() chan *partialorder.EnvMsg {
	return r.RaftEnvMsgC
}
func (r *receiver) ProcessEnvMsg() {
	// var cbetime time.Time
	// txnumcount:=0
	// lastcount:=0
	logger.Infof("zs:ProcessEnvMsg BE\n")
	for{
		envmsg:= <- r.RaftEnvMsgC



		if envmsg.Env!=nil{
			batches,_:=r.OrderedRaftForBC(envmsg.Env)
			logger.Infof("zs:ProcessEnvMsg len:%d\n",len(batches))
			if len(batches)>0{
				logger.Infof("zs:ProcessEnvMsg gvnodemsg.CommitC send batches len:%d\n",len(batches))
				envmsg.CommitC <-batches
			}
		}else{
			logger.Infof("zs:ProcessEnvMsg CUT time BE\n")
			batches:= [][]*cb.Envelope{}
			batch := r.Cut()
			if len(batch)>0{
				batches = append(batches, batch)
				envmsg.CommitC <-batches
				logger.Infof("zs:ProcessEnvMsg CUT success time EN len:%d\n",len(batch))
			}else{
				logger.Infof("zs:ProcessEnvMsg CUT not exist time EN len:%d\n",len(batch))
			}
		}


	}
	//logger.Infof("zs:ProcessEnvMsg EN\n")
}
func (r *receiver) OrderedRaftForBC(msg *cb.Envelope ) ( [][]*cb.Envelope, bool) {
	var pending bool
	messageBatches:= [][]*cb.Envelope{}
	txCN:=&partialorder.TxConsensusNode{
		TxId: "",
		ChoosedNode: nil,
		ParentNum: 0,
		ParentTxConsensusNode: make(map[string]bool),
		UNParentTxConsensusNode: make(map[string]bool),
		NChildNodes: &[]*partialorder.TxConsensusNode{},
		UNChildNodes: &[]*partialorder.TxConsensusNode{},
		State:3, //1.committed;2.ready;3.wait;4.changedstate
		WaitPTX: make(map[string]bool),
	}
	beti:=time.Now()
	txCN.ProcessEnvForBC(msg)
	r.ProcessTX(txCN)

	enti:=time.Now()
	if r.Txcount==0{
		r.ProcessTi=enti.Sub(beti)
	}else{
		r.ProcessTi+=enti.Sub(beti)
	}
	r.Txcount++


	txid:=txCN.TxId
	if len(r.pendingBatch) == 0 {
		// We are beginning a new batch, mark the time
		r.PendingBatchStartTime = time.Now()
	}

	ordererConfig, ok := r.sharedConfigFetcher.OrdererConfig()
	if !ok {
		logger.Panicf("Could not retrieve orderer config to query batch parameters, block cutting is not possible")
	}

	batchSize := ordererConfig.BatchSize()
	
	pending = true
	
	//r.NumCount++
	
	if uint32(len(*r.TCG.ReadyBatch)) >= batchSize.MaxMessageCount {
		logger.Debugf("Batch size met, cutting batch")
		//logger.Infof("zs:CUT Size\n")
		logger.Infof("zs:CUT Size  txid:%s lenReadyBatch:%d  BE\n",txid,len(*r.TCG.ReadyBatch))
		messageBatch := r.Cut()
		messageBatches = append(messageBatches, messageBatch)
		pending = false
		logger.Infof("zs:CUT Size  txid:%s lenReadyBatch:%d lenes:%d len:%d EN\n",txid,len(*r.TCG.ReadyBatch),len(messageBatches),len(messageBatch))
	}else{
		logger.Infof("zs:CUT Size  txid:%s lenReadyBatch:%d  BE\n",txid,len(*r.TCG.ReadyBatch))
		//if len(r.TCG.Root.WRChildNodes) >0 || len(r.TCG.Root.WWChildNodes) >0||len(r.TCG.Root.WRWWChildNodes) >0{
		if len(*r.TCG.ReadyBatch) >0{
			pending=true
		}else{
			pending = false
		}
	}

	logger.Infof("zs:ProcessTX txid:%s lenReadyBatch:%d  batchSize:%d pend:%v\n",
	txCN.ChoosedNode.TxId,len(*r.TCG.ReadyBatch),batchSize.MaxMessageCount,pending)







	return messageBatches,pending
}




func (r *receiver) ProcessTX(txCN *partialorder.TxConsensusNode ) {
	
	txid:=txCN.ChoosedNode.TxId
	//r.TCG.TxCNs[txid]=txCN
	logger.Infof("zs:ProcessTX txid:%s BE lenTxCNs:%d count:%d ConsensusState==%d\n",
	txid,len(r.TCG.TxCNs),txCN.ChoosedNode.Count,txCN.ChoosedNode.ConsensusState)

	if txCN.ChoosedNode.ConsensusState==3{
		logger.Infof("zs:ERROR-1 ProcessTX txid:%s txCN.ChoosedNode.ConsensusState==%d\n",
		txid,txCN.ChoosedNode.ConsensusState)
		txCN.State=4
	}
	waittxcn,isExistwaittxcn:=r.TCG.WaitUnChoosedTxCNs[txid]
	if isExistwaittxcn==true && waittxcn.DiffWaitWKV==true{
		logger.Infof("zs:ERROR-2 ProcessTX txid:%s txCN.State==%d waittxcn.DiffWaitWKV==%v\n",
		txid,txCN.State,waittxcn.DiffWaitWKV)
		txCN.State=4
	}

	if isExistwaittxcn==false{
		if txCN.ChoosedNode.ConsensusState==3{
			r.TCG.WaitUnChoosedTxCNs[txid]=&partialorder.WaitTxConsensusNode{
				ChildNum:0,
				NChildNodes: &[]*partialorder.TxConsensusNode{},
				UNChildNodes: &[]*partialorder.TxConsensusNode{},
				WriteSet: make(map[string]string),
				DiffWaitWKV: true,
				ChoosedNode: txCN,
				ChildWait: make(map[string]*partialorder.TxConsensusNode),
			}
		}

	}






	if txCN.State!=4 && isExistwaittxcn==true{
		for waitwk,waitwkvalue:=range waittxcn.WriteSet{
			wkv,isExistwkv:=txCN.ChoosedNode.WriteSet[waitwk]
			if isExistwkv==false || strings.Compare(waitwkvalue,wkv.Value)!=0{
				txCN.State=4
				waittxcn.DiffWaitWKV=true
				if isExistwkv==false{
					logger.Infof("zs:ERROR-3 ProcessTX txid:%s txCN.State==%d waittxcn.DiffWaitWKV==%v  waitk:%s waitkv:%s not exist in tx\n",
					txid,txCN.State,waittxcn.DiffWaitWKV,waitwk,waitwkvalue)
				}else{
					logger.Infof("zs:ERROR-3 ProcessTX txid:%s txCN.State==%d waittxcn.DiffWaitWKV==%v  waitk:%s waitkv:%s in tx wkv:%s\n",
					txid,txCN.State,waittxcn.DiffWaitWKV,waitwk,waitwkvalue,wkv.Value)
				}
				break
			}
		}
	}


	if txCN.State!=4 && txCN.ChoosedNode.ConsensusState!=3{
		
		if txCN.State!=4 && txCN.ChoosedNode.ConsensusState==2{
			for ptxid,ptx:=range txCN.ChoosedNode.PTx{
				for ptxk,ptxv:=range ptx.KV{
					logger.Infof("zs:ProcessTX CState:%d BEFORE txid:%s ptxid:%s ptxk:%s ptxv:%s ptxtype:%d \n",txCN.ChoosedNode.ConsensusState,txid,ptxid,ptxk,ptxv,ptx.Type[ptxk])
				}
			}
			for ptxid,ptx:=range txCN.ChoosedNode.UNPTx{
				for ptxk,ptxv:=range ptx.KV{
					logger.Infof("zs:ProcessTX CState:%d BEFORE txid:%s unptxid:%s unptxk:%s unptxv:%s unptxtype:%d \n",txCN.ChoosedNode.ConsensusState,txid,ptxid,ptxk,ptxv,ptx.Type[ptxk])
				}
			}
			logger.Infof("zs:ProcessTX txid:%s ConsensusState==2 RKVPTX  \n",txid)
			tempptx:=make(map[string]*partialorder.PtxKV)
			Intemp:=make(map[string]bool)
			for ptxid,ptx:=range txCN.ChoosedNode.PTx{
				ptxcn,isExistptxcn:=r.TCG.TxCNs[ptxid]
				if isExistptxcn==false{
					logger.Infof("zs:ERROR-4 ProcessTX txid:%s  ptxid:%s  ptxcn,isExistptxcn:=r.TCG.TxCNs[] isExistptxcn==false\n",txid,ptxid)
				}else{
					for ptxk,ptxv:=range ptx.KV{
						_,intemp:=Intemp[ptxk]
						if intemp==false{
							
							ptxwkv,isExistptxwkv:= ptxcn.ChoosedNode.WriteSet[ptxk]
							if isExistptxwkv==true && strings.Compare(ptxwkv.Value,ptxv)==0{
								logger.Infof("zs:ProcessTX txid:%s  ptxid:%s  wantk:%s wantv:%s realv:%s SUCCESS\n",txid,ptxid,ptxk,ptxv,ptxwkv.Value)
								Intemp[ptxk]=true
								tptx,istptx:=tempptx[ptxid]
								if istptx==false{
									tempptx[ptxid]=&partialorder.PtxKV{
										KV: make(map[string]string),
										Type: make(map[string]int64), 
									}
									tptx=tempptx[ptxid]
								}
								tptx.KV[ptxk]=ptxv
								tptx.Type[ptxk]=ptx.Type[ptxk]
							}else{
								if isExistptxwkv==false{
									logger.Infof("zs:ProcessTX txid:%s  ptxid:%s  wantk:%s wantv:%s realv: notexiset\n",txid,ptxid,ptxk,ptxv,ptxwkv.Value)
								}else{
									logger.Infof("zs:ProcessTX txid:%s  ptxid:%s  wantk:%s wantv:%s realv:%s not match\n",txid,ptxid,ptxk,ptxv,ptxwkv.Value)
								}
								
							}
						}
					}
				}
			}
			if len(Intemp)<len(txCN.ChoosedNode.ReadSet){
				logger.Infof("zs:ERROR-5 ProcessTX txid:%s ConsensusState==2 false in intemp:%d RKV:%d\n",
				txid,len(Intemp),len(txCN.ChoosedNode.ReadSet))
				r.TCG.NumChangedState++
				txCN.State=4

				_,isExistwaitTxcn:=r.TCG.WaitUnChoosedTxCNs[txid]
				if isExistwaitTxcn==false{
					r.TCG.WaitUnChoosedTxCNs[txid]=&partialorder.WaitTxConsensusNode{
						ChildNum:0,
						NChildNodes: &[]*partialorder.TxConsensusNode{},
						UNChildNodes: &[]*partialorder.TxConsensusNode{},
						WriteSet: make(map[string]string),
						DiffWaitWKV: true,
						ChoosedNode: txCN,
						ChildWait: make(map[string]*partialorder.TxConsensusNode),
					}
					//waitTxcn=r.TCG.WaitUnChoosedTxCNs[txid]
				}




			}else{
				txCN.ChoosedNode.PTx=tempptx
			}
		}
		for ptxid,ptx:=range txCN.ChoosedNode.PTx{
			for ptxk,ptxv:=range ptx.KV{
				logger.Infof("zs:ProcessTX CState:%d END txid:%s ptxid:%s ptxk:%s ptxv:%s ptxtype:%d \n",txCN.ChoosedNode.ConsensusState,txid,ptxid,ptxk,ptxv,ptx.Type[ptxk])
			}
		}
		for ptxid,ptx:=range txCN.ChoosedNode.UNPTx{
			for ptxk,ptxv:=range ptx.KV{
				logger.Infof("zs:ProcessTX CState:%d END txid:%s unptxid:%s unptxk:%s unptxv:%s unptxtype:%d \n",txCN.ChoosedNode.ConsensusState,txid,ptxid,ptxk,ptxv,ptx.Type[ptxk])
			}
		}
		if txCN.State!=4{
			r.TCG.TxCNs[txid]=txCN
			for ptxid,ptx:=range txCN.ChoosedNode.PTx{
				ptxcn,isExistptxcn:=r.TCG.TxCNs[ptxid]
				if isExistptxcn==false{
					
					waitTxcn,isExistwaitTxcn:=r.TCG.WaitUnChoosedTxCNs[ptxid]
					if isExistwaitTxcn==false{
						r.TCG.WaitUnChoosedTxCNs[ptxid]=&partialorder.WaitTxConsensusNode{
							ChildNum:0,
							NChildNodes: &[]*partialorder.TxConsensusNode{},
							UNChildNodes: &[]*partialorder.TxConsensusNode{},
							WriteSet: make(map[string]string),
							DiffWaitWKV: false,
							ChoosedNode: nil,
							ChildWait: make(map[string]*partialorder.TxConsensusNode),
						}
						waitTxcn=r.TCG.WaitUnChoosedTxCNs[ptxid]
					}
					waitTxcn.ChildNum++
					waitTxcn.ChildWait[txid]=txCN
					txCN.WaitPTX[ptxid]=true
					if waitTxcn.ChoosedNode==nil{
						logger.Infof("zs: ProcessTX txid:%s  ptx:%s r.TCG.WaitUnChoosedTxCNs nil\n",txid,ptxid)
						*waitTxcn.NChildNodes=append(*waitTxcn.NChildNodes,txCN)
						txCN.ParentTxConsensusNode[ptxid]=true
						txCN.ParentNum++
						for ptxk,ptxv:=range ptx.KV{
							waitTxcnValue,isExistwaitTxcnValue:=waitTxcn.WriteSet[ptxk]
							if isExistwaitTxcnValue==false{
								waitTxcn.WriteSet[ptxk]=ptxv
							}else if strings.Compare(ptxv,waitTxcnValue)!=0{
								waitTxcn.DiffWaitWKV=true
								logger.Infof("zs:ERROR-6 ProcessTX txid:%s rk:%s rv:%s ptx:%s ptx-k:%s ptx-v:%s\n",
								txid,ptxk,ptxv,ptxid,ptxk,waitTxcnValue)
								break
							}
						}	
					}else{
						logger.Infof("zs:ERROR-7 ProcessTX txid:%s  ptx:%s r.TCG.WaitUnChoosedTxCNs already false\n",txid,ptxid)
						r.TCG.NumChangedState++
						txCN.State=4
						break
					}
				}else{
					for ptxk,ptxv:=range ptx.KV{
						ptxValue,isExistptxValue:=ptxcn.ChoosedNode.WriteSet[ptxk]
						if isExistptxValue==false || strings.Compare(ptxv,ptxValue.Value)!=0{
							if isExistptxValue==false{
								logger.Infof("zs:ERROR-8 ProcessTX txid:%s rk:%s rv:%s ptx:%s ptx-k:%s not exist\n",
								txid,ptxk,ptxv,ptxid,ptxk)
							}else{
								logger.Infof("zs:ERROR-8 ProcessTX txid:%s rk:%s rv:%s ptx:%s ptx-k:%s ptx-v:%s\n",
								txid,ptxk,ptxv,ptxid,ptxk,ptxValue.Value)
							}
							r.TCG.NumChangedState++
							txCN.State=4
							_,isExistwaitTxcn:=r.TCG.WaitUnChoosedTxCNs[txid]
							if isExistwaitTxcn==false{
								r.TCG.WaitUnChoosedTxCNs[txid]=&partialorder.WaitTxConsensusNode{
									ChildNum:0,
									NChildNodes: &[]*partialorder.TxConsensusNode{},
									UNChildNodes: &[]*partialorder.TxConsensusNode{},
									WriteSet: make(map[string]string),
									DiffWaitWKV: true,
									ChoosedNode: txCN,
									ChildWait: make(map[string]*partialorder.TxConsensusNode),
								}
								//waitTxcn=r.TCG.WaitUnChoosedTxCNs[txid]
							}
							delete(r.TCG.TxCNs,txid)
							break
						}
					}
					if ptxcn.State==1{
						for ptxk,ptxv:=range ptx.KV{
							logger.Infof("zs: ProcessTX txid:%s rk:%s rv:%s ptx:%s is committed\n",txid,ptxk,ptxv,ptxid)
							gv:=r.TCG.GState[ptxk]
							if ptx.Type[ptxk]!=2 && strings.Compare(gv,ptxv)!=0{
								logger.Infof("zs:ERROR-9 ProcessTX txid:%s rk:%s rv:%s ptx:%s ptx-k:%s gv:%s\n",
									txid,ptxk,ptxv,ptxid,ptxk,gv)
								r.TCG.NumChangedState++
								txCN.State=4
								break
							}
						}
						if txCN.State==4{
							_,isExistwaitTxcn:=r.TCG.WaitUnChoosedTxCNs[txid]
							if isExistwaitTxcn==false{
								r.TCG.WaitUnChoosedTxCNs[txid]=&partialorder.WaitTxConsensusNode{
									ChildNum:0,
									NChildNodes: &[]*partialorder.TxConsensusNode{},
									UNChildNodes: &[]*partialorder.TxConsensusNode{},
									WriteSet: make(map[string]string),
									DiffWaitWKV: true,
									ChoosedNode: txCN,
									ChildWait: make(map[string]*partialorder.TxConsensusNode),
								}
								//waitTxcn=r.TCG.WaitUnChoosedTxCNs[txid]
							}
							delete(r.TCG.TxCNs,txid)

							break
						}
					}else if ptxcn.State==2{
						logger.Infof("zs:ERROR-10 ProcessTX txid:%s ptx:%s ptxcn.State==2\n",txid,ptxid)
					}else if ptxcn.State==3{
						logger.Infof("zs: ProcessTX txid:%s ptx:%s is Wait\n",txid,ptxid)
						*ptxcn.NChildNodes=append(*ptxcn.NChildNodes,txCN)
						txCN.ParentTxConsensusNode[ptxid]=true
						txCN.ParentNum++
						for waitptx,_:=range ptxcn.WaitPTX{
							txCN.WaitPTX[waitptx]=true
							r.TCG.WaitUnChoosedTxCNs[waitptx].ChildWait[txid]=txCN
						}
					}else if ptxcn.State==4{
						logger.Infof("zs:ERROR-11 ProcessTX txid:%s ptx:%s ptxcn.State==4 is Error\n",txid,ptxid)
						r.TCG.NumChangedState++
						txCN.State=4
						for waitptx,_:=range ptxcn.WaitPTX{
							txCN.WaitPTX[waitptx]=true
							r.TCG.WaitUnChoosedTxCNs[waitptx].ChildWait[txid]=txCN
						}
						break
					}
				}
			}
			if txCN.State!=4{
				for ptxid,_:=range txCN.ChoosedNode.UNPTx{
					ptxcn,isExistptxcn:=r.TCG.TxCNs[ptxid]
					if isExistptxcn==false{
						
						waitTxcn,isExistwaitTxcn:=r.TCG.WaitUnChoosedTxCNs[ptxid]
						if isExistwaitTxcn==false{
							r.TCG.WaitUnChoosedTxCNs[ptxid]=&partialorder.WaitTxConsensusNode{
								ChildNum:0,
								NChildNodes: &[]*partialorder.TxConsensusNode{},
								UNChildNodes: &[]*partialorder.TxConsensusNode{},
								WriteSet: make(map[string]string),
								DiffWaitWKV: false,
								ChoosedNode: nil,
								ChildWait: make(map[string]*partialorder.TxConsensusNode),
							}
							waitTxcn=r.TCG.WaitUnChoosedTxCNs[ptxid]
						}


	


						if waitTxcn.ChoosedNode==nil{
							logger.Infof("zs: ProcessTX txid:%s  UNptx:%s r.TCG.WaitUnChoosedTxCNs\n",txid,ptxid)
							*waitTxcn.UNChildNodes=append(*waitTxcn.UNChildNodes,txCN)
							txCN.UNParentTxConsensusNode[ptxid]=true
							txCN.ParentNum++
						}else{
							logger.Infof("zs: ProcessTX txid:%s  UNptx:%s r.TCG.WaitUnChoosedTxCNs already false\n",txid,ptxid)
						}



					}else{
						if ptxcn.State==1{
							logger.Infof("zs: ProcessTX txid:%s UNptx:%s is Committed\n",txid,ptxid)
						}else if ptxcn.State==2{
							logger.Infof("zs:ERROR-12UN ProcessTX txid:%s UNptx:%s ptxcn.State==2\n",txid,ptxid)
						}else if ptxcn.State==3{
							logger.Infof("zs: ProcessTX txid:%s UNptx:%s is Wait\n",txid,ptxid)
							*ptxcn.UNChildNodes=append(*ptxcn.UNChildNodes,txCN)
							txCN.UNParentTxConsensusNode[ptxid]=true
							txCN.ParentNum++
						}else if ptxcn.State==4{
							logger.Infof("zs:ERROR-13UN ProcessTX txid:%s UNptx:%s ptxcn.State==4 is Error\n",txid,ptxid)
						}
					}
				}
			}
		}
	}
	if isExistwaittxcn==true{
		txCN.NChildNodes=waittxcn.NChildNodes
		txCN.UNChildNodes=waittxcn.UNChildNodes
		if txCN.State!=4{
			for _,waitchild:=range waittxcn.ChildWait{
				delete(waitchild.WaitPTX,txid)
			}
			delete(r.TCG.WaitUnChoosedTxCNs,txid)
		}
		if txCN.State==4{
			waittxcn.ChoosedNode=txCN
			r.TCG.NumChangedState=r.TCG.NumChangedState+waittxcn.ChildNum
		}
	}

	if txCN.State!=4 && txCN.ParentNum==0{
		logger.Infof("zs: ProcessTX txid:%s committed\n",txid)
		r.TCG.NumGCommittedNode++
		*r.TCG.ReadyBatch=append(*r.TCG.ReadyBatch,txCN.ChoosedNode.TxResult)
		txCN.State=1
		logger.Infof("zs: ProcessTX txid:%s BE Delete txcn.State=%d\n",txid,txCN.State)
		r.Delete(txCN)
		logger.Infof("zs: ProcessTX txid:%s EN Delete txcn.State=%d\n",txid,txCN.State)

	}else{
		if txCN.State==4{
			logger.Infof("zs: ProcessTX txid:%s BE DeleteUN txcn.State=%d\n",txid,txCN.State)
			r.Delete(txCN)
			logger.Infof("zs: ProcessTX txid:%s EN DeleteUN txcn.State=%d\n",txid,txCN.State)
		}else{
			for ptxid,_:=range txCN.ParentTxConsensusNode{
				logger.Infof("zs: ProcessTX end txid:%s ptx:%s\n",txid,ptxid)
			}
			for ptxid,_:=range txCN.UNParentTxConsensusNode{
				logger.Infof("zs: ProcessTX end txid:%s UNptx:%s\n",txid,ptxid)
			}
		}
	}
	logger.Infof("zs:ProcessTX txid:%s EN txcn.State=%d\n",txid,txCN.State)
}

func (r *receiver) Delete(dtxcn *partialorder.TxConsensusNode) {



	txid:=dtxcn.ChoosedNode.TxId
	logger.Infof("zs:Delete txid:%s BE dtxcn.State=%d\n",txid,dtxcn.State)

	if dtxcn.State==1{
		if dtxcn.ChoosedNode!=nil{
			for wk,wkv:=range dtxcn.ChoosedNode.WriteSet{
				if len(dtxcn.ChoosedNode.ReadSet)>=2{
					logger.Infof("zs:DeleteTx txid:%s gk:%s gv:%s\n",txid,wk,wkv.Value)
				}
				r.TCG.GState[wk]=wkv.Value
			}
		}
	}




	for _,unchildtxcn:=range *dtxcn.UNChildNodes{
		logger.Infof("zs:Delete txid:%s dtxcn.State=%d unchildtxid:%s\n",
		txid,dtxcn.State,unchildtxcn.ChoosedNode.TxId)
		delete(unchildtxcn.UNParentTxConsensusNode,txid)
		unchildtxcn.ParentNum--
		if unchildtxcn.ParentNum==0{
			logger.Infof("zs:Delete txid:%s dtxcn.State=%d unchildtxid:%s Committed\n",
				txid,dtxcn.State,unchildtxcn.ChoosedNode.TxId)
			r.TCG.NumGCommittedNode++
			*r.TCG.ReadyBatch=append(*r.TCG.ReadyBatch,unchildtxcn.ChoosedNode.TxResult)
			unchildtxcn.State=1
			logger.Infof("zs:Delete txid:%s dtxcn.State=%d unchildtxid:%s unclidState=%d Delete BE\n",
				txid,dtxcn.State,unchildtxcn.ChoosedNode.TxId,unchildtxcn.State)
			r.Delete(unchildtxcn)
			logger.Infof("zs:Delete txid:%s dtxcn.State=%d unchildtxid:%s unclidState=%d Delete EN\n",
				txid,dtxcn.State,unchildtxcn.ChoosedNode.TxId,unchildtxcn.State)
		}
	}
	if dtxcn.State==4{
		for _,nchildtxcn:=range *dtxcn.NChildNodes{
			logger.Infof("zs:Delete txid:%s dtxcn.State=%d nchildtxid:%s \n",
			txid,dtxcn.State,nchildtxcn.ChoosedNode.TxId)
			if nchildtxcn.State!=4{
				nchildtxcn.State=4
				logger.Infof("zs:Delete txid:%s dtxcn.State=%d nchildtxid:%s nclidState=%d Delete BE\n",
					txid,dtxcn.State,nchildtxcn.ChoosedNode.TxId,nchildtxcn.State)
				r.Delete(nchildtxcn)
				logger.Infof("zs:Delete txid:%s dtxcn.State=%d nchildtxid:%s nclidState=%d Delete EN\n",
					txid,dtxcn.State,nchildtxcn.ChoosedNode.TxId,nchildtxcn.State)
			}
		}
	}else{
		for _,nchildtxcn:=range *dtxcn.NChildNodes{
			logger.Infof("zs:Delete txid:%s dtxcn.State=%d nchildtxid:%s \n",
			txid,dtxcn.State,nchildtxcn.ChoosedNode.TxId)
			delete(nchildtxcn.ParentTxConsensusNode,txid)
			nchildtxcn.ParentNum--
			if nchildtxcn.ParentNum==0{
				logger.Infof("zs:Delete txid:%s dtxcn.State=%d nchildtxid:%s Committed\n",
					txid,dtxcn.State,nchildtxcn.ChoosedNode.TxId)
				r.TCG.NumGCommittedNode++
				*r.TCG.ReadyBatch=append(*r.TCG.ReadyBatch,nchildtxcn.ChoosedNode.TxResult)
				nchildtxcn.State=1
				logger.Infof("zs:Delete txid:%s dtxcn.State=%d nchildtxid:%s nclidState=%d Delete BE\n",
					txid,dtxcn.State,nchildtxcn.ChoosedNode.TxId,nchildtxcn.State)
				r.Delete(nchildtxcn)
				logger.Infof("zs:Delete txid:%s dtxcn.State=%d nchildtxid:%s nclidState=%d Delete EN\n",
					txid,dtxcn.State,nchildtxcn.ChoosedNode.TxId,nchildtxcn.State)
			}
		}
	}
	logger.Infof("zs:Delete txid:%s EN dtxcn.State=%d\n",txid,dtxcn.State)
}
func (r *receiver) OrderedRaft(gvnode *partialorder.GVNode ) ( [][]*cb.Envelope, bool) {
	var pending bool
	messageBatches:= [][]*cb.Envelope{}
	//cns:=[]*partialorder.ChildTxConsensusNode{}
	txCN:=&partialorder.TxConsensusNode{
		TxId: gvnode.TxId,
		ChoosedNode: gvnode,
		ParentNum: 0,
		ParentTxConsensusNode: make(map[string]bool),
		UNParentTxConsensusNode: make(map[string]bool),
		NChildNodes: &[]*partialorder.TxConsensusNode{},
		UNChildNodes: &[]*partialorder.TxConsensusNode{},

		State:3, //1.committed;2.ready;3.wait;4.changedstate
		WaitPTX: make(map[string]bool),

	}
	if len(r.pendingBatch) == 0 {
		// We are beginning a new batch, mark the time
		r.PendingBatchStartTime = time.Now()
	}

	ordererConfig, ok := r.sharedConfigFetcher.OrdererConfig()
	if !ok {
		logger.Panicf("Could not retrieve orderer config to query batch parameters, block cutting is not possible")
	}

	batchSize := ordererConfig.BatchSize()



	r.ProcessTX(txCN)

	pending = true
	

	
	if uint32(len(*r.TCG.ReadyBatch)) >= batchSize.MaxMessageCount {
		logger.Debugf("Batch size met, cutting batch")
		//logger.Infof("zs:CUT Size\n")
		logger.Infof("zs:CUT Size  txid:%s lenReadyBatch:%d  BE\n",gvnode.TxId,len(*r.TCG.ReadyBatch))
		messageBatch := r.Cut()
		messageBatches = append(messageBatches, messageBatch)
		pending = false
		logger.Infof("zs:CUT Size  txid:%s lenReadyBatch:%d lenes:%d len:%d EN\n",gvnode.TxId,len(*r.TCG.ReadyBatch),len(messageBatches),len(messageBatch))

	}else{
		logger.Infof("zs:CUT Size  txid:%s lenReadyBatch:%d  BE\n",gvnode.TxId,len(*r.TCG.ReadyBatch))
		//if len(r.TCG.Root.WRChildNodes) >0 || len(r.TCG.Root.WWChildNodes) >0||len(r.TCG.Root.WRWWChildNodes) >0{
		if len(*r.TCG.ReadyBatch) >0{
			pending=true
		}else{
			pending = false
		}
	}

	logger.Infof("zs:ProcessTX txid:%s lenReadyBatch:%d  batchSize:%d pend:%v\n",
	txCN.ChoosedNode.TxId,len(*r.TCG.ReadyBatch),batchSize.MaxMessageCount,pending)







	return messageBatches,pending
}

//zs

// Ordered should be invoked sequentially as messages are ordered
//
// messageBatches length: 0, pending: false
//   - impossible, as we have just received a message
// messageBatches length: 0, pending: true
//   - no batch is cut and there are messages pending
// messageBatches length: 1, pending: false
//   - the message count reaches BatchSize.MaxMessageCount
// messageBatches length: 1, pending: true
//   - the current message will cause the pending batch size in bytes to exceed BatchSize.PreferredMaxBytes.
// messageBatches length: 2, pending: false
//   - the current message size in bytes exceeds BatchSize.PreferredMaxBytes, therefore isolated in its own batch.
// messageBatches length: 2, pending: true
//   - impossible
//
// Note that messageBatches can not be greater than 2.
func (r *receiver) Ordered(msg *cb.Envelope) (messageBatches [][]*cb.Envelope, pending bool) {
	if len(r.pendingBatch) == 0 {
		// We are beginning a new batch, mark the time
		r.PendingBatchStartTime = time.Now()
	}

	ordererConfig, ok := r.sharedConfigFetcher.OrdererConfig()
	if !ok {
		logger.Panicf("Could not retrieve orderer config to query batch parameters, block cutting is not possible")
	}

	batchSize := ordererConfig.BatchSize()

	messageSizeBytes := messageSizeBytes(msg)
	if messageSizeBytes > batchSize.PreferredMaxBytes {
		logger.Debugf("The current message, with %v bytes, is larger than the preferred batch size of %v bytes and will be isolated.", messageSizeBytes, batchSize.PreferredMaxBytes)

		// cut pending batch, if it has any messages
		if len(r.pendingBatch) > 0 {
			messageBatch := r.Cut()
			messageBatches = append(messageBatches, messageBatch)
		}

		// create new batch with single message
		messageBatches = append(messageBatches, []*cb.Envelope{msg})

		// Record that this batch took no time to fill
		r.Metrics.BlockFillDuration.With("channel", r.ChannelID).Observe(0)

		return
	}

	messageWillOverflowBatchSizeBytes := r.pendingBatchSizeBytes+messageSizeBytes > batchSize.PreferredMaxBytes

	if messageWillOverflowBatchSizeBytes {
		logger.Debugf("The current message, with %v bytes, will overflow the pending batch of %v bytes.", messageSizeBytes, r.pendingBatchSizeBytes)
		logger.Debugf("Pending batch would overflow if current message is added, cutting batch now.")
		messageBatch := r.Cut()
		r.PendingBatchStartTime = time.Now()
		messageBatches = append(messageBatches, messageBatch)
	}

	logger.Debugf("Enqueuing message into batch")
	r.pendingBatch = append(r.pendingBatch, msg)
	r.pendingBatchSizeBytes += messageSizeBytes
	pending = true

	if uint32(len(r.pendingBatch)) >= batchSize.MaxMessageCount {
		logger.Debugf("Batch size met, cutting batch")
		messageBatch := r.Cut()
		messageBatches = append(messageBatches, messageBatch)
		pending = false
	}

	return
}


func (r *receiver) CutTx() []*cb.Envelope {
	logger.Infof("zs:CutTX BE\n")
	batch:=[]*cb.Envelope{}
	batch=*r.TCG.ReadyBatch
	r.TCG.ReadyBatch=&[]*cb.Envelope{}


	logger.Infof("zs:@@@@ NumTXxns:%d NumCommitted:%d NumChangedState:%d\n",len(r.TCG.TxCNs),r.TCG.NumGCommittedNode,r.TCG.NumChangedState)


	logger.Infof("zs:CutTX EN\n")
	return batch
}
func (r *receiver) Cut() []*cb.Envelope {
	logger.Infof("zs:Cut BE\n")
	r.Metrics.BlockFillDuration.With("channel", r.ChannelID).Observe(time.Since(r.PendingBatchStartTime).Seconds())
	r.PendingBatchStartTime = time.Time{}

	beti:=time.Now()
	batch := r.CutTx()
	enti:=time.Now()
	if r.Timecount==0{
		r.ProcessBlock=enti.Sub(beti)
	}else{
		r.ProcessBlock+=enti.Sub(beti)
	}
	if r.Timecount!=0{
		t1:=enti.Add(r.LastProcessTi)
		t2:=enti.Add(r.ProcessTi)
		logger.Infof("zs: block[%d] txnum[%d] blocknum[%d] thisblockProcessTx[%v] thisProcessBlock[%v] allProcessTx[%v]  allProcessBlock[%v] \n",
		r.Timecount,r.Txcount,r.Timecount,t2.Sub(t1),enti.Sub(beti),r.ProcessTi,r.ProcessBlock)
	}
	r.LastProcessTi=r.ProcessTi


	r.pendingBatch = nil
	r.pendingBatchSizeBytes = 0
	r.NumCount=0





	logger.Infof("zs:Cut EN\n")
	r.ZSEntime=time.Now()
	logger.Infof("zs: block[%d] Obe[%v] Oen[%v] O[%v]\n",r.Timecount,r.ZSBetime,r.ZSEntime,r.ZSEntime.Sub(r.ZSBetime))
	r.ZSBetime=time.Now()
	r.Timecount++
	return batch
}
//zs

func messageSizeBytes(message *cb.Envelope) uint32 {
	return uint32(len(message.Payload) + len(message.Signature))
}







