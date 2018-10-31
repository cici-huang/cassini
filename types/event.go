package types

import (
	"errors"

	motxs "github.com/QOSGroup/cassini/mock/tx"
	bcapp "github.com/QOSGroup/qbase/example/basecoin/app"
	"github.com/QOSGroup/qbase/txs"
	qosapp "github.com/QOSGroup/qos/app"
	"github.com/tendermint/go-amino"
	"github.com/tendermint/tendermint/libs/common"
	ctypes "github.com/tendermint/tendermint/rpc/core/types"
)

type CassiniEventDataTx struct {
	From      string `json:"from"` //qsc name 或 qos
	To        string `json:"to"`   //qsc name 或 qos
	Height    int64  `json:"height"`
	Sequence  int64  `json:"sequence"`
	HashBytes []byte `json:"hashBytes"` //TxQcp 做 sha256
}

type Event struct {
	NodeAddress        string               `json:"node"` //event 源地址
	CassiniEventDataTx `json:"eventDataTx"` //event 事件
}

// CreateCompleteCodec 创建完整（包括：联盟链，公链，中继）amino编码器
func CreateCompleteCodec() *amino.Codec {

	// qos cdc
	cdc := qosapp.MakeCodec()

	// tedermint cdc
	ctypes.RegisterAmino(cdc)

	// qbase cdc
	txs.RegisterCodec(cdc)
	bcapp.RegisterCodec(cdc)

	// cassini cdc
	RegisterCassiniTypesAmino(cdc)
	cdc.RegisterConcrete(&motxs.TxMock{}, "cassini/mock/txmock", nil)
	return cdc
}

// RegisterCassiniTypesAmino 注册中继自定义类型
func RegisterCassiniTypesAmino(cdc *amino.Codec) {
	//cdc.RegisterInterface((*TMEventData)(nil), nil)
	cdc.RegisterConcrete(CassiniEventDataTx{}, "cassini/event/CassiniEventDataTx", nil)
	cdc.RegisterConcrete(Event{}, "cassini/event/Event", nil)
	//cdc.RegisterConcrete(TxQcp{}, "cassini/txqcp/TxQcp", nil)
}

func (c *CassiniEventDataTx) ConstructFromTags(tags []common.KVPair) (err error) {

	if tags == nil || len(tags) == 0 {
		return errors.New("empty tags")
	}
	for _, tag := range tags {
		if string(tag.Key) == "qcp.from" {
			c.From = string(tag.Value)
		}
		if string(tag.Key) == "qcp.to" {
			c.To = string(tag.Value)
		}
		if string(tag.Key) == "qcp.hash" {
			c.HashBytes = tag.Value
		}
		if string(tag.Key) == "qcp.sequence" {
			c.Sequence, err = BytesInt64(tag.Value)
			if err != nil {
				return err
			}
		}
	}

	return
}
