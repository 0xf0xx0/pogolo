package main

// this file is just for storing the data and initing test state
import (
	"crypto/rand"
	_ "embed"
	"testing"

	"git.0xf0xx0.eth.limo/0xf0xx0/pogolo/constants"
	"git.0xf0xx0.eth.limo/0xf0xx0/stratum"
	"git.0xf0xx0.eth.limo/0xf0xx0/stratumv2"
	"github.com/btcsuite/btcd/address/v2"
	"github.com/btcsuite/btcd/wire/v2"

	"encoding/hex"
	"encoding/json"

	"github.com/btcsuite/btcd/btcjson"
	"github.com/btcsuite/btcd/chaincfg/v2"
)

var (
	//go:embed mockdata/mockemptytemplate.json
	mockTemplateString  []byte
	MOCK_BLOCK_TEMPLATE = func() *btcjson.GetBlockTemplateResult {
		gbt := btcjson.GetBlockTemplateResult{}
		json.Unmarshal(mockTemplateString, &gbt)
		return &gbt
	}()
	MOCK_CHAIN = &chaincfg.RegressionNetParams
)

var (
	MOCK_MINING_AUTHORIZE          = `{"id": 1, "method": "mining.authorize", "params": ["fakeminer", "x"]}`
	MOCK_MINING_CONFIGURE          = `{"id": 2, "method": "mining.configure", "params": [["version-rolling"], {"version-rolling.mask": "ffffffff"}]}`
	MOCK_MINING_SUGGEST_DIFFICULTY = `{"id": 3, "method": "mining.suggest_difficulty", "params": [0.16]}`
	MOCK_MINING_SUBSCRIBE          = `{"id": 4, "method": "mining.subscribe", "params": ["bitaxe/FTXGOXX/v2021-08-24"]}`
	MOCK_MINING_SUBMIT             = `{"method": "mining.submit", "params": ["fakeminer", "1", "01000000", "6ab83ac5", "b268feba"], "id":4}`
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
)

// requests
var (
	authorizeReq         = authorizeParams.ToRequest(1)
	configureReq         = configureParams.ToRequest(2)
	suggestDifficultyReq = suggestDiffParams.ToRequest(3)
	subscribeReq         = subscribeParams.ToRequest(4)
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
	prog := make([]byte, 32)
	rand.Read(prog)

	addr, _ := address.NewAddressTaproot(prog, MOCK_CHAIN)
	return addr
}
func getCoinbaseTx() *wire.MsgTx {
	addr := getAddr()
	job, _ := CreateJobTemplate(MOCK_BLOCK_TEMPLATE)
	tx := addCoinbasePayout(stratum.ID(17), addr, job.CoinbaseTx, job.Subsidy)
	return tx
}
func hexDec(s string) []byte {
	x, _ := hex.DecodeString(s)
	return x
}

var (
	MOCK_SETUPCONNECTION = func() *stratumv2.Frame {
		payload := &stratumv2.SetupConnection{
			Protocol:              stratumv2.MiningProtocol,
			MinVersion:            stratumv2.ProtocolVersion,
			MaxVersion:            stratumv2.ProtocolVersion,
			Flags:                 stratumv2.RequiresVersionRollingFlag,
			EndpointPort:          5661,
			EndpointHost:          "pogolo@umbrel",
			DeviceVendor:          "bitaxe",
			DeviceHardwareVersion: "FTXGOXX",
			DeviceFirmware:        "v2021-08-24",
			DeviceID:              "D-Vize",
		}
		b, _ := payload.Encode()
		f := stratumv2.Frame{
			MessageType:   stratumv2.MessageSetupConnection,
			MessageLength: stratumv2.U24(len(b)),
			Payload:       b,
		}
		return &f
	}()
	MOCK_OPENEXTENDEDCHANNEL = func() *stratumv2.Frame {
		payload := &stratumv2.OpenExtendedMiningChannel{
			OpenStandardMiningChannel: stratumv2.OpenStandardMiningChannel{
				RequestID:       123,
				UserIdentity:    getAddr().EncodeAddress() + ".fakeminer",
				NominalHashRate: 1234567890,
				MaxTarget:       *constants.Target1U256,
			},
			MinExtranonceSize: 0,
		}
		b, _ := payload.Encode()
		f := stratumv2.Frame{
			MessageType:   stratumv2.MessageOpenExtendedMiningChannel,
			MessageLength: stratumv2.U24(len(b)),
			Payload:       b,
		}
		return &f
	}()
)

func encodeSv2(s stratumv2.Codable, t testing.TB) []byte {
	b, err := s.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestMain(m *testing.M) {
	conf = DEFAULT_CONFIG
	conf.DisableLogs = true

	backendChainParams = &chaincfg.RegressionNetParams
	defaultMiningAddr = getAddr()

	currTemplateID = 0
	var err error
	currTemplate, err = CreateJobTemplate(MOCK_BLOCK_TEMPLATE)
	if err != nil {
		panic(err)
	}
	sv2AuthorityKeypair = stratumv2.NewKeypair()
	sv2StaticKeypair = stratumv2.NewKeypair()
	/// cert lasts like a year, lol
	sv2Cert, _ = stratumv2.NewSignedCertificate(sv2AuthorityKeypair.Private, sv2StaticKeypair.PublicKey(), 0, 0)

	clients.Init()
	m.Run()
}
