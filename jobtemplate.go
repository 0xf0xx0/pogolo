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
	"github.com/btcsuite/btcd/chainhash/v2"
	"github.com/btcsuite/btcd/wire/v2"
)

//////
/// public-pool my beloved
/// pogolo still has its genes

// a JobTemplate is built from a getblocktemplate call,
// and is sent to clients to be turned into a MiningJob
type JobTemplate struct {
	Header       wire.BlockHeader // copied by clients
	MerkleBranch []*chainhash.Hash
	CoinbaseTx   *wire.MsgTx // copied by clients
	RawTxns      []byte      // serialized non-coinbase transactions, including witness data
	NumTxns      uint64      // total transaction count, including the coinbase
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
	CoinbaseTx             wire.MsgTx
	CoinbaseBytes          []byte            // for faster merkle root calc
	MerkleBranch           []*chainhash.Hash // merkle tree describing the block txns (root is in header, and is mutated by the client)
	merkleScratch          []*chainhash.Hash // preallocated [coinbase, ...MerkleBranch] scratch for UpdateHeader; slot 0 points to a per-job buffer
	CoinbaseExtranonce2Idx int               // for faster merkle root calc
	PrevBlock              *chainhash.Hash   // pointer to hash in header
	RawTxns                []byte            // shared, pre-serialized non-coinbase transactions
	NumTxns                uint64            // total transaction count, including the coinbase
	NetworkDiff            float64
	ID                     uint64
	MinTime                int64
	MaxTime                int64
	Version                int32 // the original job version
	Height                 int32 // used on block submission only, NOT copied or modified by clients
	Bits                   [4]byte
}

// like public-pools copyAndUpdateBlock without the copy
func (job *MiningJob) UpdateHeader(id stratum.ID, share *commonShare) (*wire.BlockHeader, bool) {
	if share.Extranonce2 != nil {
		en2Len := len(share.Extranonce2)
		if en2Len != int(conf.ExtraNonce2Size) {
			return nil, false
		}

		/// mutate the coinbase script with the extranonce2 (client id is done in fillCoinbaseTx)
		/// also mutate the coinbase bytes directly for faster merkle root calc
		sigscriptLen := len(job.CoinbaseTx.TxIn[0].SignatureScript)

		copy(job.CoinbaseTx.TxIn[0].SignatureScript[sigscriptLen-en2Len:], share.Extranonce2)
		copy(job.CoinbaseBytes[job.CoinbaseExtranonce2Idx:job.CoinbaseExtranonce2Idx+en2Len], share.Extranonce2)

		/// coinbase was changed, thus recalc the root
		*job.merkleScratch[0] = simdSha256d(job.CoinbaseBytes)
		job.Header.MerkleRoot = merkleRootFromBranches(job.merkleScratch)
	}

	/// update the header
	job.Header.Nonce = share.Nonce
	job.Header.Version = int32(share.Version)
	job.Header.Timestamp = time.Unix(int64(share.Time), 0)

	return &job.Header, true
}

func addWitnessCommitment(coinbase *wire.MsgTx, wtxns []*chainhash.Hash) {
	/// the coinbase witness nonce is exactly 32 zero bytes
	var witnessNonce [32]byte
	coinbase.TxIn[0].Witness = wire.TxWitness{witnessNonce[:]}

	/// and its wtxid is all zeroes for the commitment tree
	wtxns[0] = &chainhash.Hash{}
	merkleTree := buildMerkleTreeFromHashes(wtxns)
	witnessRoot := merkleTree[len(merkleTree)-1]

	var preimage [64]byte
	copy(preimage[:32], witnessRoot[:])
	copy(preimage[32:], witnessNonce[:])
	commitment := simdSha256d(preimage[:])

	/// OP_RETURN OP_DATA_36 {0xaa21a9ed || commitment}
	script := make([]byte, 0, len(blockchain.WitnessMagicBytes)+len(commitment))
	script = append(script, blockchain.WitnessMagicBytes...)
	script = append(script, commitment[:]...)
	coinbase.TxOut[0] = &wire.TxOut{Value: 0, PkScript: script}
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

	/// txids and wtxids come straight from GBT; only the raw transaction bytes
	/// are retained, never the decoded transaction object graph
	txns := make([]*chainhash.Hash, len(template.Transactions)+1)
	wtxns := make([]*chainhash.Hash, len(template.Transactions)+1)
	rawSize := 0
	for _, templateTx := range template.Transactions {
		rawSize += hex.DecodedLen(len(templateTx.Data))
	}
	rawTxns := make([]byte, rawSize)
	offset := 0
	for idx, templateTx := range template.Transactions {
		txLen := hex.DecodedLen(len(templateTx.Data))
		n, err := hex.Decode(rawTxns[offset:offset+txLen], []byte(templateTx.Data))
		if err != nil {
			return nil, err
		}
		offset += n

		txid, err := chainhash.NewHashFromStr(templateTx.TxID)
		if err != nil {
			return nil, err
		}
		wtxid, err := chainhash.NewHashFromStr(templateTx.Hash)
		if err != nil {
			return nil, err
		}
		txns[idx+1] = txid
		wtxns[idx+1] = wtxid
	}

	/// create temp coinbase
	cb, err := createEmptyCoinbase(template)
	if err != nil {
		return nil, err
	}
	/// NOTE: witness gets added furst, just cause its *unique*
	addWitnessCommitment(cb, wtxns)
	coinbaseHash := simdSha256d(serializeCoinbaseTx(cb))
	txns[0] = &coinbaseHash

	/// this merkle tree describes the block txids and will be used to create the merkle root
	merkleTree := buildMerkleTreeFromHashes(txns)
	merkleBranches := buildMerkleProof(merkleTree, txns[0])
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

	/// the serialized non-coinbase transactions are immutable; block submission
	/// only needs to serialize the header and the client-specific coinbase
	numTxns := uint64(len(template.Transactions) + 1)
	blockSize := 80 + wire.VarIntSerializeSize(numTxns) + cb.SerializeSize() + len(rawTxns)
	if blockSize > blockchain.MaxBlockWeight {
		return nil, errors.New("block too heavy, please reduce blockmaxweight in your node config")
	}

	currTemplateID++
	job := &JobTemplate{
		ID: currTemplateID,
		Header: wire.BlockHeader{
			/// OR configured activation bits with template from node
			Version:   template.Version | conf.BIPVersionBits,
			Bits:      headerBits,
			PrevBlock: *prevBlockHash,
			Timestamp: time.Unix(currTime, 0),
		},
		CoinbaseTx:   cb,
		RawTxns:      rawTxns,
		NumTxns:      numTxns,
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
