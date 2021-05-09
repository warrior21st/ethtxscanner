package ethtxscanner

import (
	"context"
	"math/big"
	"strconv"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/eth/filters"
	"github.com/ethereum/go-ethereum/ethclient"
)

var (
	_txlogWatcher          TxlogWatcher
	_lastScanedBlockNumber uint64 = 0
	_chainID               *big.Int
	_signer                types.EIP155Signer
	_clientSleepTimes      map[int]uint64
	_filters               map[int]*filters.Filter
)

type TxlogWatcher interface {
	//获取开始扫描的区块号
	GetScanStartBlock() uint64

	//获取节点地址
	GetEthClients() ([]*ethclient.Client, error)

	//是否是需要解析的tx
	IsInterestedLog(address string, topic0 string) bool

	//tx log回调处理方法
	Callback(txlogs []*types.Log) error

	//获取扫描间隔
	GetScanInterval() time.Duration
}

//开始扫描
func StartScanTxLogs(txlogWatcher TxlogWatcher) error {
	LogToConsole("eth tx log scanner starting...")
	_txlogWatcher = txlogWatcher
	startBlock := _txlogWatcher.GetScanStartBlock()
	if _lastScanedBlockNumber == 0 {
		if startBlock > 0 {
			_lastScanedBlockNumber = startBlock - 1
		}
	}
	clients, err := _txlogWatcher.GetEthClients()
	if err != nil {
		return err
	}

	cid, err := clients[0].ChainID(context.Background())
	if err != nil {
		return err
	}
	_chainID = cid
	_signer = types.NewEIP155Signer(_chainID)
	LogToConsole("chainID:" + _chainID.String() + ",filter scaning...")

	for i := 0; i < len(clients); i++ {
		clients[i].Close()
	}

	scanInterval := _txlogWatcher.GetScanInterval()
	if scanInterval <= time.Millisecond {
		scanInterval = 0
	}
	errCount := 0
	for true {
		scanedBlock, err := scanTxLogs(_lastScanedBlockNumber + 1)
		if err != nil {
			if scanedBlock > 0 {
				_lastScanedBlockNumber = scanedBlock
			} else {
				errCount++
			}
		} else {
			errCount = 0
		}

		//如果连续报错达到10次，则线程睡眠10秒后继续
		if errCount == 10 {
			LogToConsole("scaning block continuous error " + strconv.Itoa(errCount) + " times,sleep 30s...")
			time.Sleep(30 * time.Second)
			errCount = 0
		}

		if scanInterval > 0 {
			time.Sleep(scanInterval)
		}
	}

	return nil
}

func scanTxLogs(startBlock uint64) (uint64, error) {
	clients, err := _txlogWatcher.GetEthClients()
	if err != nil {
		return 0, err
	}

	for i := 0; i < len(clients); i++ {
		defer clients[i].Close()
	}

	errorSleepSeconds := 10
	currBlock := startBlock
	finisedMaxBlock := startBlock - 1
	filter := ethereum.FilterQuery{}
	for true {
		avaiIndexes := RebuildAvaiIndexes(len(clients), &_clientSleepTimes)
		if len(avaiIndexes) == 0 {
			break
		}
		for len(avaiIndexes) > 0 {
			index := avaiIndexes[currBlock%uint64(len(avaiIndexes))]
			client := clients[index]
			LogToConsole("scaning block " + strconv.FormatUint(currBlock, 10) + "tx logs on client_" + strconv.Itoa(index, 10) + "...")

			filter.BlockHash = common.BytesToHash(new(big.Int).SetUint64(currBlock))
			logs, err := client.FilterLogs(context.Background(), filter)
			if err != nil {
				_clientSleepTimes[index] = time.Now().UTC().Unix() + 10
				LogToConsole("client_" + strconv.Itoa(index, 10) + "response error,sleep " + strconv.Itoa(errorSleepSeconds) + "s.")
				continue
			}

			if logs == nil {
				break
			}

			for _, log := range logs {
				if _txlogWatcher.IsInterestedLog(log.Address.Hex(), log.Topics[0].Hex()) {
					_txlogWatcher.Callback(log)
				}
			}

			finisedMaxBlock = currBlock
			currBlock++
		}
	}

	return finisedMaxBlock, nil
}
