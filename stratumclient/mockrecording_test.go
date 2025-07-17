package stratumclient_test
// just for storing the data as variables
import (
	"encoding/json"
	"os"

	"github.com/btcsuite/btcd/btcjson"
)

// mock recording values from https://github.com/benjamin-wilson/public-pool
const (
	MOCK_EXTRANONCE                = "57a6f098"
	MOCK_MINING_SUBSCRIBE          = `{"id": 1, "method": "mining.subscribe", "params": ["bitaxe v2.2"]}\n`
	MOCK_MINING_CONFIGURE          = `{"id": 2, "method": "mining.configure", "params": [["version-rolling"], {"version-rolling.mask": "ffffffff"}]}\n`
	MOCK_MINING_AUTHORIZE          = `{"id": 3, "method": "mining.authorize", "params": ["tb1qumezefzdeqqwn5zfvgdrhxjzc5ylr39uhuxcz4.bitaxe3", "x"]}\n`
	MOCK_MINING_SUGGEST_DIFFICULTY = `{"id": 4, "method": "mining.suggest_difficulty", "params": [512]}\n`
	MOCK_MINING_SUBMIT             = `{"id": 5, "method": "mining.submit", "params": ["tb1qumezefzdeqqwn5zfvgdrhxjzc5ylr39uhuxcz4.bitaxe3", "1", "c7080000", "64b3f3ec", "ed460d91", "00002000"]}\n`
	MOCK_TIME                      = "64b3f3ec"
	MOCK_NOTIFY = `{"id":null,"method":"mining.notify","params":["1","171592f223740e92d223f6e68bff25279af7ac4f2246451e0000000200000000","02000000010000000000000000000000000000000000000000000000000000000000000000ffffffff1903c943255c7075626c69632d706f6f6c5c","ffffffff037a90000000000000160014e6f22ca44dc800e9d049621a3b9a42c509f1c4bc3b0f250000000000160014e6f22ca44dc800e9d049621a3b9a42c509f1c4bc0000000000000000266a24aa21a9edbd3d1d916aa0b57326a2d88ebe1b68a1d7c48585f26d8335fe6a94b62755f64c00000000",["175335649d5e8746982969ec88f52e85ac9917106fba5468e699c8879ab974a1","d5644ab3e708c54cd68dc5aedc92b8d3037449687f92ec41ed6e37673d969d4a","5c9ec187517edc0698556cca5ce27e54c96acb014770599ed9df4d4937fbf2b0"],"20000000","192495f8","64b3f3ec",false]}`
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
