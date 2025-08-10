package main_test

// this file is just for storing the data as variables
import (
	"encoding/json"
	"os"

	main "pogolo"

	"github.com/0xf0xx0/stratum"
	"github.com/btcsuite/btcd/btcjson"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
)

// recorded from/with pogolo!
var (
	MOCK_CHAIN = &chaincfg.RegressionNetParams
)

const (
	MOCK_ADDRESS                   = "bcrt1qv2w0jh49962fc0qw63aqlw6p567qkx2dj5kpg4"
	MOCK_MINING_SUBSCRIBE          = `{"id": 1, "method": "mining.subscribe", "params": ["bitaxe/FTXGOXX/v2021-08-24"]}`
	MOCK_MINING_CONFIGURE          = `{"id": 2, "method": "mining.configure", "params": [["version-rolling"], {"version-rolling.mask": "ffffffff"}]}`
	MOCK_MINING_AUTHORIZE          = `{"id": 3, "method": "mining.authorize", "params": ["bcrt1qv2w0jh49962fc0qw63aqlw6p567qkx2dj5kpg4.fakeminer", "x"]}`
	MOCK_MINING_SUGGEST_DIFFICULTY = `{"id": 4, "method": "mining.suggest_difficulty", "params": [0.16]}`

	/// empty bl00k
	MOCK_EXTRANONCE    = "93591212"
	MOCK_MINING_SUBMIT = `{"id": 5, "method": "mining.submit", "params": ["bcrt1qv2w0jh49962fc0qw63aqlw6p567qkx2dj5kpg4.fakeminer", "1", "00000000", "6883f246", "eb2c3fbc"]}`
	MOCK_NOTIFY        = `{"id":null,"method":"mining.notify","params":["1","8ee4ed9550ff98abf30399d85e69c8563474542478fa04f7574529461913f03a","01000000010000000000000000000000000000000000000000000000000000000000000000ffffffff260297001a2f706f676f6c6f202d20666f73732069732066726565646f6d2f","ffffffff0200f9029500000000160014629cf95ea52e949c3c0ed47a0fbb41a6bc0b194d0000000000000000266a24aa21a9ede2f61c3f71d1defd3fa999dfa36953755c690689799962b48bebd836974e8cf900000000",[],"30000000","207fffff","6883f246",true]}`
	MOCK_COINBASE      = "010000000001010000000000000000000000000000000000000000000000000000000000000000ffffffff260297001a2f706f676f6c6f202d20666f73732069732066726565646f6d2f9359121200000000ffffffff0200f9029500000000160014629cf95ea52e949c3c0ed47a0fbb41a6bc0b194d0000000000000000266a24aa21a9ede2f61c3f71d1defd3fa999dfa36953755c690689799962b48bebd836974e8cf90120000000000000000000000000000000000000000000000000000000000000000000000000"
	MOCK_COINBASEHASH  = "a181ca1f1455ab1b2371e4e21f81030ac7452ae8ba0c49167a8a3bda50b70c94"
	MOCK_BLOCKHASH     = "000000007b3ccb15be67ce3be8e70ca904b5de8db6b57fd93353af7797a5b125"
	MOCK_SHAREDIFF     = float64(2.077258530166716)

	/// bl00k with txns
	MOCK_EXTRANONCE_MERKLE    = `0dd001bc`
	MOCK_MINING_SUBMIT_MERKLE = `{"id":5, "method": "mining.submit", "params": ["bcrt1qv2w0jh49962fc0qw63aqlw6p567qkx2dj5kpg4.fakeminer", "1", "00000000", "68845664", "eb906d8f"]}`
	MOCK_NOTIFY_MERKLE        = `{"id":null,"method":"mining.notify","params":["1","97a5b1253353af77b6b57fd904b5de8de8e70ca9be67ce3b7b3ccb1500000000","01000000010000000000000000000000000000000000000000000000000000000000000000ffffffff260298001a2f706f676f6c6f202d20666f73732069732066726565646f6d2f","ffffffff023dc4039500000000160014629cf95ea52e949c3c0ed47a0fbb41a6bc0b194d0000000000000000266a24aa21a9eddaa2ef8f94277097f2e6f4c51f63cff7aac266edfbda60842baeb5b25acde7bb00000000",["0a22eeb01146cf315544041a8495a47f0e071ec5fbbd1ee3f674bd2c3fee8685","70583cc1fa4c3547162030fd068b697ec42c4633d44497734fb926656480f174"],"30000000","207fffff","68845664",true]}`
	MOCK_COINBASE_MERKLE      = `010000000001010000000000000000000000000000000000000000000000000000000000000000ffffffff260298001a2f706f676f6c6f202d20666f73732069732066726565646f6d2f0dd001bc00000000ffffffff023dc4039500000000160014629cf95ea52e949c3c0ed47a0fbb41a6bc0b194d0000000000000000266a24aa21a9eddaa2ef8f94277097f2e6f4c51f63cff7aac266edfbda60842baeb5b25acde7bb0120000000000000000000000000000000000000000000000000000000000000000000000000`
	MOCK_COINBASEHASH_MERKLE  = `11fd052c8f87d6a541fd5f6e80b60ef9ecaeecf2d77147c54d2bb15f8e193818`
	MOCK_BLOCKHASH_MERKLE     = `0000000130c620693f24b879db09359fb6279b8b2736f4bd359d4208dc429092`
	MOCK_SHAREDIFF_MERKLE     = float64(0.8399540342061583)
)

// params
var (
	authorizeParams = func() stratum.AuthorizeParams {
		params := stratum.AuthorizeParams{}
		params.Read(reqFrom(MOCK_MINING_AUTHORIZE))
		return params
	}()
	configureParams = func() stratum.ConfigureParams {
		params := stratum.ConfigureParams{}
		params.Read(reqFrom(MOCK_MINING_CONFIGURE))
		return params
	}()
	subscribeParams = func() stratum.SubscribeParams {
		params := stratum.SubscribeParams{}
		params.Read(reqFrom(MOCK_MINING_SUBSCRIBE))
		return params
	}()
	suggestDiffParams = func() stratum.SuggestDifficultyParams {
		params := stratum.SuggestDifficultyParams{}
		params.Read(reqFrom(MOCK_MINING_SUGGEST_DIFFICULTY))
		return params
	}()

	submitParams = func() stratum.Share {
		params := stratum.Share{}
		req := reqFrom(MOCK_MINING_SUBMIT)
		err := params.Read(req)
		if err != nil {
			panic(err)
		}
		return params
	}()
	notifyParams = func() stratum.NotifyParams {
		params := stratum.NotifyParams{}
		params.Read(notiFrom(MOCK_NOTIFY))
		return params
	}()

	submitParamsMerkle = func() stratum.Share {
		params := stratum.Share{}
		req := reqFrom(MOCK_MINING_SUBMIT_MERKLE)
		err := params.Read(req)
		if err != nil {
			panic(err)
		}
		return params
	}()
	notifyParamsMerkle = func() stratum.NotifyParams {
		params := stratum.NotifyParams{}
		params.Read(notiFrom(MOCK_NOTIFY_MERKLE))
		return params
	}()
)

// requests
var (
	authorizeReq         = stratum.AuthorizeRequest("2", authorizeParams)
	configureReq         = stratum.ConfigureRequest("1", configureParams)
	subscribeReq         = stratum.SubscribeRequest("3", subscribeParams)
	suggestDifficultyReq = stratum.SuggestDifficultyRequest("4", suggestDiffParams)

	notifyReq = stratum.Notify(notifyParams)
	submitReq = stratum.Submit("5", submitParams)

	notifyReqMerkle = stratum.Notify(notifyParamsMerkle)
	submitReqMerkle = stratum.Submit("5", submitParamsMerkle)
)

// data
var (
	MOCK_GET_BLOCK = func() *btcjson.GetBlockVerboseResult {
		gb := btcjson.GetBlockVerboseResult{}
		file, err := os.ReadFile("./mockdata/mockemptygetblock.json")
		if err != nil {
			panic(err)
		}
		json.Unmarshal(file, &gb)
		return &gb
	}()
	MOCK_RAW_TRANSACTION = func() *btcjson.TxRawResult {
		grt := btcjson.TxRawResult{}
		file, err := os.ReadFile("./mockdata/mockemptygetrawtransaction.json")
		if err != nil {
			panic(err)
		}
		json.Unmarshal(file, &grt)
		return &grt
	}()
	MOCK_BLOCK_TEMPLATE = func() *btcjson.GetBlockTemplateResult {
		gbt := btcjson.GetBlockTemplateResult{}
		file, err := os.ReadFile("./mockdata/mockemptytemplate.json")
		if err != nil {
			panic(err)
		}
		json.Unmarshal(file, &gbt)
		return &gbt
	}()

	MOCK_GET_BLOCK_MERKLE = func() *btcjson.GetBlockVerboseResult {
		gb := btcjson.GetBlockVerboseResult{}
		file, err := os.ReadFile("./mockdata/mockgetblock.json")
		if err != nil {
			panic(err)
		}
		json.Unmarshal(file, &gb)
		return &gb
	}()
	MOCK_RAW_TRANSACTION_MERKLE = func() *btcjson.TxRawResult {
		grt := btcjson.TxRawResult{}
		file, err := os.ReadFile("./mockdata/mockgetrawtransaction.json")
		if err != nil {
			panic(err)
		}
		json.Unmarshal(file, &grt)
		return &grt
	}()
	MOCK_BLOCK_TEMPLATE_MERKLE = func() *btcjson.GetBlockTemplateResult {
		gbt := btcjson.GetBlockTemplateResult{}
		file, err := os.ReadFile("./mockdata/mocktemplate.json")
		if err != nil {
			panic(err)
		}
		json.Unmarshal(file, &gbt)
		return &gbt
	}()
)

func reqFrom(r string) *stratum.Request {
	req := stratum.Request{}
	req.Unmarshal([]byte(r))
	return &req
}
func notiFrom(r string) *stratum.Notification {
	req := stratum.Notification{}
	req.Unmarshal([]byte(r))
	return &req
}
func getAddr() btcutil.Address {
	addr, _ := btcutil.DecodeAddress(MOCK_ADDRESS, MOCK_CHAIN)
	return addr
}
func getCoinbaseTx() *btcutil.Tx {
	addr := getAddr()
	job := main.CreateJobTemplate(MOCK_BLOCK_TEMPLATE)
	tx := main.CreateCoinbaseTx(addr, job.Block, job.Subsidy, MOCK_CHAIN)
	return tx
}
