package main

import (
	"encoding/hex"
	"strings"
	"testing"

	"git.0xf0xx0.eth.limo/0xf0xx0/stratum"
	"git.0xf0xx0.eth.limo/0xf0xx0/stratumv2"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
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
	t.Log(dd.IsMetBy(&ux))
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

	tag := conf.Pogolo.Tag
	conf.Pogolo.Tag = strings.Repeat("wveegwgwgws", 25) /// way too big
	_, err = createEmptyCoinbase(MOCK_BLOCK_TEMPLATE)
	conf.Pogolo.Tag = tag
	if err != nil {
		t.Fatalf("failed to fallback to default pool tag: %s", err)
	}

	en2 := conf.Pogolo.ExtraNonce2Size
	conf.Pogolo.ExtraNonce2Size = 255
	_, err = createEmptyCoinbase(MOCK_BLOCK_TEMPLATE)
	conf.Pogolo.ExtraNonce2Size = en2
	if err != nil {
		t.Fatalf("failed to fallback to default pool tag: %s", err)
	}
}

func TestClientIDGeneration(t *testing.T) {
	set := make(map[stratum.ID]struct{}, 2)
	for range 100 {
		hash := clientIDHash("127.0.0.1:42069")
		_, ok := set[hash]
		if !ok {
			set[hash] = struct{}{}
		}
	}
	for k := range set {
		t.Log(k.String())
	}
}
