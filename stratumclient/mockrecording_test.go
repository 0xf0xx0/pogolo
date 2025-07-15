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
