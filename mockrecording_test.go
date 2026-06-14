package main

// this file is just for storing the data as variables
import (
	"git.0xf0xx0.eth.limo/0xf0xx0/stratum"
	"github.com/btcsuite/btcd/btcutil"

	"encoding/json"
	"os"

	"github.com/btcsuite/btcd/btcjson"
	"github.com/btcsuite/btcd/chaincfg"
)

// recorded from/with pogolo!
var (
	MOCK_CHAIN = &chaincfg.RegressionNetParams
)

var (
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

var (
	MOCK_ADDRESS                   = "bcrt1qa5q9y9h7vndg6sske6u02wdw27y4yfg578unks"
	MOCK_MINING_AUTHORIZE          = `{"id": 1, "method": "mining.authorize", "params": ["bcrt1qa5q9y9h7vndg6sske6u02wdw27y4yfg578unks.fakeminer", "x"]}`
	MOCK_MINING_CONFIGURE          = `{"id": 2, "method": "mining.configure", "params": [["version-rolling"], {"version-rolling.mask": "ffffffff"}]}`
	MOCK_MINING_SUGGEST_DIFFICULTY = `{"id": 3, "method": "mining.suggest_difficulty", "params": [0.16]}`
	MOCK_MINING_SUBSCRIBE          = `{"id": 4, "method": "mining.subscribe", "params": ["bitaxe/FTXGOXX/v2021-08-24"]}`

	MOCK_EXTRANONCE    = "1df729b0"
	MOCK_MINING_SUBMIT = `{"method": "mining.submit", "params": ["fakeminer", "1", "00000000", "6a2e0b5d", "1f8fe700"], "id":4}`
	MOCK_NOTIFY        = `{"method":"mining.notify","params":["1","ad6009d4c54693de7ae8dc3a3c5fdb3e56dd5a77b6b3f0d9d514859700000000","01000000010000000000000000000000000000000000000000000000000000000000000000ffffffff2c012901941e2f706f676f6c6f202d20646563656e7472616c697a65206f72206469652f08","feffffff020000000000000000266a24aa21a9ede2f61c3f71d1defd3fa999dfa36953755c690689799962b48bebd836974e8cf900f2052a01000000160014ed005216fe64da8d4216ceb8f539ae578952251428000000",[],"20000000","207fffff","6a2e0b5d",true]}`
	MOCK_SHAREDIFF     = float64(0.55803) /// full float from cpuminer

	// prng seeds for template
	MOCK_SEEDA = uint64(6436127536535422497)
	MOCK_SEEDB = uint64(1576712319095203941)
)

// params
var (
	authorizeParams = func() stratum.MiningAuthorizeParams {
		params := stratum.MiningAuthorizeParams{}
		params.FromRequest(reqFrom(MOCK_MINING_AUTHORIZE))
		return params
	}()
	configureParams = func() stratum.MiningConfigureParams {
		params := stratum.MiningConfigureParams{}
		params.FromRequest(reqFrom(MOCK_MINING_CONFIGURE))
		return params
	}()
	subscribeParams = func() stratum.MiningSubscribeParams {
		params := stratum.MiningSubscribeParams{}
		params.FromRequest(reqFrom(MOCK_MINING_SUBSCRIBE))
		return params
	}()
	suggestDiffParams = func() stratum.MiningSuggestDifficultyParams {
		params := stratum.MiningSuggestDifficultyParams{}
		params.FromRequest(reqFrom(MOCK_MINING_SUGGEST_DIFFICULTY))
		return params
	}()

	submitParams = func() stratum.MiningSubmitParams {
		params := stratum.MiningSubmitParams{}
		req := reqFrom(MOCK_MINING_SUBMIT)
		err := params.FromRequest(req)
		if err != nil {
			panic(err)
		}
		return params
	}()
	notifyParams = func() stratum.MiningNotifyParams {
		params := stratum.MiningNotifyParams{}
		err := params.FromNotification(notiFrom(MOCK_NOTIFY))
		if err != nil {
			panic(err)
		}
		return params
	}()
)

// requests
var (
	authorizeReq         = authorizeParams.ToRequest(1)
	configureReq         = configureParams.ToRequest(2)
	suggestDifficultyReq = suggestDiffParams.ToRequest(3)
	subscribeReq         = subscribeParams.ToRequest(4)

	notifyReq = notifyParams.ToNotification()
	submitReq = submitParams.ToRequest(5)
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
	tx := fillCoinbaseTx(addr, btcutil.NewBlock(&job.MsgBlock), job.Subsidy, MOCK_CHAIN)
	return tx
}
