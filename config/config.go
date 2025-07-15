package config

import "github.com/btcsuite/btcd/chaincfg"
const (
	COINBASE_TAG = "/pogolo - foss is freedom/"
	VERSION_ROLLING_MASK = 0x1fffe000 // bip 320
	DEFAULT_DIFFICULTY = 1024
	EXTRANONCE_SIZE = 4 // bytes
)
var (
	CHAIN = &chaincfg.MainNetParams
)
