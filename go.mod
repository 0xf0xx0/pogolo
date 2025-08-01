module pogolo

go 1.24.4

//replace github.com/0xf0xx0/stratum => ../stratum
//replace github.com/0xf0xx0/oigiki => ../oigiki

require (
	github.com/0xf0xx0/oigiki v0.1.0
	github.com/0xf0xx0/stratum v0.0.2
	github.com/btcsuite/btcd v0.24.2
	github.com/btcsuite/btcd/btcutil v1.1.6
	github.com/btcsuite/btcd/chaincfg/chainhash v1.1.0
	github.com/fatih/color v1.18.0
	github.com/pelletier/go-toml/v2 v2.2.4
	github.com/urfave/cli/v3 v3.3.8
)

require (
	github.com/btcsuite/btcd/btcec/v2 v2.3.5 // indirect
	github.com/btcsuite/btclog v1.0.0 // indirect
	github.com/btcsuite/go-socks v0.0.0-20170105172521-4720035b7bfd // indirect
	github.com/btcsuite/websocket v0.0.0-20150119174127-31079b680792 // indirect
	github.com/decred/dcrd/crypto/blake256 v1.1.0 // indirect
	github.com/decred/dcrd/dcrec/secp256k1/v4 v4.4.0 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	golang.org/x/crypto v0.40.0 // indirect
	golang.org/x/sys v0.34.0 // indirect
)
