package constants

import ("github.com/btcsuite/btcd/chaincfg"
	"github.com/0xf0xx0/stratum")

const (
	VERSION_ROLLING_MASK = 0x1fffe000 // bip 320
	EXTRANONCE_SIZE      = 4          // bytes
	DEFAULT_DIFFICULTY   = 1024
	DEFAULT_COINBASE_TAG = "/pogolo - foss is freedom/"
	MIN_DIFFICULTY = 0.16 // soft min for clients, server can set whatever
)

// errors can be anything, so i chose http-ish codes :3
var (
	ERROR_INTERNAL = stratum.Error{Code: 500, Message: "internal server error"}
	ERROR_UNK_METHOD = stratum.Error{Code: 501, Message: "unknown method"}

	// client errors
	ERROR_NOT_SUBBED = stratum.Error{Code: 401, Message: "not subscribed"}
	ERROR_DIFF_TOO_LOW = stratum.Error{Code: 406, Message: "difficulty too low"}
	ERROR_UNK_JOB = stratum.Error{Code:410, Message: "unknown job"}
)

var (
	DEFAULT_CHAIN                = &chaincfg.RegressionNetParams
)
