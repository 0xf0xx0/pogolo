module git.0xf0xx0.eth.limo/0xf0xx0/pogolo

go 1.25.0

//replace git.0xf0xx0.eth.limo/0xf0xx0/stratum => ../stratum

//replace git.0xf0xx0.eth.limo/0xf0xx0/oigiki => ../oigiki

require (
	git.0xf0xx0.eth.limo/0xf0xx0/oigiki v1.3.0
	git.0xf0xx0.eth.limo/0xf0xx0/stratum v0.0.9-0.20260201200248-331910908358
	github.com/btcsuite/btcd v0.25.0
	github.com/btcsuite/btcd/btcutil v1.1.7-0.20251106010755-9ff0780da683
	github.com/btcsuite/btcd/chaincfg/chainhash v1.1.1-0.20251106010755-9ff0780da683
	github.com/pelletier/go-toml/v2 v2.2.4
	github.com/urfave/cli/v3 v3.6.2
	github.com/zeebo/xxh3 v1.1.0
)

require (
	github.com/btcsuite/btcd/btcec/v2 v2.3.6 // indirect
	github.com/btcsuite/btclog v1.0.0 // indirect
	github.com/btcsuite/go-socks v0.0.0-20170105172521-4720035b7bfd // indirect
	github.com/btcsuite/websocket v0.0.0-20150119174127-31079b680792 // indirect
	github.com/bytedance/gopkg v0.1.3 // indirect
	github.com/bytedance/sonic v1.15.0 // indirect
	github.com/bytedance/sonic/loader v0.5.0 // indirect
	github.com/cloudwego/base64x v0.1.6 // indirect
	github.com/decred/dcrd/crypto/blake256 v1.1.0 // indirect
	github.com/decred/dcrd/dcrec/secp256k1/v4 v4.4.0 // indirect
	github.com/klauspost/cpuid/v2 v2.3.0 // indirect
	github.com/twitchyliquid64/golang-asm v0.15.1 // indirect
	golang.org/x/arch v0.23.0 // indirect
	golang.org/x/crypto v0.47.0 // indirect
	golang.org/x/sys v0.40.0 // indirect
)
