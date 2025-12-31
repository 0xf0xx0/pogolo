package main

// this file is just for storing the data as variables
import (
	"encoding/json"
	"os"

	"git.0xf0xx0.eth.limo/0xf0xx0/stratum"
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
	MOCK_EXTRANONCE    = "7f47c29b"
	MOCK_MINING_SUBMIT = `{"method": "mining.submit", "params": ["bcrt1qv2w0jh49962fc0qw63aqlw6p567qkx2dj5kpg4.fakeminer", "2", "00000000", "69120472", "2f16a782"], "id":5}`
	MOCK_NOTIFY        = `{"id":0,"method":"mining.notify","params":["2","e4c204c7f5e22f60656b203a2f2ec8bb8e6f34803dc185f8a1d7bcac00000004","01000000010000000000000000000000000000000000000000000000000000000000000000ffffffff2b024f011e2f706f676f6c6f202d20646563656e7472616c697a65206f72206469652f08","ffffffff020000000000000000266a24aa21a9ede2f61c3f71d1defd3fa999dfa36953755c690689799962b48bebd836974e8cf9807c814a00000000160014629cf95ea52e949c3c0ed47a0fbb41a6bc0b194d00000000",[],"30000000","207fffff","69120472",true]}`
	MOCK_COINBASE      = "010000000001010000000000000000000000000000000000000000000000000000000000000000ffffffff2b024f011e2f706f676f6c6f202d20646563656e7472616c697a65206f72206469652f087f47c29b00000000ffffffff020000000000000000266a24aa21a9ede2f61c3f71d1defd3fa999dfa36953755c690689799962b48bebd836974e8cf9807c814a00000000160014629cf95ea52e949c3c0ed47a0fbb41a6bc0b194d0120000000000000000000000000000000000000000000000000000000000000000000000000"
	MOCK_BLOCKHASH     = "00000000fa3596b84f4a625ae3767dd680882c7379f0386732083e40ee38257d"
	MOCK_SHAREDIFF     = float64(1.023127685412636)
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
)

// requests
var (
	subscribeReq         = stratum.SubscribeRequest(1, subscribeParams)
	configureReq         = stratum.ConfigureRequest(2, configureParams)
	authorizeReq         = stratum.AuthorizeRequest(3, authorizeParams)
	suggestDifficultyReq = stratum.SuggestDifficultyRequest(4, suggestDiffParams)

	notifyReq = stratum.Notify(notifyParams)
	submitReq = stratum.Submit(5, submitParams)
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
	job, _ := CreateJobTemplate(MOCK_BLOCK_TEMPLATE)
	tx := FillCoinbaseTx(addr, btcutil.NewBlock(&job.MsgBlock), job.Subsidy, MOCK_CHAIN)
	return tx
}
