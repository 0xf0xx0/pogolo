package stratumclient_test

// just for storing the data as variables
import (
	"encoding/json"
	"fmt"
	"os"
	"pogolo/stratumclient"

	"github.com/btcsuite/btcd/btcjson"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	stratum "github.com/kbnchk/go-Stratum"
)

// recorded from/with https://github.com/benjamin-wilson/public-pool
const (
	MOCK_ADDRESS                   = "bcrt1qv2w0jh49962fc0qw63aqlw6p567qkx2dj5kpg4"
	MOCK_EXTRANONCE                = "00353c99"
	MOCK_MINING_SUBSCRIBE          = `{"id": 1, "method": "mining.subscribe", "params": ["bitaxe/FTXGOXX/v2021-08-24"]}`
	MOCK_MINING_CONFIGURE          = `{"id": 2, "method": "mining.configure", "params": [["version-rolling"], {"version-rolling.mask": "ffffffff"}]}`
	MOCK_MINING_AUTHORIZE          = `{"id": 3, "method": "mining.authorize", "params": ["bcrt1qv2w0jh49962fc0qw63aqlw6p567qkx2dj5kpg4.fakeminer", "x"]}`
	MOCK_MINING_SUGGEST_DIFFICULTY = `{"id": 4, "method": "mining.suggest_difficulty", "params": [2048]}`
	MOCK_MINING_SUBMIT             = `{"id": 5, "method": "mining.submit", "params": ["bcrt1qv2w0jh49962fc0qw63aqlw6p567qkx2dj5kpg4.fakeminer", "f", "00000000", "687c7dcc", "d3332dc7"]}`
	MOCK_TIME                      = "687c7dcc"
	MOCK_NOTIFY                    = `{"id":null,"method":"mining.notify","params":["f","6f83e6beec01061d3f16296355b38e5d2bd1189e3d70dbf08ea0028d00000005","01000000010000000000000000000000000000000000000000000000000000000000000000ffffffff2602f8012f706f676f6c6f202d20666f73732069732066726565646f6d2f00","ffffffff0240be402500000000160014629cf95ea52e949c3c0ed47a0fbb41a6bc0b194d0000000000000000266a24aa21a9ede2f61c3f71d1defd3fa999dfa36953755c690689799962b48bebd836974e8cf900000000",[],"20000000","207fffff","687c7dcc",true]}`
	MOCK_COINBASE                  = "020000000001010000000000000000000000000000000000000000000000000000000000000000ffffffff26011a2f706f676f6c6f202d20666f73732069732066726565646f6d2f0000a60a68bb00000000ffffffff0200f2052a01000000160014629cf95ea52e949c3c0ed47a0fbb41a6bc0b194d0000000000000000266a24aa21a9ede2f61c3f71d1defd3fa999dfa36953755c690689799962b48bebd836974e8cf90120000000000000000000000000000000000000000000000000000000000000000000000000"
	MOCK_EMPTY_COINBASE            = "010000000001010000000000000000000000000000000000000000000000000000000000000000ffffffff00ffffffff000120000000000000000000000000000000000000000000000000000000000000000000000000"
	MOCK_BLOCKHASH                 = "00000004639cb7c8ec146209f6ad156768dd8d23be8f79981039e092fee5e9d2"
)

var (
	MOCK_CHAIN = &chaincfg.RegressionNetParams
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
		println(len(req.Params))
		println(fmt.Sprintf("%+v", req.Params))
		err := params.Read(req)
		if err != nil {
			panic(err)
		}
		println(fmt.Sprintf("submit: %+v", MOCK_MINING_SUBMIT))
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
	authorizeReq         = stratum.AuthorizeRequest("2", authorizeParams)
	configureReq         = stratum.ConfigureRequest("1", configureParams)
	subscribeReq         = stratum.SubscribeRequest("3", subscribeParams)
	suggestDifficultyReq = stratum.SuggestDifficultyRequest("4", suggestDiffParams)
	submitReq            = stratum.Submit("5", submitParams)

	notifyReq = stratum.Notify(notifyParams)
)

// mock recording values from https://github.com/benjamin-wilson/public-pool
var MOCK_BLOCK_TEMPLATE = func() *btcjson.GetBlockTemplateResult {
	gbt := btcjson.GetBlockTemplateResult{}
	file, err := os.ReadFile("./mocktemplate.json")
	if err != nil {
		panic(err)
	}
	json.Unmarshal(file, &gbt)
	return &gbt
}()

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
	// en2, _ := strconv.ParseInt("0f100f", 16, 32)
	job := stratumclient.CreateJobTemplate(getBlockTemplate())
	tx := stratumclient.CreateCoinbaseTx(addr, *job, MOCK_CHAIN)
	return tx
}
func getBlockTemplate() *btcjson.GetBlockTemplateResult {
	return MOCK_BLOCK_TEMPLATE
}
