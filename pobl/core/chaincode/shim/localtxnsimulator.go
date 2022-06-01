package shim

import (
	"fmt"
	"github.com/hyperledger/fabric/protos/ledger/rwset/kvrwset"
	"sync"
	//"strings"
)

type LocalTxnSimulator struct {
	h 				*Handler
	localState 		*LocalState
	ReadSets 		map[string]bool
	WriteSets 		*WriteKeyValue
	Txid            string
	TxidCount 		int64
	MaxReadCount 	int64
	IsValid    		bool
	RepeatedNum    	int64
	FinalReadSet 	[]*kvrwset.KVRead
	FinalWriteSet 	[]*kvrwset.KVWrite
	LockMap			map[string]*KeyValue

	// LockReadSets  map[string]*LockReadKeyValue
	// LockWriteSets map[string]*LockWriteKeyValue
	UNLockReadSets  map[string]*UNLockKV
	UNLockWriteSets map[string]*UNLockKV
	PeerID 				 string
	TxCount              int64

}
type UNLockKV struct{
	KeyValue *KeyValue
	Key string
	StrValue string
	BtValue []byte
	PTxid string
}
type LockReadKeyValue struct{
	KeyValue *KeyValue
	IsLock bool
}

type LockWriteKeyValue struct{
	KeyValue *KeyValue
	Value []byte
	IsDelete bool
	IsInR bool
}

type WriteKeyValue struct {
	WriteLock *sync.Mutex
	Pre *WriteKeyValue
	Next *WriteKeyValue
	keyValue *KeyValue
	IsWrite bool
	IsInRS bool
	IsWriteKV bool
	IsReadKV bool
	ReadKVFromTxncount int64
	IsGetLock bool
}
func NewLocalTxnSimulator(h *Handler, txid string) (*LocalTxnSimulator, error) {
	return &LocalTxnSimulator{
		h: 			   h,
		localState:    h.LocalState,
		ReadSets:      make(map[string]bool),
		WriteSets:     nil,
		Txid:          txid,
		TxidCount:     0,
		MaxReadCount:  0,
		IsValid:       true,
		RepeatedNum:   0,
		FinalReadSet:  []*kvrwset.KVRead{},
		FinalWriteSet: []*kvrwset.KVWrite{},
		LockMap: 	   make(map[string]*KeyValue),
		UNLockReadSets:  make(map[string]*UNLockKV),
		UNLockWriteSets: make(map[string]*UNLockKV),
		PeerID:"", 				
		TxCount:-1,            
		// LockReadSets: map[string]*LockReadKeyValue{},
		// LockWriteSets: map[string]*LockWriteKeyValue{},
	},nil
}
func (s *LocalTxnSimulator) DeleteState(key string) error {
	return s.SetState( key, nil)
}

func (lts *LocalTxnSimulator) AddState(txid string, key string,value []byte)  {
	//fmt.Printf("zs: in addstate txid:%s    key: %s v:%s ",txid,  key,string(value))
	lts.localState.AddState( txid,key, value)
	lts.FinalReadSet=append(lts.FinalReadSet,&kvrwset.KVRead{Key: key,Version: nil,Value: value,IsRead: true,Txid: txid})
}

func (lts *LocalTxnSimulator) GetState(key string) ([]byte, error)  {
	lk,err:=lts.localState.GetUnLockState( key,lts)
	if err!=nil{
		fmt.Printf("zs: getUNlockstate txid:%s ky: %s err:%s\n",lts.Txid,  key, err.Error())
		return nil, nil
	}else{
		tempstr:=lk.StrValue
		temptxid:=lk.FromTxnid
		BtValue:=[]byte(tempstr)
		lts.UNLockReadSets[key]=&UNLockKV{
			KeyValue: lk,
			Key: lk.Key,
			StrValue: tempstr,
			BtValue: BtValue,
			PTxid: temptxid,
		}
		fmt.Printf("\n zs: getUNlockstate txid:%s ky: %s v:%s lts.strv:%s returnV:%s Ptxid:%s \n",lts.Txid, key,lk.StrValue,lts.UNLockReadSets[key].StrValue,string(BtValue),temptxid)
		//lts.FinalReadSet=append(lts.FinalReadSet,&kvrwset.KVRead{Key: key,Version: nil,Value: BtValue,IsRead: true,Txid: lk.FromTxnid})
		return BtValue,nil
	}
}
func (lts *LocalTxnSimulator) SetState( key string, value []byte) error  {
	lts.UNLockWriteSets[key]=&UNLockKV{
		KeyValue: nil,
		Key: key,
		StrValue: string(value),
		BtValue: value,
		PTxid: "",
	}
	return nil
}
