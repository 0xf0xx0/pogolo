package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/pelletier/go-toml/v2"
	"github.com/urfave/cli/v3"
)

var (
	ROOT = getConfigDir()
)

type Config struct {
	Backend Backend `toml:"backend"`
	Pogolo  Pogolo  `toml:"pogolo"`
}
type Backend struct {
	Host        string `toml:"host" comment:"RPC host:port"`
	Cookie      string `toml:"cookie,commented" comment:"RPC cookie path"`
	Rpcauth     string `toml:"rpcauth,commented" comment:"optional, RPC user/pass"`
	Chain       string `toml:"chain" comment:"mainnet | testnet | regtest"`
	ChainParams *chaincfg.Params // internal
}
type Pogolo struct {
	Interface         string  `toml:"interface" comment:"takes precedence over host, will listen on all ips"`
	Host              string  `toml:"host" comment:"host:port to listen on, preferably lan"`
	Password          string  `toml:"password,commented" comment:"optional, required for clients if set"`
	Tag               string  `toml:"tag" comment:"will be replaced by default tag if too long (see coinbase scriptsig limit)"`
	DefaultDifficulty float64 `toml:"default_difficulty" comment:"minimum 0.16"`
}

var DEFAULT_CONFIG = Config{
	Backend: Backend{
		Host:        "[::1]:8332",
		Cookie:      "~/.bitcoin/regtest/.cookie",
		Chain:       "regtest",
		ChainParams: &chaincfg.RegressionNetParams,
		Rpcauth:     "pogolo:hash",
	},
	Pogolo: Pogolo{
		Host:              "[::1]:5661",
		Tag:               "/pogolo - foss is freedom/",
		DefaultDifficulty: 1024,
	},
}

func LoadConfig(path string, conf *Config) {
	configfile, err := os.Open(path)
	if err != nil {
		println(fmt.Sprintf("failed to load config at %s: %s", path, err))
		return
	}
	d := toml.NewDecoder(configfile)
	d.DisallowUnknownFields()
	if err := d.Decode(conf); err != nil {
		println(fmt.Sprintf("failed to decode config at %s: %s", path, err))
		os.Exit(1)
	}
}
func writeDefaultConfig(path string) error {
	conf, _ := toml.Marshal(DEFAULT_CONFIG)
	if err := os.WriteFile(path, conf, 0755); err != nil {
		return cli.Exit(fmt.Sprintf("couldnt create config file: %s", err), 1)
	}
	return nil
}
func getConfigDir() string {
	userConfigDir, err := os.UserConfigDir()
	if err != nil {
		println(fmt.Sprintf("error getting config dir: %s", err))
		os.Exit(1)
	}
	return filepath.Join(userConfigDir, "./pogolo")
}
func DeepCopyConfig(dest, src *Config) {
	dest.Backend = src.Backend
	dest.Pogolo = src.Pogolo
}
