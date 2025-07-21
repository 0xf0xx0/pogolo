module pogolo

go 1.24.3

replace github.com/kbnchk/go-Stratum => ./go-Stratum

require (
	github.com/btcsuite/btcd v0.24.2
	github.com/btcsuite/btcd/btcutil v1.1.5
	github.com/btcsuite/btcd/chaincfg/chainhash v1.1.0
	github.com/kbnchk/go-Stratum v0.0.0-20230226115229-2f489578c087
)

require (
	github.com/btcsuite/btcd/btcec/v2 v2.1.3 // indirect
	github.com/btcsuite/btclog v0.0.0-20170628155309-84c8d2346e9f // indirect
	github.com/btcsuite/go-socks v0.0.0-20170105172521-4720035b7bfd // indirect
	github.com/btcsuite/websocket v0.0.0-20150119174127-31079b680792 // indirect
	github.com/decred/dcrd/crypto/blake256 v1.0.0 // indirect
	github.com/decred/dcrd/dcrec/secp256k1/v4 v4.0.1 // indirect
	github.com/stretchr/testify v1.10.0 // indirect
	golang.org/x/crypto v0.0.0-20200622213623-75b288015ac9 // indirect
	golang.org/x/sys v0.5.0 // indirect
)
