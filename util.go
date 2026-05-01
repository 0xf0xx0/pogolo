package main

import (
	"bytes"
	"fmt"
	"math"
	"math/big"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"sync"

	"git.0xf0xx0.eth.limo/0xf0xx0/pogolo/constants"

	"git.0xf0xx0.eth.limo/0xf0xx0/oigiki"
	"git.0xf0xx0.eth.limo/0xf0xx0/stratum"
	"github.com/btcsuite/btcd/blockchain"
	"github.com/btcsuite/btcd/btcjson"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/mining"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/zeebo/xxh3"
)

// basically typed sync.Map
type clientMap struct {
	lock  sync.RWMutex
	idMap map[stratum.ID]*StratumClient
}

func (m *clientMap) Init() {
	m.idMap = make(map[stratum.ID]*StratumClient, 5)
}
func (m *clientMap) Add(client *StratumClient) {
	m.lock.Lock()
	m.idMap[client.ID] = client
	m.lock.Unlock()
}
func (m *clientMap) Delete(id stratum.ID) {
	m.lock.Lock()
	delete(m.idMap, id)
	m.lock.Unlock()
}
func (m *clientMap) Get(id stratum.ID) (*StratumClient, bool) {
	m.lock.RLock()
	ret, ok := m.idMap[id]
	m.lock.RUnlock()
	return ret, ok
}
func (m *clientMap) All() []*StratumClient {
	m.lock.RLock()
	ret := make([]*StratumClient, 0, len(m.idMap))
	for _, client := range m.idMap {
		ret = append(ret, client)
	}
	m.lock.RUnlock()
	return ret
}

func DecodeStratumMessage(msg []byte) (*stratum.Request, error) {
	var m stratum.Request
	if err := m.Unmarshal(msg); err != nil {
		return nil, err
	}
	return &m, nil
}

// we dont need to do error handling here, this is only used to serialize the coinbase
// (without the witness)
func SerializeCoinbaseTx(tx *wire.MsgTx) []byte {
	serializedTx := bytes.NewBuffer(make([]byte, 0, tx.SerializeSize()))
	tx.SerializeNoWitness(serializedTx)
	return serializedTx.Bytes()
}

// hashes client ip address+port for no reason other than being different
func ClientIDHash(addr string) stratum.ID {
	/// randomly pick between upper and lower 32 for double the extranonce1s
	return stratum.ID(xxh3.HashString(addr) >> (rand.N(2)*32))
}

// placeholder tx, filled by clients
func CreateEmptyCoinbase(template *btcjson.GetBlockTemplateResult) (*btcutil.Tx, error) {
	height := template.Height
	coinbaseTxMsg := wire.NewMsgTx(wire.TxVersion)

	coinbaseTxMsg.LockTime = uint32(height) - 1 /// BIP-54

	/// 4 bytes + ExtraNonce2Size bytes of padding, for extranonces
	padding := make([]byte, constants.EXTRANONCE_SIZE+conf.Pogolo.ExtraNonce2Size)
	coinbaseScript := txscript.NewScriptBuilder().
		/// bip-34
		AddInt64(height).
		AddData([]byte(conf.Pogolo.Tag)).
		AddData(padding)
	encodedCoinbaseScript, err := coinbaseScript.Script()
	if err != nil {
		return nil, err
	}
	if len(encodedCoinbaseScript) > blockchain.MaxCoinbaseScriptLen {
		logError("pool tag too long (>100), resetting to default")
		coinbaseScript = coinbaseScript.Reset().
			AddInt64(height).
			AddData([]byte(constants.DEFAULT_COINBASE_TAG)).
			AddData(padding)
		encodedCoinbaseScript, err = coinbaseScript.Script()
		if err != nil {
			return nil, err
		}
	}

	coinbaseTxMsg.AddTxIn(&wire.TxIn{
		PreviousOutPoint: *wire.NewOutPoint(&chainhash.Hash{}, wire.MaxPrevOutIndex),
		SignatureScript:  encodedCoinbaseScript,
		Sequence:         0xfffffffe, /// BIP-54
	})
	/// 1 slot for witness, second for subsidy
	coinbaseTxMsg.TxOut = make([]*wire.TxOut, 0, 2)

	tx := btcutil.NewTx(coinbaseTxMsg)
	tx.SetIndex(0)

	return tx, nil
}

// thank you btcd devs for doin all this boilerplate work
//
// fill the coinbase with the client-specific data
func FillCoinbaseTx(addr btcutil.Address, block *btcutil.Block, subsidy int64, params *chaincfg.Params) *btcutil.Tx {
	/// address is validated on client connect, we can safely assume no errors will occur
	pkScript, _ := txscript.PayToAddrScript(addr)

	coinbase := block.Transactions()[0]
	coinbaseMsgTx := coinbase.MsgTx()
	/// HACK: mining.AddWitnessCommitment appends, empty txout
	coinbaseMsgTx.TxOut = coinbaseMsgTx.TxOut[:0]
	/// NOTE: witness gets added furst, just cause its *unique*
	mining.AddWitnessCommitment(coinbase, block.Transactions())
	/// we gotta add the subsidy too
	coinbaseMsgTx.AddTxOut(&wire.TxOut{
		Value:    subsidy,
		PkScript: pkScript,
	})
	return coinbase
}

// port of public-pools calculateDifficulty
func CalcDifficulty(hash chainhash.Hash) float64 {
	s64 := new(big.Float).SetInt(blockchain.HashToBig(&hash))
	diff, _ := s64.Quo(constants.TrueDiff1, s64).Float64()
	return diff
}

// port of public-pools calculateNetworkDifficulty
func CalcNetworkDifficulty(nBits uint32) float64 {
	maxTarget := math.Pow(2, 208) * 65535
	/// unpack the target from the compact nBits
	mantissa := float64(nBits & 0x007fffff)
	exponent := float64((nBits >> 24) & 0xff)
	target := mantissa * math.Pow(256, float64(exponent-3))

	return maxTarget / target
}

func MerkleRootFromBranches(branches []*chainhash.Hash) *chainhash.Hash {
	root := branches[0]
	/// optimization: reuse array to store the combined hashes
	/// instead of creating a new one every time
	temp := make([]byte, 0, 64)
	for _, branch := range branches[1:] {
		newroot := chainhash.DoubleHashH(append(append(temp, root[:]...), branch[:]...))
		root = &newroot
		temp = temp[:0]
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

		index = (index / 2)
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

func parseUserAgent(ua string) string {
	ua = strings.ToLower(ua)

	if strings.Contains(ua, "axe") {
		split := strings.Split(ua, "/")
		if len(split) != 3 {
			/// confusion
			return ua
		}
		/// format is *axe/<chip>/<fw_version>, drop the version
		return strings.Join(split[:2], "/")
	} else if strings.Contains(ua, "nerdminer") {
		/// https://github.com/BitMaker-hub/NerdMiner_v2/blob/a26865f7cdd9ac1a81c5b0a7c355e23cf4a1d568/src/stratum.cpp#L59
		return "nerdminer"
	} else if strings.Contains(ua, "luckyminer") {
		return "luckyminer"
	}
	/// otherwise fall back to public-pools parsing
	ua = strings.Split(ua, " ")[0]
	ua = strings.Split(ua, "/")[0]
	ua = strings.Split(ua, "v")[0]
	ua = strings.Split(ua, "-")[0]
	return ua
}

// pretty-print difficulty
func FormatDifficulty(value float64) string {
	unit := ""
	if value >= 1e15 {
		unit = "P"
		value /= 1e15
	} else if value >= 1e12 {
		unit = "T"
		value /= 1e12
	} else if value >= 1e9 {
		unit = "G"
		value /= 1e9
	} else if value >= 1e6 {
		unit = "M"
		value /= 1e6
	} else if value >= 1000 {
		unit = "k"
		value /= 1000
	}

	return strconv.FormatFloat(value, 'g', 3, 64) + unit
}

// takes MH/s
func FormatHashrate(value float64) string {
	unit := "M"
	if value > 1e9 {
		value /= 1e9
		unit = "P"
	} else if value > 1e6 {
		value /= 1e6
		unit = "T"
	} else if value > 1000 {
		value /= 1000
		unit = "G"
	}
	return strconv.FormatFloat(value, 'g', 5, 64) + " " + unit + "H/s"
}

func log(s string) {
	if disableLogs {
		return
	}
	s = oigiki.ProcessTags(oigiki.TagString(s, "cyan"))
	if logFile != nil {
		fmt.Fprintln(logFile, s)
		return
	}
	fmt.Println(s)
}
func logError(s string) {
	if disableLogs {
		return
	}
	s = oigiki.ProcessTags(oigiki.TagString(s, "red"))
	if logFile != nil {
		fmt.Fprintln(logFile, s)
		return
	}
	println(s)
}
