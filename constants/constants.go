package constants

import (
	"github.com/0xf0xx0/stratum"
)

const (
	VERSION_ROLLING_MASK = 0x1fffe000 // bip 320
	EXTRANONCE_SIZE      = 4          // bytes
	DEFAULT_DIFFICULTY   = 1024
	DEFAULT_COINBASE_TAG = "/pogolo - foss is freedom/"
	MIN_DIFFICULTY       = 0.01 // soft min for clients, server can set whatever
	SUBMISSION_DELTA_WINDOW = 32 // rolling avg window
)

// errors can be anything, so i chose http-ish codes :3
// don't add these to your mappings yet, wait till 1.0.0
var (
	ERROR_INTERNAL   = stratum.Error{Code: 500, Message: "internal server error"}
	ERROR_UNK_METHOD = stratum.Error{Code: 501, Message: "unknown method"}

	// client errors
	ERROR_NOT_SUBBED = stratum.Error{Code: 401, Message: "not subscribed"}
	// for data we understand but will ignore, optionally disconnecting
	// MAYBE: split into reject-no-dc and reject-with-dc?
	ERROR_NOT_ACCEPTED = stratum.Error{Code: 403, Message: "not accepted"}
	ERROR_DIFF_TOO_LOW = stratum.Error{Code: 406, Message: "difficulty too low"}
	ERROR_UNK_JOB      = stratum.Error{Code: 410, Message: "unknown job"}
)
