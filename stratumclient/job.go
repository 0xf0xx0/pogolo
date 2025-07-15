package stratumclient

import (
	"encoding/hex"
	"fmt"
	"pogolo/config"
	"slices"
	"strconv"
	"time"

	"github.com/btcsuite/btcd/blockchain"
	"github.com/btcsuite/btcd/btcjson"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
)

//////
/// public-pool my beloved

type JobTemplate struct {
	ID                 string
	Block              *btcutil.Block
	WitnessCommittment []byte
	MerkleBranch       []*chainhash.Hash
	CoinbaseValue      int64
	Height             int64
	Clear              bool
}

var (
	currJobID       = uint64(0)
	currBlockHeight = uint64(0)
)

func CreateJobTemplate(template *btcjson.GetBlockTemplateResult, client StratumClient) JobTemplate {
	clear := false

	if currBlockHeight != uint64(template.Height) {
		println("new block")
		clear = true
	}

	currTime := time.Now().Unix()
	if template.MinTime > currTime {
		currTime = template.MinTime
	}
	bits, _ := strconv.ParseUint(template.Bits, 16, 32)
	prevBlockHash, _ := chainhash.NewHashFromStr(template.PreviousHash)
	// networkDiff := calcNetworkDifficulty()
	txns := make([]*btcutil.Tx, len(template.Transactions)+1) /// add a slot for the coinbase
	for idx, templateTx := range template.Transactions {
		decoded, err:= hex.DecodeString(templateTx.Data)
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
	txns[0] = CreateEmptyCoinbase()

	merkleTree := BuildMerkleTree(txns, false)
	merkleBranches := BuildMerkleProof(merkleTree, txns[0].Hash())
	merkleBranches = slices.DeleteFunc(merkleBranches, func(h *chainhash.Hash) bool {
		return h == nil
	})
	merkleRoot := merkleBranches[len(merkleBranches)-1]
	merkleBranches = merkleBranches[:len(merkleBranches)-1]

	merkleBranch := merkleBranches[1:]
	witnessCommit := blockchain.CalcMerkleRoot(txns, true)

	msgTxns := make([]*wire.MsgTx, len(txns))
	for idx, tx := range txns {
		msgTxns[idx] = tx.MsgTx()
	}

	block := btcutil.NewBlock(&wire.MsgBlock{
		Header: wire.BlockHeader{
			Version:    template.Version,
			Bits:       uint32(bits),
			PrevBlock:  *prevBlockHash,
			Timestamp:  time.Unix(currTime, 0),
			MerkleRoot: *merkleRoot,
		},
		Transactions: msgTxns,
	})
	block.SetHeight(int32(template.Height))

	job := JobTemplate{
		ID:                 getNextJobID(),
		Block:              block,
		WitnessCommittment: witnessCommit[:],
		MerkleBranch:       merkleBranch,
		CoinbaseValue:      *template.CoinbaseValue,
		Height:             template.Height,
		Clear:              clear,
	}

	return job
}

func getNextJobID() string {
	currJobID++
	return fmt.Sprintf("%x", currJobID)
}

func BuildMerkleTree(txns []*btcutil.Tx, witness bool) []*chainhash.Hash {
	treeStore := blockchain.BuildMerkleTreeStore(txns, witness)
	return treeStore
}
func BuildMerkleProof(tree []*chainhash.Hash, leaf *chainhash.Hash) []*chainhash.Hash {
	index := slices.Index(tree, leaf)

	if index == -1 {
		return nil
	}

	n := len(tree)
	nodes := []*chainhash.Hash{}

	z := calcTreeWidth(n, 1)
	for ; z > 0; z-- {
		if treeNodeCount(z) == n {
			break
		}
	}
	if z == 0 {
		panic("shouldnt ever be reached")
	}

	height := 0
	i := 0
	for {
		if i >= n-1 {
			break
		}
		layerWidth := calcTreeWidth(z, height)
		height++

		odd := index%2 == 1
		if odd {
			index--
		}
		offset := i + index
		left := tree[offset]
		right := tree[offset+1]

		if i > 0 {
			if odd {
				nodes = append(nodes, left)
				nodes = append(nodes, nil)
			} else {
				nodes = append(nodes, nil)
				nodes = append(nodes, right)
			}
		} else {
			nodes = append(nodes, left)
			nodes = append(nodes, right)
		}

		index = (index / 2) | 0
		i += layerWidth
	}
	nodes = append(nodes, tree[n-1])
	return nodes
}
func calcTreeWidth(n, h int) int {
	return (n + (1 << h) - 1) >> h
}
func treeNodeCount(leafCount int) int {
	count := 1
	for i := leafCount; i > 1; i = (i + 1) >> 1 {
		count += i
	}
	return count
}
func CreateEmptyCoinbase() *btcutil.Tx {
	/// use v2?
	coinbaseTxMsg := wire.NewMsgTx(wire.TxVersion)
	coinbaseTxMsg.AddTxIn(&wire.TxIn{
		PreviousOutPoint: *wire.NewOutPoint(&chainhash.Hash{}, wire.MaxTxInSequenceNum),
		Sequence:         wire.MaxTxInSequenceNum,
		Witness:          wire.TxWitness{},
	})
	return btcutil.NewTx(coinbaseTxMsg)
}

// thank you btcd devs for doin all this boilerplate work
func CreateCoinbaseTx(addr btcutil.Address, nextBlockHeight int32, extranonce2 int64, params *chaincfg.Params) *btcutil.Tx {
	pkScript, err := txscript.PayToAddrScript(addr)
	if err != nil {
		panic(err)
	}
	coinbaseScript, err := txscript.NewScriptBuilder().
		AddInt64(int64(nextBlockHeight)).
		AddInt64(extranonce2).
		AddData([]byte(config.COINBASE_TAG)).
		Script()
	if err != nil {
		panic(err)
	}
	coinbaseTxMsg := wire.NewMsgTx(wire.TxVersion)
	coinbaseTxMsg.AddTxIn(&wire.TxIn{
		PreviousOutPoint: *wire.NewOutPoint(&chainhash.Hash{}, wire.MaxTxInSequenceNum),
		SignatureScript:  coinbaseScript,
		Sequence:         wire.MaxTxInSequenceNum,
	})
	coinbaseTxMsg.AddTxOut(&wire.TxOut{
		Value:    blockchain.CalcBlockSubsidy(nextBlockHeight, params),
		PkScript: pkScript,
	})
	return btcutil.NewTx(coinbaseTxMsg)
}
