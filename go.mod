module git.0xf0xx0.eth.limo/0xf0xx0/pogolo

go 1.26.2

// replace git.0xf0xx0.eth.limo/0xf0xx0/stratum => ../stratum
replace git.0xf0xx0.eth.limo/0xf0xx0/stratumv2 => ../stratumv2

require (
	git.0xf0xx0.eth.limo/0xf0xx0/oigiki v1.3.0
	git.0xf0xx0.eth.limo/0xf0xx0/stratum v0.0.9-0.20260517023006-717ebec60d1f
	git.0xf0xx0.eth.limo/0xf0xx0/stratumv2 v0.0.0
	github.com/btcsuite/btcd v0.25.0
	github.com/btcsuite/btcd/btcutil v1.1.7-0.20251106010755-9ff0780da683
	github.com/btcsuite/btcd/chaincfg/chainhash v1.2.0
	github.com/pelletier/go-toml/v2 v2.3.0
	github.com/prometheus/client_golang v1.23.2
	github.com/urfave/cli/v3 v3.8.0
	github.com/zeebo/xxh3 v1.1.0
)

require (
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/btcsuite/btcd/btcec/v2 v2.3.6 // indirect
	github.com/btcsuite/btclog v1.0.0 // indirect
	github.com/btcsuite/go-socks v0.0.0-20170105172521-4720035b7bfd // indirect
	github.com/btcsuite/websocket v0.0.0-20150119174127-31079b680792 // indirect
	github.com/bytedance/gopkg v0.1.4 // indirect
	github.com/bytedance/sonic v1.15.0 // indirect
	github.com/bytedance/sonic/loader v0.5.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/cloudwego/base64x v0.1.6 // indirect
	github.com/decred/dcrd/crypto/blake256 v1.1.0 // indirect
	github.com/decred/dcrd/dcrec/secp256k1/v4 v4.4.1 // indirect
	github.com/klauspost/cpuid/v2 v2.3.0 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/prometheus/client_model v0.6.2 // indirect
	github.com/prometheus/common v0.66.1 // indirect
	github.com/prometheus/procfs v0.16.1 // indirect
	github.com/twitchyliquid64/golang-asm v0.15.1 // indirect
	go.yaml.in/yaml/v2 v2.4.2 // indirect
	golang.org/x/arch v0.26.0 // indirect
	golang.org/x/crypto v0.50.0 // indirect
	golang.org/x/sys v0.43.0 // indirect
	google.golang.org/protobuf v1.36.8 // indirect
)

retract v1.2.0
