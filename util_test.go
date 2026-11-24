package main

import (
	"encoding/hex"
	"math"
	"math/big"
	"math/rand/v2"
	"strings"
	"testing"

	"git.0xf0xx0.eth.limo/0xf0xx0/pogolo/constants"

	"git.0xf0xx0.eth.limo/0xf0xx0/stratum"
	"git.0xf0xx0.eth.limo/0xf0xx0/stratumv2"
	"github.com/btcsuite/btcd/chainhash/v2"
)

var m = &clientMap{}
var fakeclient = &StratumClient{ID: 42069}

func TestBLeh(t *testing.T) {
	h := calcNetworkDifficultyHash(0x1b0404cb)
	d := calcDifficulty(h)
	x := diffToTarget(d)
	z := calcDifficulty(chainhash.Hash(x))

	t.Log(d)
	t.Log(h)
	t.Log("00000000000404cb000000000000000000000000000000000000000000000000")
	t.Log(x)
	t.Log(z)

	ch := chainhash.Hash{}
	hx, _ := hex.DecodeString("ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")
	ch.SetBytes(hx)

	t.Log(calcDifficulty(ch))

	dd := stratumv2.U256(diffToTarget(1037))
	ux := stratumv2.U256(x)
	t.Log(dd.IsMetBy(ux))
}

func TestClientMap(t *testing.T) {
	m.Init()
	m.Add(fakeclient)

	get, ok := m.Get(42069)
	if !ok || get != fakeclient {
		t.Fatal("failed to get client from map (mismatch or nonexistent)")
	}

	all := m.All()
	if len(all) != 1 || all[0] != fakeclient {
		t.Fatal("client not in .All output")
	}

	m.Delete(42069)
	if _, ok = m.Get(42069); ok {
		t.Fatal("client still exists in map after delete")
	}
	m = nil
}

func TestDecodeStratumMessage(t *testing.T) {
	_, err := decodeStratumMessage([]byte(MOCK_MINING_SUBSCRIBE))
	if err != nil {
		t.Fatalf("failed to parse message: %s", err)
	}
	_, err = decodeStratumMessage([]byte(MOCK_MINING_SUBSCRIBE)[:len(MOCK_MINING_SUBSCRIBE)/3])
	if err == nil {
		t.Fatal("parsed message when we shouldntve")
	}
}

func TestCreateEmptyCoinbase(t *testing.T) {
	_, err := createEmptyCoinbase(MOCK_BLOCK_TEMPLATE)
	if err != nil {
		t.Fatalf("failed to create coinbase: %s", err)
	}

	tag := conf.Tag
	conf.Tag = strings.Repeat("wveegwgwgws", 25) /// way too big
	_, err = createEmptyCoinbase(MOCK_BLOCK_TEMPLATE)
	conf.Tag = tag
	if err != nil {
		t.Fatalf("failed to fallback to default pool tag: %s", err)
	}

	en2 := conf.ExtraNonce2Size
	conf.ExtraNonce2Size = 255
	_, err = createEmptyCoinbase(MOCK_BLOCK_TEMPLATE)
	conf.ExtraNonce2Size = en2
	if err != nil {
		t.Fatalf("failed to fallback to default pool tag: %s", err)
	}
}

func TestClientIDGeneration(t *testing.T) {
	set := make(map[stratum.ID]struct{}, 2)
	for range 100 {
		hash := clientIDHash("127.0.0.1:42069", "10.42.69.0:65535")
		_, ok := set[hash]
		if !ok {
			set[hash] = struct{}{}
		}
	}
	for k := range set {
		t.Log(k.String())
	}
	if len(set) != 2 {
		t.Fatal("client ID generation failed: expected 2 unique IDs, got", len(set))
	}
}

// the old big.Int implementation, kept as a reference oracle
func diffToTargetBigInt(d float64) stratumv2.U256 {
	if d <= 0 {
		return constants.Target1U256
	}
	const scale = 1_000_000
	rounded := int64(math.Round(d * scale))
	if rounded <= 0 {
		return constants.Target1U256
	}
	target1, _ := new(big.Int).SetString("00000000FFFF0000000000000000000000000000000000000000000000000000", 16)
	target := new(big.Int).Mul(target1, big.NewInt(scale))
	target.Quo(target, big.NewInt(rounded))
	tb := target.Bytes() /// big-endian
	if len(tb) > 32 {
		panic("target does not fit into 32 bytes")
	}
	out := stratumv2.U256{}
	/// minimal-length big-endian -> little-endian
	for i, b := range tb {
		out[len(tb)-1-i] = b
	}
	return out
}

func TestDiffToTargetMatchesBigInt(t *testing.T) {
	diffs := []float64{
		0, -1, 0.0000001, 0.0000005, 0.0000006, 0.000001,
		0.00016, 0.16, 0.5, 0.999999, 1, 1.000001, 1.5,
		1024, 65535, 1e6, 1e9, 1e12,
	}
	/// broad logarithmic coverage, avoiding int64 overflow at the 1e6 scale
	rng := rand.New(rand.NewPCG(0xdead, 0xbeef))
	for range 10_000 {
		d := math.Exp(rng.Float64()*math.Log(1e12) - math.Log(1e7))
		diffs = append(diffs, d)
	}
	for _, d := range diffs {
		got := diffToTarget(d)
		want := diffToTargetBigInt(d)
		if got != want {
			t.Fatalf("diffToTarget(%g) mismatch:\ngot  %x\nwant %x", d, got[:], want[:])
		}
	}
}
