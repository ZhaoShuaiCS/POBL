package shim

import (
	"fmt"
	//"github.com/hyperledger/fabric/protos/ledger/rwset/kvrwset"
	"github.com/pkg/errors"
	//"strings"
	"sync"
)
type LocalState struct {
	NameSpace        string
	LocalKeyValues map[string]*KeyValue
	KVLock map[string]*sync.Mutex
	LKVMapLock *sync.Mutex
}
type KeyValue struct {
	ReadTx  *[]string
	Key 	string
	Before  *KeyValue
	Next    *KeyValue
	Value	[]byte
	StrValue string
	PendingAndReadyCh chan *LocalTxnSimulator
	WriteLock *sync.Mutex
	WriteFlag int
	FromTxncount int64
	FromTxnid    string
	IsWriteAfterRead bool
	MaxReadTxnCount int64
}
func (ls *LocalState) AddStateUnLock(txid string,key string,value []byte){
	localKeyValue, kvInLKV := ls.LocalKeyValues[key]
	if kvInLKV==false{
		ls.LKVMapLock.Lock()
		if kvInLKV==false{
			localKeyValue=&KeyValue{
				ReadTx:            &[]string{},
				Key:               key,
				Before:            nil,
				Next:              nil,
				Value:             value,
				//PendingAndReadyCh: make(chan *LocalTxnSimulator,1000),
				WriteLock:         new(sync.Mutex),
				WriteFlag:         0,
				FromTxncount:      1,
				FromTxnid:         "global",
				IsWriteAfterRead:  true,
			}
			ls.LocalKeyValues[key]=localKeyValue
		}
		localKeyValue=ls.LocalKeyValues[key]
		ls.LKVMapLock.Unlock()
		fmt.Printf("zs: addstate txid:%s k:%s v:%s ",txid,key,value)
	}else{
		fmt.Printf("zs: addstate already exist txid:%s k:%s v:%s ",txid,key,value)
	}
}
func (ls *LocalState) AddState(txid string,key string,value []byte){
	localKeyValue, kvInLKV := ls.LocalKeyValues[key]
	if kvInLKV==false{
		ls.LKVMapLock.Lock()
		if kvInLKV==false{
			localKeyValue=&KeyValue{
				ReadTx:            &[]string{},
				Key:               key,
				Before:            nil,
				Next:              nil,
				Value:             value,
				//PendingAndReadyCh: make(chan *LocalTxnSimulator,1000),
				WriteLock:         new(sync.Mutex),
				WriteFlag:         0,
				FromTxncount:      1,
				FromTxnid:         "global",
				IsWriteAfterRead:  true,
			}
			ls.LocalKeyValues[key]=localKeyValue
		}
		localKeyValue=ls.LocalKeyValues[key]
		ls.LKVMapLock.Unlock()
		fmt.Printf("zs: addstate txid:%s k:%s v:%s ",txid,key,value)
	}else{
		fmt.Printf("zs: addstate already exist txid:%s k:%s v:%s ",txid,key,value)
	}
}

//zsunlock0421
func (ls *LocalState) GetUnLockState( key string, lts *LocalTxnSimulator) (*KeyValue,error) {
	//fmt.Printf("zs: in GetLockState txid:%s    key: %s" ,lts.Txid,key)
	if localKeyValue, okkey := ls.LocalKeyValues[key]; okkey {
		//fmt.Printf("zs: in GetLockState txid:%s    key: %s before lock" ,lts.Txid,key)
		//localKeyValue.WriteLock.Lock()
		//fmt.Printf("zs: in GetUNLockState txid:%s    key: %s v:%s\n" ,lts.Txid,key,string(localKeyValue.Value))
		return localKeyValue,nil
	}else {
		//fmt.Printf("zs: in GetUNLockState txid:%s    has not key: %s\n" ,lts.Txid,key)
		err := errors.Errorf("zs: localstate    do not has key: %s \n" ,key)
		return nil,err
	}
}

func (ls *LocalState) SetUnLockKVForWrite(key string,btvalue []byte, strvalue string,txid string) (*KeyValue,string,[]byte){
	var ptxid string 
	ptxid=""
	oldvalue:=[]byte{}
	localKeyValue, kvInLKV := ls.LocalKeyValues[key]
	if kvInLKV==false{
		ls.LKVMapLock.Lock()
		localKeyValue, kvInLKV = ls.LocalKeyValues[key]
		if kvInLKV==false{
			tlocalKeyValue:=&KeyValue{
				ReadTx:            &[]string{},
				Key:               key,
				Before:            nil,
				Next:              nil,
				Value:             btvalue,
				StrValue:          strvalue,
				PendingAndReadyCh: nil,
				WriteLock:         new(sync.Mutex),
				WriteFlag:         0,
				FromTxncount:      -7,
				FromTxnid:         txid,
				IsWriteAfterRead:  false,
			}
			ls.LocalKeyValues[key]=tlocalKeyValue
			localKeyValue=ls.LocalKeyValues[key]
			ls.LKVMapLock.Unlock()
			ptxid=localKeyValue.FromTxnid
			oldvalue=localKeyValue.Value
		}else{
			ls.LKVMapLock.Unlock()
			localKeyValue=ls.LocalKeyValues[key]
			oldvalue=localKeyValue.Value
			localKeyValue.Value=btvalue
			localKeyValue.StrValue=strvalue
			ptxid=localKeyValue.FromTxnid
			localKeyValue.FromTxnid=txid
		}
	}else{
		oldvalue=localKeyValue.Value
		localKeyValue.Value=btvalue
		localKeyValue.StrValue=strvalue
		ptxid=localKeyValue.FromTxnid
		localKeyValue.FromTxnid=txid
	}
	return localKeyValue,ptxid,oldvalue
}
//zsunlock0421