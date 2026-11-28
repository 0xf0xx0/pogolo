package main

import (
	"bytes"
	"encoding/hex"
	"strconv"
	"testing"
	"time"

	"git.0xf0xx0.eth.limo/0xf0xx0/stratum"
	"github.com/btcsuite/btcd/blockchain"
	"github.com/btcsuite/btcd/btcjson"
	"github.com/btcsuite/btcd/btcutil/v2"
	"github.com/btcsuite/btcd/chainhash/v2"
	"github.com/btcsuite/btcd/wire/v2"
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
	if job.Header.PrevBlock.String() != template.PreviousHash {
		t.Errorf("job prevhash mismatch, expected %s, got %s",
			template.PreviousHash,
			job.Header.PrevBlock.String())
	}
}

func TestJobMinTime(t *testing.T) {
	template := *MOCK_BLOCK_TEMPLATE
	template.MinTime = time.Now().Unix() + 6000
	_, err := CreateJobTemplate(&template)
	if err != nil {
		t.Fatal(err.Error())
	}
}
func TestJobmaxTime(t *testing.T) {
	template := *MOCK_BLOCK_TEMPLATE
	template.MinTime = 0
	template.MaxTime = 6000
	_, err := CreateJobTemplate(&template)
	if err != nil {
		t.Fatal(err.Error())
	}
}

func TestValidateCoinbaseScript(t *testing.T) {
	tx := getCoinbaseTx()
	script := tx.TxIn[0].SignatureScript

	if len(script) > blockchain.MaxCoinbaseScriptLen {
		t.Errorf("coinbase script too long: %d, max %d", len(script), blockchain.MaxCoinbaseScriptLen)
	}
	t.Logf("%q", script)
	t.Logf("coinbase script: %x", script)
	t.Logf("script len: %d, max: %d", len(script), blockchain.MaxCoinbaseScriptLen)
}

func TestUpdateBlock(t *testing.T) {
	template := *MOCK_BLOCK_TEMPLATE
	targetShareDiff := 0.000069
	client := &StratumClient{
		ID:   stratum.ID(1234567890),
		User: getAddr(),
	}

	tml, _ := CreateJobTemplate(&template)
	job := client.createJob(tml)
	client.CurrentJob = &job

	share, found := shittyCpuMiner(targetShareDiff, false, client, nil, t)
	if !found {
		t.Error("failed to find a hash in 2^32 iters, bad luck")
		return
	}
	submitParams := &stratum.MiningSubmitParams{}
	submitParams.FromRequest(share)
	jobID, _ := strconv.ParseUint(submitParams.JobID, 16, 64)
	s := &commonShare{
		ChannelID:   0,
		JobID:       uint32(jobID),
		Time:        submitParams.Time,
		Version:     uint32(job.Version),
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
	if shareDiff < targetShareDiff {
		t.Errorf("bad share diff: expected >=%f, got %f", targetShareDiff, shareDiff)
		return
	}

	serializedHeader := bytes.NewBuffer([]byte{})
	hdr.Serialize(serializedHeader)
	t.Logf("sharediff: %g", shareDiff)
	t.Logf("header: %s", hex.EncodeToString(serializedHeader.Bytes()))
	t.Logf("hash: %s", hdr.BlockHash())
}

func makeTestTx(i int) *wire.MsgTx {
	tx := &wire.MsgTx{Version: 1, LockTime: uint32(i)}
	tx.AddTxIn(&wire.TxIn{
		PreviousOutPoint: wire.OutPoint{
			Hash:  chainhash.Hash{byte(i)},
			Index: uint32(i),
		},
		SignatureScript: []byte{0x51},
		Sequence:        0xfffffffe,
	})
	tx.AddTxOut(&wire.TxOut{Value: int64(1000 + i), PkScript: []byte{0x51}})
	if i%2 == 0 {
		tx.TxIn[0].Witness = wire.TxWitness{[]byte{byte(i), 0xde, 0xad}}
	}
	return tx
}

func TestMerkleTreeFromHashes(t *testing.T) {
	for _, count := range []int{1, 2, 3, 4, 5, 7, 8, 9, 16, 17, 100} {
		txs := make([]*btcutil.Tx, count)
		leaves := make([]*chainhash.Hash, count)
		wLeaves := make([]*chainhash.Hash, count)
		for i := range count {
			msg := makeTestTx(i + 1)
			tx := btcutil.NewTx(msg)
			txs[i] = tx
			leaves[i] = tx.Hash()
			wh := msg.WitnessHash()
			wLeaves[i] = &wh
		}

		want := blockchain.BuildMerkleTreeStore(txs, false)
		got := buildMerkleTreeFromHashes(leaves)
		if len(got) != len(want) {
			t.Fatalf("count %d: tree length mismatch: got %d, want %d", count, len(got), len(want))
		}
		for i := range want {
			if (got[i] == nil) != (want[i] == nil) {
				t.Fatalf("count %d: nil mismatch at %d", count, i)
			}
			if got[i] != nil && *got[i] != *want[i] {
				t.Fatalf("count %d: hash mismatch at %d", count, i)
			}
		}

		/// the witness tree uses the zero hash for the coinbase
		wLeaves[0] = &chainhash.Hash{}
		wantRoot := blockchain.CalcMerkleRoot(txs, true)
		wTree := buildMerkleTreeFromHashes(wLeaves)
		if gotRoot := wTree[len(wTree)-1]; *gotRoot != wantRoot {
			t.Fatalf("count %d: witness root mismatch", count)
		}
	}
}

func TestRawTemplateAndSubmission(t *testing.T) {
	template := *MOCK_BLOCK_TEMPLATE
	template.Transactions = nil
	msgs := []*wire.MsgTx{makeTestTx(1), makeTestTx(2), makeTestTx(3)}
	var wantRaw []byte
	for _, msg := range msgs {
		var buf bytes.Buffer
		if err := msg.Serialize(&buf); err != nil {
			t.Fatal(err)
		}
		template.Transactions = append(template.Transactions, btcjson.GetBlockTemplateResultTx{
			Data: hex.EncodeToString(buf.Bytes()),
			TxID: msg.TxHash().String(),
			Hash: msg.WitnessHash().String(),
		})
		wantRaw = append(wantRaw, buf.Bytes()...)
	}

	job, err := CreateJobTemplate(&template)
	if err != nil {
		t.Fatal(err)
	}
	if job.NumTxns != uint64(len(msgs)+1) {
		t.Fatalf("wrong tx count: %d", job.NumTxns)
	}
	if !bytes.Equal(job.RawTxns, wantRaw) {
		t.Fatal("raw non-coinbase transactions changed")
	}

	/// independently verify the merkle branch using btcd's transaction-based builder
	refTxns := []*btcutil.Tx{btcutil.NewTx(job.CoinbaseTx.Copy())}
	for _, msg := range msgs {
		refTxns = append(refTxns, btcutil.NewTx(msg.Copy()))
	}
	tree := blockchain.BuildMerkleTreeStore(refTxns, false)
	branches := buildMerkleProof(tree, refTxns[0].Hash())
	var wantBranch []*chainhash.Hash
	for _, branch := range branches[:len(branches)-1] {
		if branch != nil {
			wantBranch = append(wantBranch, branch)
		}
	}
	if len(wantBranch) > 1 {
		wantBranch = wantBranch[1:]
	} else {
		wantBranch = nil
	}
	if len(job.MerkleBranch) != len(wantBranch) {
		t.Fatalf("branch length mismatch: got %d, want %d", len(job.MerkleBranch), len(wantBranch))
	}
	for i := range wantBranch {
		if *job.MerkleBranch[i] != *wantBranch[i] {
			t.Fatalf("branch mismatch at %d", i)
		}
	}

	/// independently verify the witness commitment
	wantRoot := blockchain.CalcMerkleRoot(refTxns, true)
	var preimage [64]byte
	copy(preimage[:32], wantRoot[:])
	wantCommitment := simdSha256d(preimage[:])
	wantScript := append(blockchain.WitnessMagicBytes, wantCommitment[:]...)
	if !bytes.Equal(job.CoinbaseTx.TxOut[0].PkScript, wantScript) {
		t.Fatalf("witness commitment mismatch")
	}

	client := &StratumClient{ID: stratum.ID(1234567890), User: getAddr()}
	miningJob := client.createJob(job)
	share := &commonShare{
		Time:        uint32(miningJob.Header.Timestamp.Unix()),
		Version:     uint32(miningJob.Version),
		Nonce:       123,
		Extranonce2: make([]byte, conf.ExtraNonce2Size),
	}
	header, ok := miningJob.UpdateHeader(client.ID, share)
	if !ok {
		t.Fatal("failed to update header")
	}
	sub := blockSubmission{
		Header:   *header,
		Height:   miningJob.Height,
		RawTxns:  miningJob.RawTxns,
		NumTxns:  miningJob.NumTxns,
		Coinbase: miningJob.CoinbaseTx.Copy(),
		Share:    share,
	}
	blockBytes := serializeSubmissionBlock(sub)
	var block wire.MsgBlock
	if err := block.Deserialize(bytes.NewReader(blockBytes)); err != nil {
		t.Fatalf("assembled block does not deserialize: %s", err)
	}
	if block.Header != *header {
		t.Fatal("submission header mismatch")
	}
	if len(block.Transactions) != len(msgs)+1 {
		t.Fatalf("submission tx count mismatch: %d", len(block.Transactions))
	}
	for i, msg := range msgs {
		if block.Transactions[i+1].TxHash() != msg.TxHash() {
			t.Fatalf("submission tx %d changed", i+1)
		}
	}
	var wantCoinbase bytes.Buffer
	if err := sub.Coinbase.Serialize(&wantCoinbase); err != nil {
		t.Fatal(err)
	}
	var gotCoinbase bytes.Buffer
	if err := block.Transactions[0].Serialize(&gotCoinbase); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotCoinbase.Bytes(), wantCoinbase.Bytes()) {
		t.Fatal("submission coinbase changed during assembly")
	}
}
