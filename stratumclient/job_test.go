package stratumclient_test

import (
	"pogolo/config"
	"pogolo/stratumclient"
	"strconv"
	"testing"

	"github.com/btcsuite/btcd/blockchain"
	"github.com/btcsuite/btcd/btcjson"
	"github.com/btcsuite/btcd/btcutil"
)

const (
	ADDR   = "bc1pdqrcrxa8vx6gy75mfdfj84puhxffh4fq46h3gkp6jxdd0vjcsdyspfxcv6"
	HEIGHT = 0
)

func TestGBT(t *testing.T) {
	template := getBlockTemplate()
	t.Logf("%+v", template)
}

func TestJobTemplate(t *testing.T) {
	_, client := initClient()
	template := getBlockTemplate()
	job := stratumclient.CreateJobTemplate(template, *client)
	if job.Height != template.Height || job.Block.Height() != int32(template.Height) {
		t.Errorf("job height mismatch: expected %d, got %d and %d", template.Height, job.Height, job.Block.Height())
	}
	if job.CoinbaseValue != *template.CoinbaseValue {
		t.Errorf("job coinbase value mismatch, expected %d, got %d", template.CoinbaseValue, job.CoinbaseValue)
	}
	t.Logf("%+v", job)
}

func TestCoinbaseWeight(t *testing.T) {
	tx := getCoinbaseTx(HEIGHT)
	coinbaseWeight := blockchain.GetTransactionWeight(tx)
	if coinbaseWeight > blockchain.MaxBlockWeight {
		t.Errorf("block too heavy: %d, max %d", coinbaseWeight, blockchain.MaxCoinbaseScriptLen)
	}
	t.Logf("weight: %d", coinbaseWeight)
}

func TestCoinbaseScript(t *testing.T) {
	tx := getCoinbaseTx(HEIGHT)

	coinbaseScript := tx.MsgTx().TxIn[0].SignatureScript
	t.Logf("%q", coinbaseScript)
	t.Logf("coinbase script: %x", coinbaseScript)
}

func TestValidatePkScript(t *testing.T) {
	tx := getCoinbaseTx(HEIGHT)

	pkscript := tx.MsgTx().TxOut[0].PkScript
	t.Logf("pkscript: %x", pkscript)
}

func TestValidateCoinbaseScript(t *testing.T) {
	tx := getCoinbaseTx(HEIGHT)
	txIn := tx.MsgTx().TxIn[0]
	script := txIn.SignatureScript

	if len(script) > blockchain.MaxCoinbaseScriptLen {
		t.Errorf("coinbase script too long: %d, max %d", len(script), blockchain.MaxCoinbaseScriptLen)
	}
	t.Logf("script len: %d, max: %d", len(script), blockchain.MaxCoinbaseScriptLen)
}

func getAddr() btcutil.Address {
	addr, _ := btcutil.DecodeAddress(ADDR, config.CHAIN)
	return addr
}
func getCoinbaseTx(height int32) *btcutil.Tx {
	addr := getAddr()
	en2, _ := strconv.ParseInt("0f100f", 16, 32)
	tx := stratumclient.CreateCoinbaseTx(addr, height, en2, config.CHAIN)
	return tx
}
func getBlockTemplate() *btcjson.GetBlockTemplateResult {
	return MOCK_BLOCK_TEMPLATE
}
