package main

import (
	"encoding/hex"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/0xf0xx0/stratum"
	"github.com/btcsuite/btcd/blockchain"
	"github.com/btcsuite/btcd/btcjson"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/mining"
	"github.com/btcsuite/btcd/wire"
)

//////
/// public-pool my beloved

type JobTemplate struct {
	ID           string
	MsgBlock     *wire.MsgBlock
	Bits         []byte
	MerkleBranch []*chainhash.Hash
	NetworkDiff  float64
	Subsidy      int64
	Height       int64
}
type MiningJob struct {
	stratum.NotifyParams
	MerkleBranch []*chainhash.Hash
	Block        *btcutil.Block
	Version      int32
	NetworkDiff  float64
}

func CreateJobTemplate(template *btcjson.GetBlockTemplateResult) *JobTemplate {

	currTime := time.Now().Unix()
	if template.MinTime > currTime {
		currTime = template.MinTime
	}
	headerBits, _ := strconv.ParseUint(template.Bits, 16, 32)
	prevBlockHash, _ := chainhash.NewHashFromStr(template.PreviousHash)

	txns := make([]*btcutil.Tx, len(template.Transactions)+1) /// add a slot for the coinbase
	/// decode the serialized txns into nice lil btcutil.Txs
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
		/// skip 0, thats the coinbase slot
		txns[idx+1] = tx
	}

	/// create temp coinbase
	txns[0] = CreateEmptyCoinbase(template)

	/// this merkle tree is for the header merkle root, created from the block txids
	merkleTree := blockchain.BuildMerkleTreeStore(txns, false)
	merkleBranches := BuildMerkleProof(merkleTree, txns[0].Hash())
	merkleBranches = slices.DeleteFunc(merkleBranches, func(h *chainhash.Hash) bool {
		return h == nil
	})

	merkleRoot := merkleBranches[len(merkleBranches)-1]
	merkleBranches = merkleBranches[:len(merkleBranches)-1]

	merkleBranch := []*chainhash.Hash{}
	/// theres only 1 branch with empty bl00ks
	if len(merkleBranches) > 1 {
		merkleBranch = merkleBranches[1:]
	}
	/// btcd does the witness merkle root for us :3
	/// thisll be updated on share submission
	mining.AddWitnessCommitment(txns[0], txns)

	msgTxns := make([]*wire.MsgTx, len(txns))
	for idx, tx := range txns {
		msgTxns[idx] = tx.MsgTx()
	}

	block := &wire.MsgBlock{
		Header: wire.BlockHeader{
			Version:    template.Version,
			Bits:       uint32(headerBits),
			PrevBlock:  *prevBlockHash,
			Timestamp:  time.Unix(currTime, 0),
			MerkleRoot: *merkleRoot,
		},
		Transactions: msgTxns,
	}

	/// bitties on the yitties
	bits, _ := hex.DecodeString(template.Bits)
	currTemplateID++
	job := &JobTemplate{
		ID:           strconv.FormatUint(currTemplateID, 16),
		MsgBlock:     block,
		MerkleBranch: merkleBranch,
		Bits:         bits,
		NetworkDiff:  CalcNetworkDifficulty(uint32(headerBits)),
		Subsidy:      *template.CoinbaseValue,
		Height:       template.Height,
	}
	return job
}

// like public-pools copyAndUpdateBlock without the copy
func (job *MiningJob) UpdateBlock(client *StratumClient, share stratum.Share, notif stratum.NotifyParams) (*wire.MsgBlock, error) {
	/// because we copied the block from the template when making the job, we can just reuse it
	msgBlock := job.Block.MsgBlock()

	if len(share.ExtraNonce2) != int(conf.Pogolo.ExtraNonce2Size) {
		return nil, errors.New("invalid extranonce2 size " + strconv.Itoa(int(conf.Pogolo.ExtraNonce2Size)))
	}

	coinbase := strings.Builder{}
	/// alloc enough space for the cb (we're goin for speed so 512 is enough without over-allocing)
	/// assuming avg tx size of 330, alloc at most 660
	/// our coinbase is at max 256 bytes, nice
	coinbase.Grow(512)
	coinbase.WriteString(hex.EncodeToString(notif.CoinbasePart1))
	coinbase.WriteString(client.ID.String())
	coinbase.WriteString(hex.EncodeToString(share.ExtraNonce2))
	coinbase.WriteString(hex.EncodeToString(notif.CoinbasePart2))
	decodedCoinbase, err := hex.DecodeString(coinbase.String())
	if err != nil {
		return nil, err
	}
	coinbaseTx, err := btcutil.NewTxFromBytes(decodedCoinbase)
	if err != nil {
		return nil, err
	}

	msgBlock.Transactions[0] = coinbaseTx.MsgTx()

	/// update the header
	msgBlock.Header.Nonce = share.Nonce
	msgBlock.Header.Version = job.Version + int32(share.VersionMask)
	msgBlock.Header.Timestamp = time.Unix(int64(share.Time), 0)

	/// coinbase was changed, thus recalc the root
	branches := make([]*chainhash.Hash, 1, len(job.MerkleBranch)+1)
	branches[0] = coinbaseTx.Hash()
	branches = append(branches, job.MerkleBranch...)
	msgBlock.Header.MerkleRoot = *merkleRootFromBranches(branches)

	return msgBlock, nil
}
