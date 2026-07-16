package main

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"slices"
	"time"

	"git.0xf0xx0.eth.limo/0xf0xx0/stratum"
	"github.com/btcsuite/btcd/blockchain"
	"github.com/btcsuite/btcd/btcjson"
	"github.com/btcsuite/btcd/btcutil/v2"
	"github.com/btcsuite/btcd/chainhash/v2"
	"github.com/btcsuite/btcd/mining"
	"github.com/btcsuite/btcd/wire/v2"
)

//////
/// public-pool my beloved

// a JobTemplate is built from a getblocktemplate call,
// and is sent to clients to be turned into a MiningJob
type JobTemplate struct {
	MsgBlock     wire.MsgBlock
	MerkleBranch []*chainhash.Hash
	NetworkDiff  float64
	ID           uint64
	Subsidy      int64
	Height       int64
	MinTime      int64
	MaxTime      int64
	Bits         [4]byte
}

// MiningJob is built from a JobTemplate and is used to construct mining.notify/NewMiningJob mesages
// and store mining state
type MiningJob struct {
	Header                 wire.BlockHeader
	CoinbasePart1          []byte
	CoinbasePart2          []byte
	MerkleBranch           []*chainhash.Hash // merkle tree describing the block txns (root is in header, and is mutated by the client)
	CoinbaseTx             *wire.MsgTx
	CoinbaseBytes          []byte          // for faster merkle root calc
	CoinbaseExtranonce2Idx int             // for faster merkle root calc
	PrevBlock              *chainhash.Hash // pointer to hash in header
	NetworkDiff            float64
	ID                     uint64
	MinTime                int64
	MaxTime                int64
	Version                int32 // the original job version
	Bits                   [4]byte
}

// like public-pools copyAndUpdateBlock without the copy
func (job *MiningJob) UpdateHeader(id stratum.ID, share *commonShare) (*wire.BlockHeader, bool) {
	if share.Extranonce2 == nil {
		return nil, false
	}
	en2Len := len(share.Extranonce2)
	if en2Len != int(conf.ExtraNonce2Size) {
		return nil, false
	}

	/// mutate the coinbase script with the extranonce2 (client id is done in fillCoinbaseTx)
	/// also mutate the coinbase bytes directly for faster merkle root calc
	/// MAYBE: precalc len
	sigscriptLen := len(job.CoinbaseTx.TxIn[0].SignatureScript)
	copy(job.CoinbaseTx.TxIn[0].SignatureScript[sigscriptLen-en2Len:], share.Extranonce2)
	copy(job.CoinbaseBytes[job.CoinbaseExtranonce2Idx:job.CoinbaseExtranonce2Idx+en2Len], share.Extranonce2)

	/// update the header
	job.Header.Nonce = share.Nonce
	job.Header.Version = int32(share.Version)
	job.Header.Timestamp = time.Unix(int64(share.Time), 0)

	/// coinbase was changed, thus recalc the root
	branches := make([]*chainhash.Hash, 1, len(job.MerkleBranch)+1)
	coinbaseTxHash := simdSha256d(job.CoinbaseBytes)
	branches[0] = &coinbaseTxHash
	branches = append(branches, job.MerkleBranch...)
	job.Header.MerkleRoot = *merkleRootFromBranches(branches)

	return &job.Header, true
}

func CreateJobTemplate(template *btcjson.GetBlockTemplateResult) (*JobTemplate, error) {
	/// set the block timestamp, clamping to min and max time
	currTime := max(template.MinTime, time.Now().Unix())
	if template.MaxTime > template.MinTime && template.MaxTime < currTime {
		currTime = template.MaxTime
	}

	/// bitties on the yitties
	bits, err := hex.DecodeString(template.Bits)
	if err != nil {
		return nil, err
	}
	/// represented as uint32 for wire.MsgHeader and diff calc
	headerBits := binary.BigEndian.Uint32(bits)

	prevBlockHash, err := chainhash.NewHashFromStr(template.PreviousHash)
	if err != nil {
		return nil, err
	}
	// add a slot for the coinbase
	txns := make([]*btcutil.Tx, len(template.Transactions)+1)

	/// decode the serialized txns into nice lil btcutil.Txs
	for idx, templateTx := range template.Transactions {
		decoded, err := hex.DecodeString(templateTx.Data)
		if err != nil {
			println(idx, templateTx.Data)
			return nil, err
		}
		tx, err := btcutil.NewTxFromBytes(decoded)
		if err != nil {
			println(idx, templateTx.Data)
			return nil, err
		}
		/// skip 0, thats the coinbase slot
		txns[idx+1] = tx
	}

	/// create temp coinbase
	cb, err := createEmptyCoinbase(template)
	if err != nil {
		return nil, err
	}
	txns[0] = cb
	/// NOTE: witness gets added furst, just cause its *unique*
	mining.AddWitnessCommitment(txns[0], txns)

	/// this merkle tree describes the block txids and will be used to create the merkle root
	merkleTree := blockchain.BuildMerkleTreeStore(txns, false)
	merkleBranches := buildMerkleProof(merkleTree, txns[0].Hash())
	/// prune empty branches
	merkleBranches = slices.DeleteFunc(merkleBranches, func(h *chainhash.Hash) bool {
		return h == nil
	})

	/// skip over the merkle root (its replaced in fillCoinbaseTx)
	merkleBranches = merkleBranches[:len(merkleBranches)-1]

	merkleBranch := []*chainhash.Hash{}
	/// theres only 1 branch with empty bl00ks, the coinbase hash
	if len(merkleBranches) > 1 {
		merkleBranch = merkleBranches[1:]
	}

	msgTxns := make([]*wire.MsgTx, len(txns))
	for idx, tx := range txns {
		msgTxns[idx] = tx.MsgTx()
	}

	block := wire.MsgBlock{
		Header: wire.BlockHeader{
			/// OR configured activation bits with template from node
			Version:   template.Version | conf.BIPVersionBits,
			Bits:      headerBits,
			PrevBlock: *prevBlockHash,
			Timestamp: time.Unix(currTime, 0),
		},
		Transactions: msgTxns,
	}

	if block.SerializeSize() > blockchain.MaxBlockWeight {
		err := errors.New("block too heavy, please reduce blockmaxweight in your node config")
		return nil, err
	}

	currTemplateID++
	job := &JobTemplate{
		ID:           currTemplateID,
		MsgBlock:     block,
		MerkleBranch: merkleBranch,
		Bits:         [4]byte(bits),
		NetworkDiff:  calcNetworkDifficulty(headerBits),
		Subsidy:      *template.CoinbaseValue,
		Height:       template.Height,
		/// pass min and max time through for share validation
		MinTime: template.MinTime,
		MaxTime: template.MaxTime,
	}
	return job, nil
}
