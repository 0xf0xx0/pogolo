package main_test

import (
	"bytes"
	"encoding/hex"
	"testing"

	main "pogolo"

	"github.com/0xf0xx0/stratum"
	"github.com/btcsuite/btcd/blockchain"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/mining"
)

func TestWitnessCalc(t *testing.T) {
	template := MOCK_BLOCK_TEMPLATE
	txns := make([]*btcutil.Tx, len(template.Transactions)+1) /// add a slot for the coinbase
	for idx, templateTx := range template.Transactions {
		decoded, err := hex.DecodeString(templateTx.Data)
		if err != nil {
			println(idx, templateTx.Data)
			panic(err)
		}
		tx, err := btcutil.NewTxFromBytes(decoded)
		if err != nil {
			println(idx, templateTx.Data)
			panic(err)
		}
		txns[idx+1] = tx
	}
	txns[0] = main.CreateEmptyCoinbase(template)
	witnessCommit := hex.EncodeToString(mining.AddWitnessCommitment(txns[0], txns))
	t.Log(witnessCommit)
	t.Log(template.DefaultWitnessCommitment[12:])
}

func TestUpdateBlock(t *testing.T) {
	template := MOCK_BLOCK_TEMPLATE_MERKLE
	expectedShareDiff := MOCK_SHAREDIFF_MERKLE
	id, _ := stratum.DecodeID(MOCK_EXTRANONCE_MERKLE)
	client := &main.StratumClient{
		ID:   id,
		User: getAddr(),
	}
	job := main.CreateJobTemplate(template)
	blk, _ := job.UpdateBlock(client, submitParamsMerkle, notifyParamsMerkle)
	shareDiff := main.CalcDifficulty(blk.Header)
	if shareDiff != expectedShareDiff {
		t.Errorf("share diff mismatch: expected %f, got %f", expectedShareDiff, shareDiff)
	}
	serializedHeader := bytes.NewBuffer([]byte{})
	blk.Header.Serialize(serializedHeader)
	t.Logf("sharediff: %g", shareDiff)
	t.Logf("header: %s", hex.EncodeToString(serializedHeader.Bytes()))
	t.Logf("hash: %s", blk.BlockHash())
}

func TestCreateJobTemplate(t *testing.T) {
	template := MOCK_BLOCK_TEMPLATE
	job := main.CreateJobTemplate(template)

	if job.Height != template.Height || job.Block.Height() != int32(template.Height) {
		t.Errorf("job height mismatch: expected %d, got job: %d and block: %d", template.Height, job.Height, job.Block.Height())
	}
	if job.Subsidy != *template.CoinbaseValue {
		t.Errorf("job coinbase value mismatch, expected %d, got %d", template.CoinbaseValue, job.Subsidy)
	}
	jobBits := hex.EncodeToString(job.Bits)
	if jobBits != template.Bits {
		t.Errorf("job bits mismatch, expected %s, got %s", template.Bits, jobBits)
	}
	if job.Block.MsgBlock().Header.PrevBlock.String() != template.PreviousHash {
		t.Errorf("job prevhash mismatch, expected %s, got %s",
			template.PreviousHash,
			job.Block.MsgBlock().Header.PrevBlock.String())
	}
	/// .DefaultWitnessCommitment includes the magic bytes, trim em before comparing
	expectedWitnessCommitment := template.DefaultWitnessCommitment[12:]
	if hex.EncodeToString(job.WitnessCommittment) != expectedWitnessCommitment {
		t.Errorf("job witness mismatch, expected %s, got %s",
			expectedWitnessCommitment,
			hex.EncodeToString(job.WitnessCommittment))
	}
	/// TODO/FIXME: how to validate merkle root?
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
	/// TODO: verify the block height is at the start
}
