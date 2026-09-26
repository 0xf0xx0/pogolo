package main

// this file is just for storing the data as variables
import (
	_ "embed"
	"testing"

	"git.0xf0xx0.eth.limo/0xf0xx0/stratum"
	"git.0xf0xx0.eth.limo/0xf0xx0/stratumv2"
	"github.com/btcsuite/btcd/address/v2"
	"github.com/btcsuite/btcd/chainhash/v2"
	"github.com/btcsuite/btcd/wire/v2"

	"encoding/hex"
	"encoding/json"

	"github.com/btcsuite/btcd/btcjson"
	"github.com/btcsuite/btcd/chaincfg/v2"
)

// recorded from/with pogolo!
var (
	MOCK_CHAIN = &chaincfg.RegressionNetParams
)

var (
	//go:embed mockdata/mockemptytemplate.json
	mockTemplateString  []byte
	MOCK_BLOCK_TEMPLATE = func() *btcjson.GetBlockTemplateResult {
		gbt := btcjson.GetBlockTemplateResult{}
		json.Unmarshal(mockTemplateString, &gbt)
		return &gbt
	}()
	// prng seeds for template
	MOCK_SEEDA = uint64(0x9e6826a50ba55d27)
	MOCK_SEEDB = uint64(0x910a50e0af0acbb5)
)

var (
	MOCK_ADDRESS                   = "bcrt1qa5q9y9h7vndg6sske6u02wdw27y4yfg578unks"
	MOCK_MINING_AUTHORIZE          = `{"id": 1, "method": "mining.authorize", "params": ["bcrt1qa5q9y9h7vndg6sske6u02wdw27y4yfg578unks.fakeminer", "x"]}`
	MOCK_MINING_CONFIGURE          = `{"id": 2, "method": "mining.configure", "params": [["version-rolling"], {"version-rolling.mask": "ffffffff"}]}`
	MOCK_MINING_SUGGEST_DIFFICULTY = `{"id": 3, "method": "mining.suggest_difficulty", "params": [0.16]}`
	MOCK_MINING_SUBSCRIBE          = `{"id": 4, "method": "mining.subscribe", "params": ["bitaxe/FTXGOXX/v2021-08-24"]}`

	MOCK_EXTRANONCE    = "8357f44c"
	MOCK_MINING_SUBMIT = `{"method": "mining.submit", "params": ["fakeminer", "1", "01000000", "6ab83ac5", "b268feba"], "id":4}`
	MOCK_NOTIFY        = `{"method":"mining.notify","params":["1","16585a5708ccf4118a07ad29ad3cb72f9c8290d278830dcef941c2c100000000","01000000010000000000000000000000000000000000000000000000000000000000000000ffffffff2c027d011f2f706f676f6c6f202d20646563656e7472616c697a65206f72206469652f4408","feffffff020000000000000000266a24aa21a9ede2f61c3f71d1defd3fa999dfa36953755c690689799962b48bebd836974e8cf9807c814a00000000160014ed005216fe64da8d4216ceb8f539ae57895225147c010000",[],"30000000","207fffff","6ab83ac5",true],"id":null}`
	MOCK_SHAREHASH     = func() chainhash.Hash {
		h := &chainhash.Hash{}
		/// damn a 122 diff on a cpu
		shareHash := hexDec("0000000002170e9659ce1e5faa6f405d452bfba4a59846f1fe201199f27367cc")
		for i := range 16 {
			shareHash[i], shareHash[31-i] = shareHash[31-i], shareHash[i]
		}
		err := h.SetBytes(shareHash)
		if err != nil {
			panic(err)
		}
		// println(h.String(), calcDifficulty(*h))
		return *h
	}()
	MOCK_SHAREDIFF = calcDifficulty(MOCK_SHAREHASH)
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
func getAddr() address.Address {
	addr, _ := address.DecodeAddress(MOCK_ADDRESS, MOCK_CHAIN)
	return addr
}
func getCoinbaseTx() *wire.MsgTx {
	addr := getAddr()
	job, _ := CreateJobTemplate(MOCK_BLOCK_TEMPLATE)
	en1, _ := stratum.DecodeID(MOCK_EXTRANONCE)
	tx := addCoinbasePayout(en1, addr, job.CoinbaseTx, job.Subsidy)
	return tx
}
func hexDec(s string) []byte {
	x, _ := hex.DecodeString(s)
	return x
}

var (
	MOCK_SETUPCONNECTION = func() stratumv2.Frame {
		payload := hexDec("")
		f := stratumv2.Frame{
			MessageType:   stratumv2.MessageSetupConnection,
			MessageLength: stratumv2.U24(len(payload)),
			Payload:       payload,
		}
		return f
	}()
	MOCK_OPENEXTENDEDCHANNEL = func() stratumv2.Frame {
		payload := hexDec("")
		f := stratumv2.Frame{
			MessageType:   stratumv2.MessageOpenExtendedMiningChannel,
			MessageLength: stratumv2.U24(len(payload)),
			Payload:       payload,
		}
		return f
	}()
	MOCK_SV2_SUBMIT = func() stratumv2.Frame {
		payload := hexDec("")
		f := stratumv2.Frame{
			MessageType:   stratumv2.MessageSubmitSharesExtended,
			MessageLength: stratumv2.U24(len(payload)),
			Payload:       payload,
		}
		return f
	}()
)

func encodeSv2(s stratumv2.Codable, t testing.TB) []byte {
	b, err := s.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return b
}
