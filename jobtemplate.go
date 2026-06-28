package main

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"slices"
	"time"

	"git.0xf0xx0.eth.limo/0xf0xx0/pogolo/constants"

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

type JobTemplate struct {
	MsgBlock     wire.MsgBlock
	Bits         []byte
	MerkleBranch []*chainhash.Hash
	ID           uint64
	NetworkDiff  float64
	Subsidy      int64
	Height       int64
	MinTime      int64
	MaxTime      int64
}
type MiningJob struct {
	JobIDInt           uint64
	Header             wire.BlockHeader
	CoinbaseTx         *btcutil.Tx
	MerkleBranch       []*chainhash.Hash
	NetworkDiff        float64
	Version            int32
	MinTime            int64
	MaxTime            int64
	PrevHash           *chainhash.Hash
	CoinbasePart1      []byte
	CoinbasePart2      []byte
	Timestamp          time.Time
	Bits               []byte
	MiningNotifyParams stratum.MiningNotifyParams // for sv1
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

	/// this merkle tree is for the header merkle root, created from the block txids
	merkleTree := blockchain.BuildMerkleTreeStore(txns, false)
	merkleBranches := buildMerkleProof(merkleTree, txns[0].Hash())
	/// prune empty branches
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

	block := wire.MsgBlock{
		Header: wire.BlockHeader{
			/// OR configured activation bits with template from node
			Version:    template.Version | conf.Pogolo.BIPVersionBits,
			Bits:       headerBits,
			PrevBlock:  *prevBlockHash,
			Timestamp:  time.Unix(currTime, 0),
			MerkleRoot: *merkleRoot,
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
		Bits:         bits,
		NetworkDiff:  calcNetworkDifficulty(headerBits),
		Subsidy:      *template.CoinbaseValue,
		Height:       template.Height,
		/// pass min and max time through for share validation
		MinTime: template.MinTime,
		MaxTime: template.MaxTime,
	}
	return job, nil
}

// like public-pools copyAndUpdateBlock without the copy
func (job *MiningJob) UpdateHeader(id stratum.ID, share commonShare, notif stratum.MiningNotifyParams) (wire.BlockHeader, bool) {
	en2Len := 0
	if share.Extranonce2 != nil {
		en2Len = len(share.Extranonce2)
		if en2Len > 0 && en2Len != int(conf.Pogolo.ExtraNonce2Size) {
			return wire.BlockHeader{}, false
		}
	}

	/// mutate the coinbase script with the client id and extranonce2
	coinbaseMsgTx := job.CoinbaseTx.MsgTx()
	sigscript := coinbaseMsgTx.TxIn[0].SignatureScript
	coinbaseMsgTx.TxIn[0].SignatureScript = slices.Replace(sigscript,
		len(sigscript)-(constants.EXTRANONCE_SIZE+en2Len),
		len(sigscript),
		append(id.Bytes(), share.Extranonce2...)...,
	)

	/// update the header
	job.Header.Nonce = share.Nonce
	job.Header.Version = int32(share.Version)
	job.Header.Timestamp = time.Unix(int64(share.Time), 0)

	/// coinbase was changed, thus recalc the root
	branches := make([]*chainhash.Hash, 1, len(job.MerkleBranch)+1)
	coinbaseTxHash := coinbaseMsgTx.TxHash()
	branches[0] = &coinbaseTxHash
	branches = append(branches, job.MerkleBranch...)
	job.Header.MerkleRoot = *merkleRootFromBranches(branches)

	return job.Header, true
}
