package config

import "github.com/btcsuite/btcd/chaincfg"
const (
	VERSION_ROLLING_MASK = 0x1fffe000 // bip 320
	EXTRANONCE_SIZE = 4 // bytes
	DEFAULT_DIFFICULTY = 1024
)
var (
	COINBASE_TAG = "/pogolo - foss is freedom/"
	CHAIN = &chaincfg.RegressionNetParams
)
type Config struct {
	// Host `toml:"theme" comment:"Supports everything display.format does"`
}
