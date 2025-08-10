package main

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"math"
	"math/big"
	"pogolo/constants"
	"slices"

	"github.com/0xf0xx0/stratum"
	"github.com/btcsuite/btcd/blockchain"
	"github.com/btcsuite/btcd/btcjson"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/mining"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
)

func DecodeStratumMessage(msg []byte) (*stratum.Request, error) {
	var m stratum.Request
	if err := m.Unmarshal(msg); err != nil {
		return nil, err
	}
	return &m, nil
}
func SerializeTx(tx *wire.MsgTx, witness bool) ([]byte, error) {
	serializedTx := bytes.NewBuffer([]byte{})

	if witness {
		if err := tx.Serialize(serializedTx); err != nil {
			return nil, err
		}
	} else {
		if err := tx.SerializeNoWitness(serializedTx); err != nil {
			return nil, err
		}
	}
	return serializedTx.Bytes(), nil
}
func SerializeBlock(blk *btcutil.Block) ([]byte, error) {
	buf := bytes.NewBuffer([]byte{})
	if err := blk.MsgBlock().Serialize(buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// 32-bit (4-byte) uint32 used for client id and extranonce1
// TODO: hash client local ip address? for no reason other than being different
func ClientIDHash() stratum.ID {
	randomBytes := make([]byte, constants.EXTRANONCE_SIZE)
	rand.Read(randomBytes)
	/// im 90% sure this needs to be BE
	return stratum.ID(binary.BigEndian.Uint32(randomBytes))
}

// placeholder tx, filled with MiningJob
func CreateEmptyCoinbase(template *btcjson.GetBlockTemplateResult) *btcutil.Tx {
	coinbaseTxMsg := wire.NewMsgTx(wire.TxVersion)

	height := template.Height
	padding := [7]byte{} /// 8 bytes of padding, for extranonces
	///                 /// adding data pushes an extra byte for the opcode
	coinbaseScript := txscript.NewScriptBuilder().
		/// bip-34
		AddInt64(height).
		AddData([]byte(conf.Pogolo.Tag)).
		AddData(padding[:])
	encodedCoinbaseScript, err := coinbaseScript.Script()
	if err != nil {
		panic(err)
	}
	if len(encodedCoinbaseScript) > blockchain.MaxCoinbaseScriptLen {
		println("pool tag too long, resetting to default")
		coinbaseScript = coinbaseScript.Reset().
			AddInt64(height).
			AddData([]byte(constants.DEFAULT_COINBASE_TAG)).
			AddData(padding[:])
		encodedCoinbaseScript, err = coinbaseScript.Script()
		if err != nil {
			panic(err)
		}
	}
	coinbaseTxMsg.AddTxIn(&wire.TxIn{
		PreviousOutPoint: *wire.NewOutPoint(&chainhash.Hash{}, wire.MaxPrevOutIndex),
		SignatureScript:  encodedCoinbaseScript,
		Sequence:         wire.MaxTxInSequenceNum,
	})

	tx := btcutil.NewTx(coinbaseTxMsg)
	tx.SetIndex(0)

	return tx
}

// thank you btcd devs for doin all this boilerplate work
func CreateCoinbaseTx(addr btcutil.Address, block *btcutil.Block, subsidy int64, params *chaincfg.Params) *btcutil.Tx {
	pkScript, err := txscript.PayToAddrScript(addr)
	if err != nil {
		panic(err)
	}

	coinbase := block.Transactions()[0]
	coinbaseTxMsg := coinbase.MsgTx()
	coinbaseTxMsg.AddTxOut(&wire.TxOut{
		Value:    subsidy,
		PkScript: pkScript,
	})
	/// AND I AM ITS SOLE WITNESS
	mining.AddWitnessCommitment(coinbase, block.Transactions())
	return coinbase
}

// port of public-pools calculateDifficulty
func CalcDifficulty(header wire.BlockHeader) float64 {
	hashResult := header.BlockHash()
	s64 := blockchain.HashToBig(&hashResult)
	trueDiff1 := big.Int{}
	trueDiff1.SetString("26959535291011309493156476344723991336010898738574164086137773096960", 10)
	diff, _ := new(big.Float).Quo(new(big.Float).SetInt(&trueDiff1), new(big.Float).SetInt(s64)).Float64()
	return diff
}

// port of public-pools calculateNetworkDifficulty
func CalcNetworkDifficulty(nBits uint32) float64 {
	mantissa := float64(nBits & 0x007fffff)
	exponent := float64((nBits >> 24) & 0xff)
	target := mantissa * math.Pow(256, float64(exponent-3))
	maxTarget := math.Pow(2, 208) * 65535
	difficulty := maxTarget / target
	return difficulty
}

func merkleRootFromBranches(branches []*chainhash.Hash) *chainhash.Hash {
	root := branches[0]

	for _, h := range branches[1:] {
		newroot := chainhash.DoubleHashH(append(root[:], h[:]...))
		root = &newroot
	}
	return root
}
func BuildMerkleProof(tree []*chainhash.Hash, leaf *chainhash.Hash) []*chainhash.Hash {
	index := slices.Index(tree, leaf)

	if index == -1 {
		return nil
	}

	n := len(tree)
	nodes := []*chainhash.Hash{}

	z := calcTreeWidth(n, 1)
	for z > 0 {
		if treeNodeCount(z) == n {
			break
		}
		z--
	}
	if z == 0 {
		panic("shouldnt ever be reached")
	}

	height := 0
	i := 0
	for i < n-1 {
		layerWidth := calcTreeWidth(z, height)
		height++

		odd := index%2 == 1
		if odd {
			index--
		}
		offset := i + index
		left := tree[offset]
		var right *chainhash.Hash
		if index == layerWidth-1 {
			right = left
		} else {
			right = tree[offset+1]
		}

		if i > 0 {
			if odd {
				nodes = append(nodes, left)
				nodes = append(nodes, nil)
			} else {
				nodes = append(nodes, nil)
				nodes = append(nodes, right)
			}
		} else {
			nodes = append(nodes, left)
			nodes = append(nodes, right)
		}

		index = (index / 2) | 0
		i += layerWidth
	}
	nodes = append(nodes, tree[n-1])
	return nodes
}
func calcTreeWidth(n, h int) int {
	return (n + (1 << h) - 1) >> h
}
func treeNodeCount(leafCount int) int {
	count := 1
	for i := leafCount; i > 1; i = (i + 1) >> 1 {
		count += i
	}
	return count
}

func diffFormat(value float64) string {
	unit := ""
	if value > 1e12 {
		unit = "T"
		value /= 1e12
	} else if value > 1e9 {
		unit = "B"
		value /= 1e9
	} else if value > 1e6 {
		unit = "M"
		value /= 1e6
	} else if value > 1000 {
		unit = "k"
		value /= 1000
	}
	return fmt.Sprintf("%.3g%s", value, unit)
}

// takes MH/s
func formatHashrate(value float64) string {
	unit := "MH"
	if value > 1e6 {
		value /= 1e6
		unit = "TH"
	} else if value > 1000 {
		value /= 1000
		unit = "GH"
	}
	return fmt.Sprintf("%.5g %s/s", value, unit)
}
