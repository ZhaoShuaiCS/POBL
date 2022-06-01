package partialorder
import (
	"time"

	"github.com/hyperledger/fabric/protos/common"
	"github.com/hyperledger/fabric/core/ledger/kvledger/txmgmt/rwsetutil"
	cb "github.com/hyperledger/fabric/protos/common"
	"github.com/hyperledger/fabric/common/flogging"
	"github.com/hyperledger/fabric/protos/utils"
	"strings"

)
var logger = flogging.MustGetLogger("ZSPatrialOrder")
type GVReadKV struct {
	Key string
	Value string
	//Type int64     //1: read-write, must wait or false; 2: only-write, must wait or false; 3: only-read, no wait or ture;
	ParentTxId string
}
type GVWriteKV struct {
	Key string
	Value string
	//Type int64     //1: inReadSet; 0: OutofReadset
	ParentTxId string
}
type PtxKV struct {
	KV map[string]string
	//Value map[string]string
	Type map[string]int64     //1: n-wr;2:n-wr and n-ww;3:n-ww;4:n-rw;
}

type GVNode struct {
	Count int64
	TxResult *common.Envelope
	TxRWSet *rwsetutil.TxRwSet
	TxId string
	//Votes int64
	ReadSet map[string] *GVReadKV
	WriteSet map[string]*GVWriteKV
	PTx map[string]*PtxKV
	UNPTx map[string]*PtxKV
	State int64 //1.committed; 2.wait but choosed; 3. wait but not choosed; 4:false
	ConsensusState int64 //1.all rkv rkvptx wkvptx;2. rkv;3. not reach consensus; 

	NumSameRKV int64
	NumSameRKVPtx int64
}
// type GVNodeQueue struct {
// 	ChoosedGVNode *GVNode
// 	Next *GVNodeQueue
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
type UnchoosedGVNode struct {
	//GVNodeInQ *GVNodeQueue
	UNPTxVotes map[string]int64
	UNPTx map[string]*PtxKV
	//Num
	Count int64
	ChoosedGVNode *GVNode
	// ReadVotes map[string]int64
	// WriteVotes map[string]int64
	// MaxReadValue map[string]string
	// ConRKVGVNodes map[string]*GVNode
	// NumConsensusRKV int64
	// NumConsensusWKV int64
	ValueGVNode *[]*GVNode
	AllVotes int64
	State int64//1.committed; 2.wait but choosed; 3. wait but not choosed; 4:false
}
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
type GVConsensus struct {
	//GVNodeQHeader *GVNodeQueue
	GUnchoosedGVNode map[string]*UnchoosedGVNode
	GChoosedGVNode map[string]*GVNode
	// GlobalState map[string]*StateValue
	// WaitStateGVNode map[string]*WaitStateValue
	NumExeNodes int64
	NumKWeight int64
	NumTxwithAllvotes int64
	NumRKVVotes2 int64
	NumRKVVotes3 int64
	NumRKVVotes4 int64
	NumRKVVotes5 int64
	NumVotes2 int64
	NumVotes3 int64
	NumVotes4 int64
	NumVotes5 int64
	NumGChooseNode int64
	NumGCommittedNode int64
	OTXCount int64
	OTXLastCount int64
	OTXLastTime time.Time
	ROTXCount int64
	ROTXLastCount int64
	ROTXLastTime time.Time
}

// type ChildTxConsensusNode struct {
// 	ChildNode *TxConsensusNode
// 	ChildType int64 //1,n-wr;2,n-ww;3, n-wr && n-ww
// }

// type TxConsensusNode struct {
// 	ChoosedNode *GVNode
// 	State int64 //1.committed;2.ready;3.wait;4.unexist
// 	ReadyParentTxConsensusNode map[string]*TxConsensusNode
// 	WaitParentTxConsensusNode map[string]*TxConsensusNode
// 	NumChildTx int64
// 	ChildNodes []*ChildTxConsensusNode
// }

// type TxConsensusGraph struct {
// 	GState map[string]*StateValue
// 	ReadyTxCNs map[string]*TxConsensusNode
// 	WaitTxCNs map[string]*TxConsensusNode
// 	RootTxCNode map[string]*TxConsensusNode
// 	WaitState map[string][]*TxConsensusNode
// 	WaitStateTxCNs map[string] []*ChildTxConsensusNode
// 	NumReadyTx int64
// 	NumGCommittedNode int64
// }

type GVNodeMsg struct{
	ChoosedNode *GVNode
	CommitC     chan [][]*common.Envelope
}

type TxConsensusNode struct {
	TxId string
	ChoosedNode *GVNode
	ParentNum int64
	ParentTxConsensusNode map[string]bool
	UNParentTxConsensusNode map[string]bool
	NChildNodes *[]*TxConsensusNode
	UNChildNodes *[]*TxConsensusNode
	//RootPartentWW map[string]int64
	// WRChildNodes map[string]*[]*TxConsensusNode
	// WWChildNodes map[string]*[]*TxConsensusNode
	// WRWWChildNodes map[string]*[]*TxConsensusNode
	WaitPTX map[string]bool
	State int64 //1.committed;2.ready;3.wait;4.changedstate
}

type WaitTxConsensusNode struct {
	//WaitWKV map[string]string
	ChildNum int64
	NChildNodes *[]*TxConsensusNode
	UNChildNodes *[]*TxConsensusNode
	// WRChildNodes map[string]*[]*TxConsensusNode
	// WWChildNodes map[string]*[]*TxConsensusNode
	// WRWWChildNodes map[string]*[]*TxConsensusNode
	WriteSet map[string]string
	DiffWaitWKV bool
	ChoosedNode *TxConsensusNode
	ChildWait map[string]*TxConsensusNode
}

type TxConsensusGraph struct {
	//ReadyMap map[string]bool
	//MinCount int64
	//CountMap map[int64]bool
	GState map[string]string
	TxCNs map[string]*TxConsensusNode
	CommittedTxCNs map[string]*TxConsensusNode
	WaitUnChoosedTxCNs map[string]*WaitTxConsensusNode
	Root *TxConsensusNode
	//TempRoot *TxConsensusNode
	//RootWWChildNodes map[string]*[]*TxConsensusNode
	// RootWRChildNodes map[string]*[]*TxConsensusNode
	// RootWWChildNodes map[string]*[]*TxConsensusNode
	// RootWRWWChildNodes map[string]*[]*TxConsensusNode
	//NumReadyTx int64
	NumGCommittedNode int64
	NumChangedState int64
	ReadyBatch *[]*cb.Envelope
}
type EnvMsg struct{
	Env *common.Envelope
	CommitC     chan [][]*common.Envelope
}
func (tcn *TxConsensusNode) ProcessEnvForBC(env *common.Envelope) {

	chdr,respPayload,errres:=utils.GetActionAndTxidFromEnvelopeMsg(env)
	if errres!=nil{
		logger.Infof("zs:ERROR in respPayload,errres:=utils.GetActionFromEnvelopeMsg(env)\n")
	}
	txId:=chdr.TxId

	txRWSet := &rwsetutil.TxRwSet{}
	if err := txRWSet.FromProtoBytes(respPayload.Results); err != nil {
		logger.Infof("zs: gettx txid:%s txRWSet.FromProtoBytes failed",txId)
	}
	gnodeA:=&GVNode{
		TxResult: env,
		TxRWSet: txRWSet,
		TxId: txId,
		ReadSet: make(map[string] *GVReadKV),
		WriteSet: make(map[string]*GVWriteKV),
		PTx: make(map[string]*PtxKV),
		UNPTx: make(map[string]*PtxKV),
		State: 3, //1.committed; 2.wait but choosed; 3. wait but not choosed; 4:false
		ConsensusState: 0,//1.all rkv rkvptx wkvptx;2. rkv rkvptx;3. rkv; 0.unconsensus
		NumSameRKV: 1,
		NumSameRKVPtx: 1,
	}
	for _,rwset:=range txRWSet.NsRwSets{
		//logger.Infof("zs: gettx txid:%s namespace:%s ",txId,rwset.NameSpace)
		if strings.Compare(rwset.NameSpace,"lscc")!=0{
			for _,rkv:=range rwset.KvRwSet.Reads{
				rkValue:=string(rkv.Value)
				if rkv.IsRead==true{
					gvreadKV:=&GVReadKV{
						Key:        rkv.Key,
						Value:      rkValue,
						//Type:       1,
						ParentTxId: rkv.Txid,
					}
					gnodeA.ReadSet[rkv.Key]=gvreadKV
	
					ptmap,isExistptmap:=gnodeA.PTx[rkv.Txid]
					if isExistptmap==false{
						gnodeA.PTx[rkv.Txid]=&PtxKV{
							KV: make(map[string]string),
							//Value map[string]string
							Type: make(map[string]int64),  
						}
						ptmap=gnodeA.PTx[rkv.Txid]
					}
					ptmap.KV[rkv.Key]=rkValue
					ptmap.Type[rkv.Key]=1
					// logger.Infof("zs: gettx txid:%s normal namespace:%s rk:%s rv:%s risRead:%v rtxid:%s type:%d",
					// txId,rwset.NameSpace,rkv.Key,string(rkv.Value),rkv.IsRead,rkv.Txid,ptmap.Type[rkv.Key])
				}else{
					// newk:=rkv.Key[:len(rkv.Key)-len(rkv.Txid)]
					// gunptmap,isExistgunptmap:=gnodeA.UNPTx[rkv.Txid]
					// if isExistgunptmap==false{
					// 	gnodeA.UNPTx[rkv.Txid]=&PtxKV{
					// 		KV: make(map[string]string),
					// 		//Value map[string]string
					// 		Type: make(map[string]int64),  
					// 	}
					// 	gunptmap=gnodeA.UNPTx[rkv.Txid]
					// }
					// gunptmap.KV[newk]=rkValue
					// gunptmap.Type[newk]=4
				}
			}
			for _,wkv:=range rwset.KvRwSet.Writes{
				gnodeA.WriteSet[wkv.Key]=&GVWriteKV{
					Key:   wkv.Key,
					Value: string(wkv.Value),
					//Type:  1,
					//ParentTxId: wkv.Txid,
				}
			}
		}
	}

	// for rk,rkv:=range gnodeA.ReadSet{
	// 	logger.Infof("zs: gettx @@txid:%s rk:%s rv:%s \n",txId,rk,string(rkv.Value))
	// }
	// if len(gnodeA.ReadSet)>=2{
	// 	for wk,wkv:=range gnodeA.WriteSet{
	// 		logger.Infof("zs: gettx @@txid:%s wk:%s wv:%s \n",txId,wk,string(wkv.Value))
	// 	}
	// }
	// for ptxid,ptx:=range gnodeA.PTx{
	// 	for ptxk,ptxkv:=range ptx.KV{
	// 		logger.Infof("zs: gettx @@txid:%s ptxid:%s  pk:%s pv:%s ptype:%d\n",txId,ptxid,ptxk,ptxkv,ptx.Type[ptxk])
	// 	}
	// }
	// for ptxid,ptx:=range gnodeA.UNPTx{
	// 	for ptxk,ptxkv:=range ptx.KV{
	// 		logger.Infof("zs: gettx @@txid:%s UNptxid:%s  UNpk:%s UNpv:%s UNptype:%d\n",txId,ptxid,ptxk,ptxkv,ptx.Type[ptxk])
	// 	}
	// }
	tcn.TxId= gnodeA.TxId
	tcn.ChoosedNode= gnodeA

}

