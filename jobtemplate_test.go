package main

import (
	"bytes"
	"encoding/hex"
	"math"
	"strconv"
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
	jobBits := hex.EncodeToString(job.Bits[:])
	if jobBits != template.Bits {
		t.Errorf("job bits mismatch, expected %s, got %s", template.Bits, jobBits)
	}
	if job.MsgBlock.Header.PrevBlock.String() != template.PreviousHash {
		t.Errorf("job prevhash mismatch, expected %s, got %s",
			template.PreviousHash,
			job.MsgBlock.Header.PrevBlock.String())
	}
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
}

// this needs to be re-done every time something about the block structure changes
func TestUpdateBlock(t *testing.T) {
	rng.Seed(MOCK_SEEDA, MOCK_SEEDB)
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
	jobID, _ := strconv.ParseUint(submitParams.JobID, 16, 64)
	s := &commonShare{
		ChannelID:   0,
		JobID:       uint32(jobID),
		Time:        submitParams.Time,
		Version:     uint32(job.Version) + submitParams.VersionMask,
		Nonce:       submitParams.Nonce,
		Extranonce2: submitParams.Extranonce2,
		Sequence:    0,
	}
	hdr, ok := job.UpdateHeader(client.ID, s)
	if !ok {
		t.Error("invalid extranonce2 length")
		return
	}

	t.Logf("%+v\n", hdr)
	shareDiff := calcDifficulty(hdr.BlockHash())
	if math.Abs(shareDiff-expectedShareDiff) > 0.001 {
		t.Errorf("share diff mismatch: expected %f, got %f", expectedShareDiff, shareDiff)
		return
	}

	serializedHeader := bytes.NewBuffer([]byte{})
	hdr.Serialize(serializedHeader)
	t.Logf("sharediff: %g", shareDiff)
	t.Logf("header: %s", hex.EncodeToString(serializedHeader.Bytes()))
	t.Logf("hash: %s", hdr.BlockHash())
}
