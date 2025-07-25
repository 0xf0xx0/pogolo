package stratumclient_test

import (
	"bytes"
	"encoding/hex"
	"pogolo/stratumclient"
	"testing"

	"github.com/btcsuite/btcd/blockchain"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/mining"
	stratum "github.com/kbnchk/go-Stratum"
)

const (
	HEIGHT = 0
)

func TestWitnessCalc(t *testing.T) {
	template := getBlockTemplate()
	txns := make([]*btcutil.Tx, len(template.Transactions)+1) /// add a slot for the coinbase
	decoded, _ := hex.DecodeString(MOCK_EMPTY_COINBASE)
	txns[0], _ = btcutil.NewTxFromBytes(decoded)
	witnessCommit := hex.EncodeToString(mining.AddWitnessCommitment(txns[0], txns))
	t.Log(witnessCommit)
	t.Log(template.DefaultWitnessCommitment[12:])
}

func TestUpdateBlock(t *testing.T) {
	expectedShareDiff := float64(0.22783314248308514)
	id, _ := stratum.DecodeID(MOCK_EXTRANONCE)
	user := getAddr()
	client := &stratumclient.StratumClient{
		ID:   id,
		User: &user,
	}
	template := getBlockTemplate()
	job := stratumclient.CreateJobTemplate(template)
	job.UpdateBlock(client, submitParams, notifyParams)
	shareDiff, _ := stratumclient.CalcDifficulty(job.Block.MsgBlock().Header)
	if shareDiff != expectedShareDiff {
		t.Fatalf("share diff mismatch: expected %f, got %f", expectedShareDiff, shareDiff)
	}
	serializedHeader := bytes.NewBuffer([]byte{})
	job.Block.MsgBlock().Header.Serialize(serializedHeader)
	t.Logf("sharediff: %g", shareDiff)
	t.Logf("header: %s", hex.EncodeToString(serializedHeader.Bytes()))
	t.Logf("hash: %s", job.Block.Hash())
	blk,_ := stratumclient.SerializeBlock(&job.Block)
	t.Logf("block: %x", blk)
}

func TestJobTemplate(t *testing.T) {
	template := getBlockTemplate()
	job := stratumclient.CreateJobTemplate(template)
	if job.Height != template.Height || job.Block.Height() != int32(template.Height) {
		t.Errorf("job height mismatch: expected %d, got %d and %d", template.Height, job.Height, job.Block.Height())
	}
	if job.Subsidy != *template.CoinbaseValue {
		t.Errorf("job coinbase value mismatch, expected %d, got %d", template.CoinbaseValue, job.Subsidy)
	}
	jobBits := hex.EncodeToString(job.Bits)
	if jobBits != template.Bits {
		t.Errorf("job bits mismatch, expected %s, got %s", template.Bits, jobBits)
	}
	if len(notifyParams.MerkleBranches) != 0 {
		t.Errorf("job merkle branch mismatch, expected %s, got %s", template.Bits, jobBits)
		t.Errorf("job merkle mismatch, expected empty, got %+v", notifyParams.MerkleBranches)
	}
	if job.Block.MsgBlock().Header.PrevBlock.String() != template.PreviousHash {
		t.Errorf("job prevhash mismatch, expected %s, got %s",
			template.PreviousHash,
			job.Block.MsgBlock().Header.PrevBlock.String())
	}
	/// .DefaultWitnessCommitment includes the magic bytes, trim em before comparing
	witnessCommitment := template.DefaultWitnessCommitment[12:]
	if hex.EncodeToString(job.WitnessCommittment) != witnessCommitment {
		t.Errorf("job witness mismatch, expected %s, got %s",
			witnessCommitment,
			hex.EncodeToString(job.WitnessCommittment))
	}
	t.Logf("validated job: %+v", job)
}

func TestSubmission(t *testing.T) {

}

func TestCoinbaseWeight(t *testing.T) {
	tx := getCoinbaseTx()
	coinbaseWeight := blockchain.GetTransactionWeight(tx)
	if coinbaseWeight > blockchain.MaxBlockWeight {
		t.Errorf("block too heavy: %d, max %d", coinbaseWeight, blockchain.MaxBlockWeight)
	}
	t.Logf("weight: %d", coinbaseWeight)
}

func TestCoinbaseScript(t *testing.T) {
	tx := getCoinbaseTx()

	coinbaseScript := tx.MsgTx().TxIn[0].SignatureScript

	if len(coinbaseScript) > blockchain.MaxCoinbaseScriptLen {
		t.Errorf("pool identifier too long: max %d, got %d", blockchain.MaxCoinbaseScriptLen, len(coinbaseScript))
	}
	t.Logf("%q", coinbaseScript)
	t.Logf("coinbase script: %x", coinbaseScript)
}

func TestValidatePkScript(t *testing.T) {
	tx := getCoinbaseTx()

	pkscript := tx.MsgTx().TxOut[0].PkScript
	t.Logf("pkscript: %x", pkscript)
}

func TestValidateCoinbaseScript(t *testing.T) {
	tx := getCoinbaseTx()
	txIn := tx.MsgTx().TxIn[0]
	script := txIn.SignatureScript

	if len(script) > blockchain.MaxCoinbaseScriptLen {
		t.Errorf("coinbase script too long: %d, max %d", len(script), blockchain.MaxCoinbaseScriptLen)
	}
	t.Logf("script len: %d, max: %d", len(script), blockchain.MaxCoinbaseScriptLen)
}
