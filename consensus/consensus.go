package consensus

import (
	"errors"
	cmn "github.com/QOSGroup/cassini/common"
	"github.com/QOSGroup/cassini/config"
	"github.com/QOSGroup/cassini/log"
	"github.com/QOSGroup/cassini/restclient"
	"github.com/QOSGroup/cassini/types"
	"github.com/QOSGroup/qbase/example/basecoin/app"
	"github.com/QOSGroup/qbase/txs"
	"github.com/nats-io/go-nats"
	"github.com/tendermint/go-amino"
	"github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/libs/common"
	"strings"
)

// ConsEngine Consensus engine
type ConsEngine struct {
	M        *MsgMapper
	f        *Ferry
	sequence int64
	from     string
	to       string
	conf     *config.Config
}

// NewConsEngine New a consensus engine
func NewConsEngine(from, to string) *ConsEngine {
	ce := new(ConsEngine)
	ce.M = &MsgMapper{MsgMap: make(map[int64]map[string]string)}
	ce.f = &Ferry{config.GetConfig()}
	ce.from = from
	ce.to = to
	ce.conf = config.GetConfig()
	return ce
}

// Add2Engine Add a message to consensus engine
func (c *ConsEngine) Add2Engine(msg *nats.Msg) error {
	event := types.Event{}

	if amino.UnmarshalBinary(msg.Data, &event) != nil {

		return errors.New("the event Unmarshal error")
	}

	if event.Sequence < c.sequence {
		return errors.New("msg sequence is small then the sequence in consensus engine")
	}

	seq, err := c.M.AddMsgToMap(c.conf, c.f, event)
	if err != nil {
		return err
	}
	c.SetSequence(seq)
	return nil
}

// StartEngine 出发共识引擎尝试处理下一个交易
func (c *ConsEngine) StartEngine() error {
	log.Debugf("Start consensus engine from: [%s] to: [%s] sequence: [%d]",
		c.from, c.to, c.sequence)
	nodes := c.conf.GetQscConfig(c.from).NodeAddress

	for _, node := range strings.Split(nodes, ",") {

		qcp, err := c.f.queryTxQcpFromNode(c.to, node, c.sequence)

		if err != nil || qcp == nil {
			continue
		}
		hash := crypto.Sha256(qcp.GetSigData())
		ced := types.CassiniEventDataTx{From: c.from, To: c.to, Height: qcp.BlockHeight, Sequence: c.sequence}

		ced.HashBytes = hash

		event := types.Event{NodeAddress: node, CassiniEventDataTx: ced}

		seq, err := c.M.AddMsgToMap(c.conf, c.f, event)
		if err != nil {
			return err
		}
		if seq > 0 {
			c.SetSequence(seq)
			return nil
		}

	}

	return nil

}

// SetSequence 初始化交易序列号
func (c *ConsEngine) SetSequence(s int64) {
	log.Infof("sequence set to [#%d]", s)
	c.sequence = s
}

// Ferry Comsumer tx message and handle(consensus, broadcast...) it.
type Ferry struct {
	conf *config.Config
}

//ferryQCP get qcp transaction from source chain and post it to destnation chain
//
//from is chain name of the source chain
//to is the chain name of destnation chain
//nodes is consensus nodes of the source chain
func (f *Ferry) ferryQCP(from, to, hash, nodes string, sequence int64) (err error) {

	log.Debugf("Ferry qcp from [%s] to [%s], sequence=%d", from, to, sequence)

	qcp, err := f.getTxQcp(from, to, hash, nodes, sequence)

	if err != nil {
		log.Errorf("%v", err)
		return errors.New("get qcp transaction failed")
	}

	qscConf := f.conf.GetQscConfig(from)

	// Sign data for public chain
	// Config in QscConfig.Signature
	// true - required
	// false/default - not required
	if qscConf.Signature {
		cdc := app.MakeCodec()
		err = cmn.SignTxQcp(qcp, f.conf.Prikey, cdc)
		if err != nil {
			log.Errorf("Sign Tx Qcp error: %v", err)
		}
		log.Debugf("Sign Tx Qcp for chain: %s", from)
	}

	err = f.postTxQcp(to, qcp)

	if err != nil {
		return errors.New("post qcp transaction failed")
	}

	log.Infof("success ferry qcp transaction from [%s] to [%s] sequence [#%d] \n", from, to, sequence)
	return nil

}

//getTxQcp get QCP transactions from sorce chain
func (f *Ferry) getTxQcp(from, to, hash, nodes string, sequence int64) (qcp *txs.TxQcp, err error) {

	success := false

EndGet:

	for _, node := range strings.Split(nodes, ",") {

		qcp, err = f.getTxQcpFromNode(to, hash, node, sequence)

		if err != nil || qcp == nil {
			continue
		}

		success = true
		break EndGet

	}

	if !success {
		return nil, errors.New("get qcp transaction from chain " + from + " failed")
	}

	return
}

func (f *Ferry) getTxQcpParalle(from, to, hash, nodes string, sequence int64) (qcps []txs.TxQcp, err error) {

	nodeList := strings.Split(nodes, ",")
	var tasks = make([]common.Task, len(nodeList))

	for i := 0; i < len(tasks); i++ {
		tasks[i] = func(i int) (res interface{}, err error, abort bool) {
			qcp, err := f.getTxQcpFromNode(to, hash, nodeList[i], sequence)
			return qcp, err, false //TODO
		}
	}

	var tResults, ok = common.Parallel(tasks...)
	if !ok {
		log.Error("parallel failed")
	}

	var failTasks int
	for i := 0; i < len(tasks); i++ {
		tResult, ok := tResults.LatestResult(i)
		if !ok {
			failTasks++
		} else if tResult.Error != nil {
			failTasks++
		} else {
			qcps = append(qcps, *(tResult.Value).(*txs.TxQcp))
		}

	}

	if len(qcps)*2 > failTasks { //TODO 加入共识逻辑
		return qcps, nil
	}

	return nil, errors.New("parallel get qcp transaction from chain " + from + " failed")
}

//getTxQcpFromNode get QCP transactions from single chain node
func (f *Ferry) getTxQcpFromNode(to, hash, node string, sequence int64) (qcp *txs.TxQcp, err error) {

	qcp, err = f.queryTxQcpFromNode(to, node, sequence)

	if err != nil || qcp == nil {
		return nil, errors.New("get TxQcp from " + node + "failed.")
	}

	//TODO 取本地联盟链公钥验签
	//pubkey := qcp.Sig.Pubkey  //mock pubkey 为 nil pnic
	//if !pubkey.VerifyBytes(qcp.GetSigData(), qcp.Sig.Signature) {
	//	return nil, errors.New("get TxQcp from " + node + " data verify failed.")
	//}

	// qcp hash 与 hash值比对
	//if string(tmhash.Sum(qcp.GetSigData())) != hash { //算法保持 tmhash.hash 一致 sha256 前 20byte
	hash2 := cmn.Bytes2HexStr(crypto.Sha256(qcp.GetSigData()))
	if hash2 != hash {
		return nil, errors.New("get TxQcp from " + node + "failed")
	}

	return qcp, nil

}

func (f *Ferry) queryTxQcpFromNode(to, node string, sequence int64) (qcp *txs.TxQcp, err error) {

	r := restclient.NewRestClient(node) //"tcp://127.0.0.1:26657"
	qcp, err = r.GetTxQcp(to, sequence)
	if err != nil || qcp == nil {
		return nil, errors.New("get TxQcp from " + node + "failed.")
	}

	return qcp, nil
}

func (f *Ferry) postTxQcp(to string, qcp *txs.TxQcp) (err error) {

	success := false
	qscConfig := f.conf.GetQscConfig(to)
	toNodes := qscConfig.NodeAddress
EndPost:
	for _, node := range strings.Split(toNodes, ",") {

		r := restclient.NewRestClient(node)
		err := r.PostTxQcp(to, qcp) //TODO 连接每个目标链node
		if err != nil {
			continue
		}

		success = true
		break EndPost
	}

	if !success {
		return errors.New("post qcp transaction failed")
	}

	return

}
