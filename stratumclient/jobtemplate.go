package stratumclient

import (
	"encoding/hex"
	"encoding/json"
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
	"github.com/btcsuite/btcd/mining"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	stratum "github.com/kbnchk/go-Stratum"
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
	CoinbaseTx   *btcutil.Tx
	Notification stratum.Notification
	NotifyParams stratum.NotifyParams
}

var (
	currTemplateID  = uint64(0)
	currBlockHeight = uint64(0)
)

func CreateJobTemplate(template *btcjson.GetBlockTemplateResult) *JobTemplate {
	clear := false

	m, _ := json.Marshal(template)
	println("template", string(m))
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

	merkleTree := blockchain.BuildMerkleTreeStore(txns, false)
	merkleBranches := BuildMerkleProof(merkleTree, txns[0].Hash())
	merkleBranches = slices.DeleteFunc(merkleBranches, func(h *chainhash.Hash) bool {
		return h == nil
	})

	merkleRoot := merkleBranches[len(merkleBranches)-1]
	merkleBranches = merkleBranches[:len(merkleBranches)-1]

	merkleBranch := []*chainhash.Hash{}
	if len(merkleBranches) > 1 {
		merkleBranch = merkleBranches[1:]
	}
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
	println("weh:", block.Height())
	block.SetHeight(int32(template.Height))
	println("weh:", block.Height())

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

func getNextTemplateID() string {
	currTemplateID++
	return fmt.Sprintf("%x", currTemplateID)
}
func merkleRootFromBranches(branches []*chainhash.Hash) *chainhash.Hash {
	root := branches[0]

	for _, h := range branches[1:] {
		newroot := chainhash.DoubleHashH(append(root[:], h[:]...))
		root = &newroot
	}
	return root
}
func BuildMerkleProof(tree []*chainhash.Hash, leaf *chainhash.Hash) []*chainhash.Hash {
	index := slices.Index(tree, leaf)

	if index == -1 {
		return nil
	}

	n := len(tree)
	nodes := []*chainhash.Hash{}

	z := calcTreeWidth(n, 1)
	for z > 0 {
		if treeNodeCount(z) == n {
			break
		}
		z--
	}
	if z == 0 {
		panic("shouldnt ever be reached")
	}

	height := 0
	i := 0
	for i < n-1 {
		layerWidth := calcTreeWidth(z, height)
		height++

		odd := index%2 == 1
		if odd {
			index--
		}
		offset := i + index
		left := tree[offset]
		var right *chainhash.Hash
		if index == layerWidth-1 {
			right = left
		} else {
			right = tree[offset+1]
		}

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
	coinbaseTxMsg := wire.NewMsgTx(wire.TxVersion)
	emptyWitness := [blockchain.CoinbaseWitnessDataLen]byte{}
	coinbaseTxMsg.AddTxIn(&wire.TxIn{
		PreviousOutPoint: *wire.NewOutPoint(&chainhash.Hash{}, wire.MaxTxInSequenceNum),
		Sequence:         wire.MaxTxInSequenceNum,
		Witness:          wire.TxWitness{emptyWitness[:]},
	})

	return btcutil.NewTx(coinbaseTxMsg)
}

// thank you btcd devs for doin all this boilerplate work
// TODO: reuse empty coinbase
func CreateCoinbaseTx(addr btcutil.Address, template JobTemplate, params *chaincfg.Params) *btcutil.Tx {
	pkScript, err := txscript.PayToAddrScript(addr)
	if err != nil {
		panic(err)
	}
	height := template.Block.Height()
	println("height:", height)
	padding := [7]byte{} /// 8 bytes of padding, for extranonces
	/// adding data pushes an extra byte for the opcode
	coinbaseScript := txscript.NewScriptBuilder().
		/// bip-34
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
			AddInt64(int64(height)).
			AddData(padding[:])
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

	tx := btcutil.NewTx(coinbaseTxMsg)
	tx.SetIndex(0)
	/// AND I AM ITS SOLE WITNESS
	commitment := mining.AddWitnessCommitment(tx, template.Block.Transactions())
	println("commitment:", hex.EncodeToString(commitment))
	return tx
}

// like public-pools copyAndUpdateBlock, but without the copy
func (template *JobTemplate) UpdateBlock(client *StratumClient, share stratum.Share, notif stratum.NotifyParams) *wire.MsgBlock {
	msgBlock := template.Block.MsgBlock()

	coinbase := hex.EncodeToString(notif.CoinbasePart1) + client.ID.String() +
		hex.EncodeToString(share.ExtraNonce2) + hex.EncodeToString(notif.CoinbasePart2)
	decodedCoinbase, _ := hex.DecodeString(coinbase)
	tx, err := btcutil.NewTxFromBytes(decodedCoinbase)
	if err != nil {
		panic(err)
	}

	msgBlock.Transactions[0] = tx.MsgTx()

	msgBlock.Header.Nonce = share.Nonce

	if share.VersionMask != nil {
		msgBlock.Header.Version = msgBlock.Header.Version + int32(*share.VersionMask)
	}

	branches := append([]*chainhash.Hash{tx.Hash()}, template.MerkleBranch...)
	msgBlock.Header.MerkleRoot = *merkleRootFromBranches(branches)
	msgBlock.Header.Timestamp = time.Unix(int64(share.Time), 0)

	return msgBlock
}
