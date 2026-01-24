package main

import (
	"bytes"
	"encoding/hex"
	"math"
	"testing"
	"time"

	"git.0xf0xx0.eth.limo/0xf0xx0/stratum"
	"github.com/btcsuite/btcd/blockchain"
)

func TestCreateJobTemplate(t *testing.T) {
	template := MOCK_BLOCK_TEMPLATE
	job, err := CreateJobTemplate(template)

	if err != nil {
		t.Errorf("error making job: %s", err)
		t.FailNow()
		return
	}
	if job.Height != template.Height {
		t.Errorf("job height mismatch: expected %d, got job: %d", template.Height, job.Height)
	}
	if job.Subsidy != *template.CoinbaseValue {
		t.Errorf("job coinbase value mismatch, expected %d, got %d", template.CoinbaseValue, job.Subsidy)
	}
	jobBits := hex.EncodeToString(job.Bits)
	if jobBits != template.Bits {
		t.Errorf("job bits mismatch, expected %s, got %s", template.Bits, jobBits)
	}
	if job.MsgBlock.Header.PrevBlock.String() != template.PreviousHash {
		t.Errorf("job prevhash mismatch, expected %s, got %s",
			template.PreviousHash,
			job.MsgBlock.Header.PrevBlock.String())
	}
	/// MAYBE/FIXME: how to validate merkle root?
}

func TestJobMinTime(t *testing.T) {
	template := MOCK_BLOCK_TEMPLATE
	template.MinTime = time.Now().Unix() + 6000
	_, err := CreateJobTemplate(template)
	if err != nil {
		t.Fatal(err.Error())
	}
}
func TestJobmaxTime(t *testing.T) {
	template := MOCK_BLOCK_TEMPLATE
	template.MinTime = 0
	template.MaxTime = 6000
	_, err := CreateJobTemplate(template)
	if err != nil {
		t.Fatal(err.Error())
	}
}

func TestValidateCoinbaseScript(t *testing.T) {
	tx := getCoinbaseTx()
	script := tx.MsgTx().TxIn[0].SignatureScript

	if len(script) > blockchain.MaxCoinbaseScriptLen {
		t.Errorf("coinbase script too long: %d, max %d", len(script), blockchain.MaxCoinbaseScriptLen)
	}
	t.Logf("%q", script)
	t.Logf("coinbase script: %x", script)
	t.Logf("script len: %d, max: %d", len(script), blockchain.MaxCoinbaseScriptLen)
	/// MAYBE: verify the block height is at the start
}

// this needs to be re-done every time something about the block structure changes
func TestUpdateBlock(t *testing.T) {
	conf = DEFAULT_CONFIG
	template := MOCK_BLOCK_TEMPLATE
	expectedShareDiff := MOCK_SHAREDIFF
	id, _ := stratum.DecodeID(MOCK_EXTRANONCE)
	client := &StratumClient{
		ID:   id,
		User: getAddr(),
	}

	tml, _ := CreateJobTemplate(template)
	job := client.createJob(tml)
	blk, err := job.UpdateBlock(client.ID, submitParams, notifyParams)
	if err != nil {
		t.Error(err)
		return
	}

	shareDiff, _ := CalcDifficulty(blk.Header)
	if math.Abs(shareDiff-expectedShareDiff) > 0.001 {
		t.Errorf("share diff mismatch: expected %f, got %f", expectedShareDiff, shareDiff)
		return
	}

	serializedHeader := bytes.NewBuffer([]byte{})
	blk.Header.Serialize(serializedHeader)
	t.Logf("sharediff: %g", shareDiff)
	t.Logf("header: %s", hex.EncodeToString(serializedHeader.Bytes()))
	t.Logf("hash: %s", blk.BlockHash())
}
