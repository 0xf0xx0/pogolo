package main

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"slices"
	"strconv"
	"time"

	"git.0xf0xx0.eth.limo/0xf0xx0/pogolo/constants"

	"git.0xf0xx0.eth.limo/0xf0xx0/stratum"
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
	MsgBlock     wire.MsgBlock
	Bits         []byte
	MerkleBranch []*chainhash.Hash
	ID           string
	NetworkDiff  float64
	Subsidy      int64
	Height       int64
}
type MiningJob struct {
	stratum.MiningNotifyParams
	Block        btcutil.Block
	MerkleBranch []*chainhash.Hash
	NetworkDiff  float64
	Version      int32
}

func CreateJobTemplate(template *btcjson.GetBlockTemplateResult) (*JobTemplate, error) {
	/// set the block timestamp, clamping to min and max time
	currTime := time.Now().Unix()
	if template.MinTime > currTime {
		currTime = template.MinTime
	}
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
	cb, err := CreateEmptyCoinbase(template)
	if err != nil {
		return nil, err
	}
	txns[0] = cb

	/// this merkle tree is for the header merkle root, created from the block txids
	merkleTree := blockchain.BuildMerkleTreeStore(txns, false)
	merkleBranches := BuildMerkleProof(merkleTree, txns[0].Hash())
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
			Version:    template.Version,
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
		ID:           strconv.FormatUint(currTemplateID, 16),
		MsgBlock:     block,
		MerkleBranch: merkleBranch,
		Bits:         bits,
		NetworkDiff:  CalcNetworkDifficulty(headerBits),
		Subsidy:      *template.CoinbaseValue,
		Height:       template.Height,
	}
	return job, nil
}

// like public-pools copyAndUpdateBlock without the copy
func (job *MiningJob) UpdateBlock(id stratum.ID, share stratum.Share, notif stratum.MiningNotifyParams) (*wire.MsgBlock, error) {
	/// because we copied the block from the template when making the job, we can just reuse it
	msgBlock := job.Block.MsgBlock()

	if len(share.ExtraNonce2) != int(conf.Pogolo.ExtraNonce2Size) {
		return nil, errors.New("invalid extranonce2 size " + strconv.Itoa(int(conf.Pogolo.ExtraNonce2Size)))
	}

	/// mutate the coinbase script with the client id and extranonce2
	coinbaseMsgTx := msgBlock.Transactions[0]
	sigscript := coinbaseMsgTx.TxIn[0].SignatureScript
	coinbaseMsgTx.TxIn[0].SignatureScript = slices.Replace(sigscript,
		len(sigscript)-(constants.EXTRANONCE_SIZE+int(conf.Pogolo.ExtraNonce2Size)),
		len(sigscript),
		append(id.Bytes(), share.ExtraNonce2...)...,
	)

	/// update the header
	msgBlock.Header.Nonce = share.Nonce
	msgBlock.Header.Version = job.Version + int32(share.VersionMask)
	msgBlock.Header.Timestamp = time.Unix(int64(share.Time), 0)

	/// coinbase was changed, thus recalc the root
	coinbaseTx := btcutil.NewTx(coinbaseMsgTx)
	branches := make([]*chainhash.Hash, 1, len(job.MerkleBranch)+1)
	branches[0] = coinbaseTx.Hash()
	branches = append(branches, job.MerkleBranch...)
	msgBlock.Header.MerkleRoot = *MerkleRootFromBranches(branches)

	return msgBlock, nil
}
