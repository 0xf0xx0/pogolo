package constants

import (
	"github.com/0xf0xx0/stratum"
)

const (
	VERSION_ROLLING_MASK         = 0x1fffe000 // bip 320 constant
	EXTRANONCE_SIZE              = 4          // in bytes
	DEFAULT_DIFFICULTY           = 1024       // for gpu, fpga, and asic miners
	DEFAULT_DIFFICULTY_LOW_POWER = 0.16       // for cpu miners
	DEFAULT_COINBASE_TAG         = "/pogolo - decentralize or die/"
	MIN_DIFFICULTY               = 0.01       // hard min
	SUBMISSION_DELTA_WINDOW      = 32         // rolling avg window, also the diff adjust period
	HASHRATE_WINDOW              = int64(600) // 10 min windows
)

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

	// internal server error
	ERROR_INTERNAL = stratum.Error{Code: 500, Message: "Internal server error"}
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
	ERROR_UNK_JOB      = stratum.Error{Code: 410, Message: "Unknown job"}
	ERROR_LOW_DIFF     = stratum.Error{Code: 413, Message: "Difficulty too low"}
	// for data we understand but couldnt process
	ERROR_UNPROCESSABLE = stratum.Error{Code: 422, Message: "Unprocessable content"}
)
