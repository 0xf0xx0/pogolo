package stratumclient

import (
	"bytes"
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
	stratum "github.com/kbnchk/go-Stratum"
)

//////
/// public-pool my beloved

type JobTemplate struct {
	ID                 string
	Block              *btcutil.Block
	WitnessCommittment []byte
	MerkleBranch       []*chainhash.Hash
	MerkleRoot         *chainhash.Hash
	Bits               []byte
	Subsidy            int64
	Height             int64
	Clear              bool
}
type MiningJob struct {
	Template     *JobTemplate
	CoinbaseTx   *btcutil.Tx
	Notification stratum.Notification
}

var (
	currTemplateID  = uint64(0)
	currBlockHeight = uint64(0)
)

func CreateJobTemplate(template *btcjson.GetBlockTemplateResult) *JobTemplate {
	clear := false

	if currBlockHeight != uint64(template.Height) {
		println("new block")
		currBlockHeight = uint64(template.Height)
		clear = true
	}

	currTime := time.Now().Unix()
	if template.MinTime > currTime {
		currTime = template.MinTime
	}
	headerBits, _ := strconv.ParseUint(template.Bits, 16, 32)
	prevBlockHash, _ := chainhash.NewHashFromStr(template.PreviousHash)
	// networkDiff := calcNetworkDifficulty()
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
			Bits:       uint32(headerBits),
			PrevBlock:  *prevBlockHash,
			Timestamp:  time.Unix(currTime, 0),
			MerkleRoot: *merkleRoot,
		},
		Transactions: msgTxns,
	})
	block.SetHeight(int32(template.Height))

	bits, _ := hex.DecodeString(template.Bits)
	job := &JobTemplate{
		ID:                 getNextTemplateID(),
		Block:              block,
		WitnessCommittment: witnessCommit[:],
		MerkleBranch:       merkleBranch,
		MerkleRoot:         merkleRoot,
		Bits:               bits,
		Subsidy:            *template.CoinbaseValue,
		Height:             template.Height,
		Clear:              clear,
	}

	return job
}

func getNextTemplateID() string {
	currTemplateID++
	return fmt.Sprintf("%x", currTemplateID)
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

// placeholder tx
func CreateEmptyCoinbase() *btcutil.Tx {
	/// use v2?
	coinbaseTxMsg := wire.NewMsgTx(wire.TxVersion)
	coinbaseTxMsg.AddTxIn(&wire.TxIn{
		PreviousOutPoint: *wire.NewOutPoint(&chainhash.Hash{}, wire.MaxTxInSequenceNum),
		Sequence:         wire.MaxTxInSequenceNum,
		Witness:          wire.TxWitness{
			/// TODO: empty 32-byte witness
		},
	})

	return btcutil.NewTx(coinbaseTxMsg)
}
func SerializeTx(tx *wire.MsgTx, witness bool) ([]byte, error) {
	serializedTx := bytes.NewBuffer(make([]byte, 0, tx.SerializeSize()))

	if witness {
		if err := tx.Serialize(serializedTx); err != nil {
			return nil, err
		}
	} else {
		serializedTx = bytes.NewBuffer(make([]byte, 0, tx.SerializeSizeStripped()))
		if err := tx.SerializeNoWitness(serializedTx); err != nil {
			return nil, err
		}
	}
	return serializedTx.Bytes(), nil
}

// thank you btcd devs for doin all this boilerplate work
func CreateCoinbaseTx(addr btcutil.Address, template JobTemplate, params *chaincfg.Params) *btcutil.Tx {
	pkScript, err := txscript.PayToAddrScript(addr)
	if err != nil {
		panic(err)
	}
	height := template.Block.Height()
	padding := [15]byte{} /// 16 bytes of padding, for extranonce
	/// adding data pushes an extra byte for the opcode
	coinbaseScript := txscript.NewScriptBuilder().
		AddInt64(int64(height)).
		AddData([]byte(config.COINBASE_TAG)).
		AddData(padding[:])
	encodedCoinbaseScript, err := coinbaseScript.Script()
	if err != nil {
		panic(err)
	}
	if len(encodedCoinbaseScript) > blockchain.MaxCoinbaseScriptLen {
		println("pool tag too long, removing")
		coinbaseScript = coinbaseScript.Reset().
			AddInt64(int64(height))
		encodedCoinbaseScript, err = coinbaseScript.Script()
		if err != nil {
			panic(err)
		}
	}
	emptyWitness := [blockchain.CoinbaseWitnessDataLen]byte{}
	coinbaseTxMsg := wire.NewMsgTx(wire.TxVersion)
	coinbaseTxMsg.AddTxIn(&wire.TxIn{
		PreviousOutPoint: *wire.NewOutPoint(&chainhash.Hash{}, wire.MaxPrevOutIndex),
		SignatureScript:  encodedCoinbaseScript,
		Sequence:         wire.MaxTxInSequenceNum,
		Witness:          wire.TxWitness{emptyWitness[:]}, /// 32 bytes of nothin
	})
	coinbaseTxMsg.AddTxOut(&wire.TxOut{
		Value:    template.Subsidy,
		PkScript: pkScript,
	})
	/// there has to be a way to use txscript, right?
	magicBytes, _ := hex.DecodeString("6a24" + "aa21a9ed")
	witnessCommit := append(magicBytes, template.WitnessCommittment...)
	coinbaseTxMsg.AddTxOut(&wire.TxOut{
		Value:    0,
		PkScript: witnessCommit,
	})

	tx := btcutil.NewTx(coinbaseTxMsg)
	tx.SetIndex(0)
	return tx
}
