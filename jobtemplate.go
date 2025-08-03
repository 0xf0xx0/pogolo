package main

import (
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
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
	ID                 string
	Block              btcutil.Block
	WitnessCommittment []byte
	MerkleBranch       []*chainhash.Hash
	MerkleRoot         *chainhash.Hash
	NetworkDiff        float64
	Bits               []byte
	Subsidy            int64
	Height             int64
	Clear              bool
}
type MiningJob struct {
	Template     *JobTemplate
	NotifyParams stratum.NotifyParams
}

var (
	currTemplateID  = uint64(0)
	currBlockHeight = uint64(0)
)

func CreateJobTemplate(template *btcjson.GetBlockTemplateResult) *JobTemplate {
	/// TODO/MAYBE: false and keep track of multiple jobs
	/// is it neccesary?
	clear := true

	if currBlockHeight != uint64(template.Height) {
		currBlockHeight = uint64(template.Height)
	}

	currTime := time.Now().Unix()
	if template.MinTime > currTime {
		currTime = template.MinTime
	}
	headerBits, _ := strconv.ParseUint(template.Bits, 16, 32)
	prevBlockHash, _ := chainhash.NewHashFromStr(template.PreviousHash)

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
	witnessCommit := mining.AddWitnessCommitment(txns[0], txns)

	msgTxns := make([]*wire.MsgTx, len(txns))
	for idx, tx := range txns {
		msgTxns[idx] = tx.MsgTx()
	}

	block := btcutil.NewBlock(&wire.MsgBlock{
		Header: wire.BlockHeader{
			Version:    template.Version,
			Bits:       uint32(headerBits),
			PrevBlock:  *prevBlockHash,
			Timestamp:  time.Unix(currTime, 0),
			MerkleRoot: *merkleRoot,
		},
		Transactions: msgTxns,
	})
	block.SetHeight(int32(template.Height))

	/// bitties on the yitties
	bits, _ := hex.DecodeString(template.Bits)
	job := &JobTemplate{
		ID:                 getNextTemplateID(),
		Block:              *block,
		WitnessCommittment: witnessCommit,
		MerkleBranch:       merkleBranch,
		MerkleRoot:         merkleRoot,
		Bits:               bits,
		NetworkDiff:        CalcNetworkDifficulty(uint32(headerBits)),
		Subsidy:            *template.CoinbaseValue,
		Height:             template.Height,
		Clear:              clear,
	}

	return job
}

// counter but hex so its super hi-tek
func getNextTemplateID() string {
	currTemplateID++
	return fmt.Sprintf("%x", currTemplateID)
}

// like public-pools copyAndUpdateBlock
func (template *JobTemplate) UpdateBlock(client *StratumClient, share stratum.Share, notif stratum.NotifyParams) (*wire.MsgBlock, error) {
	msgBlock := template.Block.MsgBlock().Copy()

	coinbase := hex.EncodeToString(notif.CoinbasePart1) + client.ID.String() +
		hex.EncodeToString(share.ExtraNonce2) + hex.EncodeToString(notif.CoinbasePart2)
	decodedCoinbase, err := hex.DecodeString(coinbase)
	if err != nil {
		return nil, err
	}
	tx, err := btcutil.NewTxFromBytes(decodedCoinbase)
	if err != nil {
		return nil, err
	}

	msgBlock.Transactions[0] = tx.MsgTx()
	msgBlock.Header.Nonce = share.Nonce
	msgBlock.Header.Timestamp = time.Unix(int64(share.Time), 0)

	if share.VersionMask != 0 {
		msgBlock.Header.Version += int32(share.VersionMask)
	}

	/// coinbase was changed, thus recalc the root
	branches := append([]*chainhash.Hash{tx.Hash()}, template.MerkleBranch...)
	msgBlock.Header.MerkleRoot = *merkleRootFromBranches(branches)

	return msgBlock, nil
}
