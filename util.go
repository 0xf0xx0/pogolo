package main

import (
	"bytes"
	"encoding/binary"
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
	"git.0xf0xx0.eth.limo/0xf0xx0/stratumv2"
	"github.com/btcsuite/btcd/address/v2"
	"github.com/btcsuite/btcd/blockchain"
	"github.com/btcsuite/btcd/btcjson"
	"github.com/btcsuite/btcd/btcutil/v2"
	"github.com/btcsuite/btcd/chainhash/v2"
	"github.com/btcsuite/btcd/txscript/v2"
	"github.com/btcsuite/btcd/wire/v2"
	"github.com/minio/sha256-simd"
	"github.com/zeebo/xxh3"
)

var (
	seeda = rand.Uint64()
	seedb = rand.Uint64()
	rng   = rand.NewPCG(seeda, seedb)

	maxTargetFloat = float64(math.Pow(2, 208) * 65535)
)

// basically typed sync.Map
type clientMap struct {
	lock  sync.RWMutex
	idMap map[stratum.ID]*StratumClient
	len   int
}

func (m *clientMap) Init() {
	m.idMap = make(map[stratum.ID]*StratumClient, 5)
}
func (m *clientMap) Add(client *StratumClient) {
	m.lock.Lock()
	defer m.lock.Unlock()
	m.idMap[client.ID] = client
	m.len++
}
func (m *clientMap) Delete(id stratum.ID) {
	m.lock.Lock()
	defer m.lock.Unlock()
	delete(m.idMap, id)
	m.len--
}
func (m *clientMap) Get(id stratum.ID) (*StratumClient, bool) {
	m.lock.RLock()
	defer m.lock.RUnlock()
	ret, ok := m.idMap[id]
	return ret, ok
}
func (m *clientMap) All() []*StratumClient {
	m.lock.RLock()
	defer m.lock.RUnlock()
	ret := make([]*StratumClient, 0, len(m.idMap))
	for _, client := range m.idMap {
		ret = append(ret, client)
	}
	return ret
}
func (m *clientMap) AllStats() []StratumClientStats {
	m.lock.RLock()
	defer m.lock.RUnlock()
	ret := make([]StratumClientStats, 0, len(m.idMap))
	for _, client := range m.idMap {
		// make a copy
		stats := StratumClientStats{
			lastTimeSlot:       client.stats.lastTimeSlot,
			currTimeSlot:       client.stats.currTimeSlot,
			startTime:          client.stats.startTime,
			lastSubmissionTime: client.stats.lastSubmissionTime,
			sharesAccepted:     client.stats.sharesAccepted,
			sharesRejected:     client.stats.sharesRejected,
			avgSubmissionDelta: client.stats.avgSubmissionDelta,
			bestDiff:           client.stats.bestDiff,
			hashrate:           client.stats.hashrate,
		}
		ret = append(ret, stats)
	}
	return ret
}
func (m *clientMap) NotifyAll(job *JobTemplate) {
	m.lock.RLock()
	defer m.lock.RUnlock()
	for _, client := range m.idMap {
		client.TemplateChannel() <- job
	}
}
func (m *clientMap) Len() int {
	m.lock.RLock()
	defer m.lock.RUnlock()
	return m.len
}

func decodeStratumMessage(msg []byte) (*stratum.Request, error) {
	var m stratum.Request
	if err := m.Unmarshal(msg); err != nil {
		return nil, err
	}
	return &m, nil
}

// we dont need to do error handling here, this is only used to serialize the coinbase
// (without the witness)
func serializeCoinbaseTx(tx *wire.MsgTx) []byte {
	serializedTx := bytes.NewBuffer(make([]byte, 0, tx.SerializeSize()))
	tx.SerializeNoWitness(serializedTx)
	return serializedTx.Bytes()
}

// "merkle root" of remote and local addr hashes for no reason other than being different
// dis pogolo we serious and silly :3
func clientIDHash(la, ra string) stratum.ID {
	out := make([]byte, 16)
	binary.LittleEndian.PutUint64(out, xxh3.HashString(la))
	binary.LittleEndian.PutUint64(out[8:], xxh3.HashString(ra))

	/// randomly pick between upper and lower 32 for double the extranonce1s
	return stratum.ID(xxh3.Hash(out) >> (rand.N(2) * 32))
}

// initial tx, copied and filled by clients
// sets up everything but the outpoints, which are populated in fillCoinbaseTx
func createEmptyCoinbase(template *btcjson.GetBlockTemplateResult) (*btcutil.Tx, error) {
	height := template.Height
	coinbaseTxMsg := &wire.MsgTx{
		Version:  wire.TxVersion,
		TxIn:     make([]*wire.TxIn, 0, 1),  /// only 1 txin for coinbases
		TxOut:    make([]*wire.TxOut, 0, 2), /// 1 slot for witness, second for subsidy
		LockTime: uint32(height) - 1,        /// BIP-54
	}

	/// 4 bytes + ExtraNonce2Size bytes of padding, for extranonces
	padding := make([]byte, constants.EXTRANONCE_SIZE+conf.ExtraNonce2Size)
	/// random byte to avoid client loops if template doesn't change
	/// better alternative to not sending the job at all
	coinbaseScript := txscript.NewScriptBuilder().
		/// bip-34
		AddInt64(height).
		/// MAYBE: remove prng, use jobid % 256?
		AddData([]byte{uint8(rng.Uint64())}).
		AddData([]byte(conf.Tag)).
		AddData(padding)
	encodedCoinbaseScript, err := coinbaseScript.Script()
	if err != nil {
		return nil, err
	}
	if len(encodedCoinbaseScript) > blockchain.MaxCoinbaseScriptLen {
		globalLogError("pool tag too long (>100), resetting to default")
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

	tx := btcutil.NewTx(coinbaseTxMsg)
	tx.SetIndex(0)

	return tx, nil
}

// thank you btcd devs for doin all this boilerplate work
//
// fill the coinbase with the client-specific data
func addCoinbasePayout(en1 stratum.ID, addr address.Address, coinbaseMsgTx *wire.MsgTx, subsidy int64) *wire.MsgTx {
	/// address is validated on client connect, we can safely assume no errors will occur
	pkScript, _ := txscript.PayToAddrScript(addr)
	/// we gotta add the subsidy too
	coinbaseMsgTx.AddTxOut(&wire.TxOut{
		Value:    subsidy,
		PkScript: pkScript,
	})
	/// pre-fill extranonce1
	sigscriptLen := len(coinbaseMsgTx.TxIn[0].SignatureScript)
	copy(coinbaseMsgTx.TxIn[0].SignatureScript[sigscriptLen-(constants.EXTRANONCE_SIZE+int(conf.ExtraNonce2Size)):], en1.Bytes())
	return coinbaseMsgTx
}

// shamelessly stolen from m45core lol
// faster diff calc
func calcDifficulty(hash chainhash.Hash) float64 {
	msb := -1
	for i := len(hash) - 1; i >= 0; i-- {
		if hash[i] != 0 {
			msb = i
			break
		}
	}
	if msb < 0 {
		return maxTargetFloat
	}

	var top uint64
	for j := range 8 {
		idx := msb - j
		var b byte
		if idx >= 0 {
			b = hash[idx]
		}
		top = (top << 8) | uint64(b)
	}
	if top == 0 {
		return maxTargetFloat
	}

	// For msb==31 we used bytes [31..24], leaving 24 bytes below => exponentBits=192.
	exponentBits := 8 * (msb - 7)

	// diff = (65535 / top) * 2^(208 - exponentBits)
	diff := math.Ldexp(65535.0/float64(top), 208-exponentBits)
	if diff <= 0 || math.IsNaN(diff) {
		return maxTargetFloat
	}
	if math.IsInf(diff, 0) {
		return math.MaxFloat64
	}
	return diff
}

// port of public-pools calculateNetworkDifficulty
func calcNetworkDifficulty(nBits uint32) float64 {
	/// unpack the target from the compact nBits
	mantissa := float64(nBits & 0x007fffff)
	exponent := float64((nBits >> 24) & 0xff)
	target := mantissa * math.Pow(256, float64(exponent-3))

	return maxTargetFloat / target
}

// a * 2**(8*(b-3)), where a is bits[0:3] and b is bits[3]. Returns the target as a big-endian byte array.
// Note that bits is little-endian, and that the 24th bit, theoretically a sign bit, is ignored as per the spec's suggestion.
func calcNetworkDifficultyHash(nBits uint32) chainhash.Hash {
	target := chainhash.Hash{}
	a := nBits & 0x007fffff // 23-bit mantissa; 24th is sign bit which is ignored
	b := nBits >> 24 & 0xff

	byte_shift := b - 3 // Original shift is 8 * (b - 3), so b - 3 represents the number of bytes left in the target array to shift a

	binary.LittleEndian.PutUint32(target[byte_shift:], a)

	return target
}

// TODO: figure out non-bigint version? mafffffff
// converts a difficulty float to a target hash
// copied from public-pool
func diffToTarget(d float64) stratumv2.U256 {
	if d <= 0 {
		return *constants.Target1U256
	}
	scale := float64(1000000)
	bigScale := big.NewInt(1000000)
	rounded := int64(math.Round(d * scale))
	if rounded <= 0 {
		return *constants.Target1U256
	}
	bigRound := big.NewInt(rounded)

	target := new(big.Int).Mul(constants.Target1, bigScale)
	target = target.Quo(target, bigRound)
	out := stratumv2.U256{}

	tb := target.Bytes() // big-endian
	tbLen := len(tb)
	if tbLen > 32 {
		panic("target does not fit into 32 bytes")
	}
	copy(out[32-tbLen:], tb)

	// convert to little-endian
	for i := range 16 {
		out[i], out[31-i] = out[31-i], out[i]
	}
	return out
}

// estimated target diff from hashrate in h/s, clamped to [constants.MIN_DIFFICULTY]
func calcDiffFromHashrate(hashrate float64) float64 {
	diff := hashrate * float64(conf.TargetShareInterval) / 0x100000000
	return math.Max(math.Round(diff), constants.MIN_DIFFICULTY)
}

func merkleRootFromBranches(branches []*chainhash.Hash) *chainhash.Hash {
	root := branches[0]
	/// optimization: reuse array to store the combined hashes
	/// instead of creating a new one every time
	temp := make([]byte, 64)
	for _, branch := range branches[1:] {
		copy(temp[:32], root[:])
		copy(temp[32:], branch[:])
		newroot := simdSha256d(temp)
		root = &newroot
	}
	return root
}
func buildMerkleProof(tree []*chainhash.Hash, leaf *chainhash.Hash) []*chainhash.Hash {
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
func formatDifficulty(value float64) string {
	sb := strings.Builder{}
	sb.Grow(8)
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

	sb.WriteString(strconv.FormatFloat(value, 'g', 3, 64))
	sb.WriteString(unit)
	return sb.String()
}

// takes MH/s
func formatHashrate(value float64) string {
	sb := strings.Builder{}
	sb.Grow(16)

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

	sb.WriteString(strconv.FormatFloat(value, 'g', 5, 64))
	sb.WriteRune(' ')
	sb.WriteString(unit)
	sb.WriteString("H/s")
	return sb.String()
}

func globalLog(s string) {
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
func globalLogError(s string) {
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

func simdCoinbaseTxHash(msgTx *wire.MsgTx) chainhash.Hash {
	h := sha256.New()
	msgTx.SerializeNoWitness(h)
	temp := make([]byte, 0, 32)
	first := h.Sum(temp)
	h.Reset()
	h.Write(first)
	return chainhash.Hash(h.Sum(temp))
}

func simdHeaderHash(header *wire.BlockHeader) chainhash.Hash {
	h := sha256.New()
	header.Serialize(h)
	temp := make([]byte, 0, 32)
	first := h.Sum(temp)
	h.Reset()
	h.Write(first)
	return chainhash.Hash(h.Sum(temp))
}

func simdSha256d(data []byte) chainhash.Hash {
	a := sha256.Sum256(data)
	return sha256.Sum256(a[:])
}
