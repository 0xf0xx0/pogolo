package config

import "github.com/btcsuite/btcd/chaincfg"

const (
	VERSION_ROLLING_MASK = 0x1fffe000 // bip 320
	EXTRANONCE_SIZE      = 4          // bytes
	DEFAULT_DIFFICULTY   = 1024
)

var (
	COINBASE_TAG = "/pogolo - foss is freedom/"
	CHAIN        = &chaincfg.RegressionNetParams
)

type Config struct {
	Backend Backend `toml:"backend"`
	Pogolo  Pogolo  `toml:"pogolo"`
}
type Backend struct {
	Host    string `toml:"host" comment:"RPC host:port"`
	Cookie  string `toml:"cookie,commented" comment:"RPC cookie path"`
	Rpcauth string `toml:"rpcauth,commented" comment:"RPC auth user:pass"`
	Chain   string `toml:"chain" comment:"mainnet | testnet | regtest"`
}
type Pogolo struct {
	Host              string  `toml:"host" comment:"host:port to listen on"`
	Password          string  `toml:"password,commented" comment:"optional, required for clients if set"`
	Tag               string  `toml:"tag" comment:"will be replaced by default tag if too long (see coinbase scriptsig limit)"`
	DefaultDifficulty float64 `toml:"default_difficulty" comment:"minimum 0.16"`
}
