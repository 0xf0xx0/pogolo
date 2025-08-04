package config

import (
	"fmt"
	"os"
	"path/filepath"
	"pogolo/constants"
	"strings"

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
	Host         string `toml:"host" comment:"RPC host:port"`
	Cookie       string `toml:"cookie,commented" comment:"RPC cookie path"`
	Rpcauth      string `toml:"rpcauth,commented" comment:"optional, RPC user/pass"`
	PollInterval uint64 `toml:"poll_interval" comment:"how quickly to poll for block updates, in milliseconds"`
}
type Pogolo struct {
	Interface           string  `toml:"interface" comment:"takes precedence over ip, will listen on all interface ips"`
	IP                  string  `toml:"ip,commented" comment:"ipv4, v6, or domain (domain will only resolve the first ip)"`
	Port                uint16  `toml:"port" comment:"use 0 to pick a random port"`
	HTTPPort            uint16  `toml:"http_port" comment:"port for the api"`
	Password            string  `toml:"password,commented" comment:"optional, required for clients if set"`
	Tag                 string  `toml:"tag" comment:"will be replaced by default tag if too long (see coinbase scriptsig limit)"`
	DefaultDifficulty   float64 `toml:"default_difficulty" comment:"minimum 0.01"`
	JobInterval         uint64  `toml:"job_interval" comment:"how often to send new work to clients, in seconds"`
	TargetShareInterval uint64  `toml:"target_share_interval" comment:"how often we want shares on average, in seconds"`
	DiffAdjustInterval  uint64  `toml:"difficulty_adjustment_interval" comment:"how often we should check to adjust a clients difficulty, in seconds"`
}

var DEFAULT_CONFIG = Config{
	Backend: Backend{
		Host:         "[::1]:18443",
		Cookie:       resolvePath("~/.bitcoin/regtest/.cookie"),
		PollInterval: 500,
	},
	Pogolo: Pogolo{
		Interface:           "lo",
		IP:                  "[::1]",
		Port:                5661,
		HTTPPort:            5662,
		Tag:                 constants.DEFAULT_COINBASE_TAG,
		DefaultDifficulty:   constants.DEFAULT_DIFFICULTY,
		JobInterval:         60,
		TargetShareInterval: 10,
		DiffAdjustInterval:  300,
	},
}

func LoadConfig(path string, conf *Config) error {
	configfile, err := os.Open(path)
	if err != nil {
		return err
	}
	d := toml.NewDecoder(configfile)
	d.DisallowUnknownFields()
	if err := d.Decode(conf); err != nil {
		return err
	}
	conf.Backend.Cookie = resolvePath(conf.Backend.Cookie)
	return nil
}
func WriteDefaultConfig(path string) error {
	conf, _ := toml.Marshal(DEFAULT_CONFIG)
	if err := os.WriteFile(resolvePath(path), conf, 0755); err != nil {
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

// resolves ~ and cleans path
// https://stackoverflow.com/a/17617721
func resolvePath(path string) string {
	if strings.HasPrefix(path, "~") {
		// Use strings.HasPrefix so we don't match paths like
		// "/something/~/something/"
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, path[1:])
	}
	return path
}
