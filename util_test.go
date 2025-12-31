package main

import (
	"strings"
	"testing"
)

var m = &clientMap{}
var fakeclient = &StratumClient{ID: 42069}

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
	if get, ok = m.Get(42069); ok {
		t.Fatal("client still exists in map after delete")
	}
	m = nil
}

func TestDecodeStratumMessage(t *testing.T) {
	_, err := DecodeStratumMessage([]byte(MOCK_MINING_SUBSCRIBE))
	if err != nil {
		t.Fatalf("failed to parse message: %s", err)
	}
	_, err = DecodeStratumMessage([]byte(MOCK_MINING_SUBSCRIBE)[:len(MOCK_MINING_SUBSCRIBE)/3])
	if err == nil {
		t.Fatal("parsed message when we shouldntve")
	}
}

func TestCreateEmptyCoinbase(t *testing.T) {
	_, err := CreateEmptyCoinbase(MOCK_BLOCK_TEMPLATE)
	if err != nil {
		t.Fatalf("failed to create coinbase: %s", err)
	}

	tag := conf.Pogolo.Tag
	conf.Pogolo.Tag = strings.Repeat("wveegwgwgws", 25) /// way too big
	_, err = CreateEmptyCoinbase(MOCK_BLOCK_TEMPLATE)
	conf.Pogolo.Tag = tag
	if err != nil {
		t.Fatalf("failed to fallback to default pool tag: %s", err)
	}

	/// TODO: handle extranonce2 too large
}
