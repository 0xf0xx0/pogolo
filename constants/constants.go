package constants

import (
	"math/big"

	"git.0xf0xx0.eth.limo/0xf0xx0/stratum"
)

const (
	VERSION_ROLLING_MASK = uint32(0x1fffffe0) // bip 323 constant
	EXTRANONCE_SIZE      = 4                  // in bytes
	DEFAULT_DIFFICULTY   = 1024               // for gpu, fpga, and asic miners
	DEFAULT_COINBASE_TAG = "/pogolo - decentralize or die/"
	MIN_DIFFICULTY       = 0.16       // hard min
	HASHRATE_WINDOW      = int64(600) // 10 min windows
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
	// for data we understand but will ignore, optionally disconnecting
	ERROR_NOT_ACCEPTED = stratum.Error{Code: 406, Message: "Not accepted"}
	// normal mining errors
	ERROR_STALE              = stratum.Error{Code: 410, Message: "Stale job"}
	ERROR_SHARE_BETWEEN_JOBS = stratum.Error{Code: 411, Message: "Share submitted during job change"}
	ERROR_INV_VER_MASK       = stratum.Error{Code: 412, Message: "Invalid version mask"}
	ERROR_DUPE_SHARE         = stratum.Error{Code: 413, Message: "Duplicate share"}
	ERROR_LOW_DIFF           = stratum.Error{Code: 414, Message: "Difficulty too low"}
	ERROR_BAD_TIME           = stratum.Error{Code: 415, Message: "Invalid ntime"}
	// for data we understand but couldnt process
	ERROR_UNPROCESSABLE = stratum.Error{Code: 422, Message: "Unprocessable content"}
)

// used for diff calc
var TrueDiff1 = func() *big.Float {
	td1 := big.Int{}
	td1.SetString("26959535291011309493156476344723991336010898738574164086137773096960", 10)
	return new(big.Float).SetInt(&td1)
}()
