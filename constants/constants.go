package constants

import (
	"time"

	"git.0xf0xx0.eth.limo/0xf0xx0/stratum"
	"git.0xf0xx0.eth.limo/0xf0xx0/stratumv2"
	"github.com/btcsuite/btcd/chaincfg/v2"
	"github.com/btcsuite/btcd/chainhash/v2"
	"github.com/btcsuite/btcd/wire/v2"
)

const (
	VERSION_ROLLING_MASK = uint32(0x1fffffe0) // bip 323 constant
	EXTRANONCE_SIZE      = 4                  // in bytes
	DEFAULT_DIFFICULTY   = 1024               // for gpu, fpga, and asic miners
	DEFAULT_COINBASE_TAG = "/pogolo - decentralize or die/"
	MIN_DIFFICULTY       = 0.16        // hard min
	HASHRATE_WINDOW      = uint64(600) // 10 min windows
)

// exit codes
const (
	EXIT_NONE = iota
	EXIT_MISC
	EXIT_BACKEND
	EXIT_CONFIG
	EXIT_NET
)

// errors can be anything, so i chose http-ish codes :3
var (
	// server errors

	// unknown stratum method
	ERROR_UNK_METHOD = stratum.Error{Code: 501, Message: "Unknown method"}
	// unsupported stratum method
	ERROR_UNSUPP_METHOD = stratum.Error{Code: 502, Message: "Unsupported method"}

	// client errors

	// submission before subscription
	ERROR_NOT_SUBBED   = stratum.Error{Code: 401, Message: "Not subscribed"}
	ERROR_UNAUTHORIZED = stratum.Error{Code: 403, Message: "Unauthorized"}
	ERROR_INV_CHAN_ID  = stratum.Error{Code: 404, Message: "Invalid channel ID"}
	// for data we understand but will ignore, optionally disconnecting
	ERROR_NOT_ACCEPTED = stratum.Error{Code: 406, Message: "Not accepted"}
	// normal mining errors
	ERROR_STALE        = stratum.Error{Code: 410, Message: "Stale job"}
	ERROR_INV_VER_MASK = stratum.Error{Code: 412, Message: "Invalid version mask"}
	ERROR_DUPE_SHARE   = stratum.Error{Code: 413, Message: "Duplicate share"}
	ERROR_LOW_DIFF     = stratum.Error{Code: 414, Message: "Difficulty too low"}
	ERROR_BAD_TIME     = stratum.Error{Code: 415, Message: "Invalid ntime"}
	// for data we understand but couldnt process
	ERROR_UNPROCESSABLE = stratum.Error{Code: 422, Message: "Unprocessable content"}
)

var Target1U256 = func() stratumv2.U256 {
	u := stratumv2.U256{}
	// source: https://en.bitcoin.it/wiki/Difficulty#How_is_difficulty_calculated?_What_is_the_difference_between_bdiff_and_pdiff?
	// we wanna use the truncated one to stick to bdiff
	u.SetString("00000000FFFF0000000000000000000000000000000000000000000000000000")
	return u
}()

var genesisHash = chainhash.Hash([chainhash.HashSize]byte{
	0x97, 0xd4, 0x91, 0x4a, 0x67, 0x0d, 0x20, 0x4b,
	0x47, 0x6a, 0xff, 0x6f, 0xfd, 0x46, 0x45, 0x3d,
	0x33, 0x1c, 0xdc, 0x52, 0x32, 0x7d, 0x7e, 0x63,
	0xc1, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
})
var TestNet5Params = chaincfg.Params{
	Name:        "testnet5",
	Net:         0x46495645,
	DefaultPort: "18353",
	DNSSeeds:    []chaincfg.DNSSeed{},
	GenesisBlock: &wire.MsgBlock{
		Header: wire.BlockHeader{
			Version:   0x2230c000,
			PrevBlock: chainhash.Hash{},
			MerkleRoot: chainhash.Hash([chainhash.HashSize]byte{
				0x8c, 0x58, 0x99, 0xb6, 0x5c, 0x5b, 0x08, 0x34,
				0xad, 0xb1, 0x7a, 0xf3, 0x14, 0xe2, 0x2a, 0x9b,
				0xf0, 0x67, 0xe7, 0xed, 0x81, 0x44, 0xc3, 0x79,
				0x67, 0xb5, 0xec, 0x56, 0xb5, 0xa6, 0xe2, 0x8d,
			}),
			Timestamp: time.Unix(1794632593, 0),
			Bits:      0x1a0fffff,
			Nonce:     0x653d9131,
		},
		Transactions: []*wire.MsgTx{&wire.MsgTx{
			Version: 1,
			TxIn: []*wire.TxIn{
				&wire.TxIn{
					PreviousOutPoint: wire.OutPoint{
						Hash:  chainhash.Hash{},
						Index: 0xffffffff,
					},
					SignatureScript: []byte{

						0x04, 0xff, 0xff, 0x00, 0x1d, 0x10, 0x04, 0x4c,
						0x52, 0x32, 0x30, 0x32, 0x36, 0x2d, 0x31, 0x31,
						0x2d, 0x31, 0x34, 0x20, 0x39, 0x37, 0x35, 0x37,
						0x34, 0x34, 0x20, 0x30, 0x30, 0x30, 0x30, 0x30,
						0x30, 0x30, 0x30, 0x30, 0x30, 0x30, 0x30, 0x30,
						0x30, 0x30, 0x30, 0x30, 0x30, 0x30, 0x31, 0x32,
						0x33, 0x65, 0x32, 0x36, 0x38, 0x31, 0x65, 0x32,
						0x66, 0x38, 0x32, 0x61, 0x63, 0x64, 0x63, 0x62,
						0x39, 0x34, 0x33, 0x30, 0x61, 0x31, 0x35, 0x65,
						0x65, 0x63, 0x32, 0x34, 0x63, 0x63, 0x38, 0x35,
						0x62, 0x34, 0x30, 0x30, 0x36, 0x39, 0x31, 0x64,
						0x34, 0x37, 0x39,
					},
					Sequence: 0xffffffff,
				},
			},
			TxOut: []*wire.TxOut{
				&wire.TxOut{
					Value: 5000000000,
					PkScript: []byte{
						0x6a, 0x08, 0x95, 0xeb,
						0xf7, 0x6a, 0x02, 0x00,
						0x00, 0x00},
				},
			},
			LockTime: 0,
		}},
	},
	GenesisHash:              &genesisHash,
	PowLimit:                 chaincfg.TestNet4Params.PowLimit,
	PowLimitBits:             0x1a0fffff,
	EnforceBIP94:             false,
	BIP0034Height:            1,
	BIP0065Height:            1,
	BIP0066Height:            1,
	CoinbaseMaturity:         100,
	SubsidyReductionInterval: 210000,
	TargetTimespan:           time.Hour * 24 * 14,
	TargetTimePerBlock:       time.Minute * 10,
	RetargetAdjustmentFactor: 4,
	ReduceMinDifficulty:      false,
	GenerateSupported:        false,

	Checkpoints: []chaincfg.Checkpoint{},

	RuleChangeActivationThreshold: 1512,
	MinerConfirmationWindow:       2016,
	Deployments: [chaincfg.DefinedDeployments]chaincfg.ConsensusDeployment{
		chaincfg.DeploymentTestDummy: chaincfg.ConsensusDeployment{
			BitNumber: 28,
			DeploymentStarter: chaincfg.NewMedianTimeDeploymentStarter(
				time.Unix(1199145601, 0),
			),
			DeploymentEnder: chaincfg.NewMedianTimeDeploymentEnder(
				time.Unix(1230767999, 0),
			),
		},
		chaincfg.DeploymentTestDummyMinActivation: chaincfg.ConsensusDeployment{
			BitNumber:                 22,
			CustomActivationThreshold: 1815,
			MinActivationHeight:       10_0000,
			DeploymentStarter: chaincfg.NewMedianTimeDeploymentStarter(
				time.Time{},
			),
			DeploymentEnder: chaincfg.NewMedianTimeDeploymentEnder(
				time.Time{},
			),
		},
		chaincfg.DeploymentTestDummyAlwaysActive: chaincfg.ConsensusDeployment{
			BitNumber: 30,
			DeploymentStarter: chaincfg.NewMedianTimeDeploymentStarter(
				time.Time{},
			),
			DeploymentEnder: chaincfg.NewMedianTimeDeploymentEnder(
				time.Time{},
			),
			AlwaysActiveHeight: 1,
		},
		chaincfg.DeploymentCSV: chaincfg.ConsensusDeployment{
			BitNumber: 31,
			DeploymentStarter: chaincfg.NewMedianTimeDeploymentStarter(
				time.Time{},
			),
			DeploymentEnder: chaincfg.NewMedianTimeDeploymentEnder(
				time.Time{},
			),
			AlwaysActiveHeight: 1,
		},
		chaincfg.DeploymentSegwit: chaincfg.ConsensusDeployment{
			BitNumber: 29,
			DeploymentStarter: chaincfg.NewMedianTimeDeploymentStarter(
				time.Time{},
			),
			DeploymentEnder: chaincfg.NewMedianTimeDeploymentEnder(
				time.Time{},
			),
			AlwaysActiveHeight: 1,
		},
		chaincfg.DeploymentTaproot: chaincfg.ConsensusDeployment{
			BitNumber: 2,
			DeploymentStarter: chaincfg.NewMedianTimeDeploymentStarter(
				time.Time{},
			),
			DeploymentEnder: chaincfg.NewMedianTimeDeploymentEnder(
				time.Time{},
			),
			MinActivationHeight: 0,
			AlwaysActiveHeight:  1,
		},
	},

	RelayNonStdTxs: true,

	Bech32HRPSegwit: "tb",

	PubKeyHashAddrID:        0x6f,
	ScriptHashAddrID:        0xc4,
	WitnessPubKeyHashAddrID: 0x03,
	WitnessScriptHashAddrID: 0x28,
	PrivateKeyID:            0xef,

	HDPrivateKeyID: [4]byte{0x04, 0x35, 0x83, 0x94},
	HDPublicKeyID:  [4]byte{0x04, 0x35, 0x87, 0xcf},

	HDCoinType: 1,
}
