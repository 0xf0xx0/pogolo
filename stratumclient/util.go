package stratumclient

import (
	"bytes"
	"encoding/hex"
	"math"
	"math/big"

	"github.com/btcsuite/btcd/blockchain"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"
	stratum "github.com/kbnchk/go-Stratum"
)

func DecodeStratumMessage(msg []byte) (*stratum.Request, error) {
	var m stratum.Request
	if err := m.Unmarshal(msg); err != nil {
		return nil, err
	}
	return &m, nil
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
func SerializeBlock(blk *btcutil.Block) ([]byte, error) {
	buf := bytes.NewBuffer([]byte{})
	if err := blk.MsgBlock().Serialize(buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// port of public-pools calculateDifficulty
func CalcDifficulty(header wire.BlockHeader) (float64, big.Accuracy) {
	hashResult := header.BlockHash()
	s64 := blockchain.HashToBig(&hashResult)
	trueDiff1 := big.Int{}
	trueDiff1.SetString("26959535291011309493156476344723991336010898738574164086137773096960", 10)
	return new(big.Float).Quo(new(big.Float).SetInt(&trueDiff1), new(big.Float).SetInt(s64)).Float64()
}

// port of public-pools calculateNetworkDifficulty
func CalcNetworkDifficulty(nBits uint32) float64 {
	mantissa := float64(nBits & 0x007fffff)
	exponent := float64((nBits >> 24) & 0xff)
	target := mantissa * math.Pow(256, float64(exponent-3))
	maxTarget := math.Pow(2, 208) * 65535
	difficulty := maxTarget / target
	return difficulty
}

func DeepCopyTemplate(t *JobTemplate) *JobTemplate {
	newtemplate := JobTemplate{}
	newtemplate.ID = t.ID
	newtemplate.Block = *btcutil.NewBlock(t.Block.MsgBlock().Copy())
	newtemplate.Block.SetHeight(t.Block.Height())
	newtemplate.WitnessCommittment = make([]byte, len(t.WitnessCommittment))
	copy(newtemplate.WitnessCommittment[:], t.WitnessCommittment[:])
	newtemplate.MerkleBranch = make([]*chainhash.Hash, len(t.MerkleBranch))
	for i, mb := range t.MerkleBranch {
		newtemplate.MerkleBranch[i] = &chainhash.Hash{}
		copy(newtemplate.MerkleBranch[i][:], mb[:])
	}
	if t.MerkleRoot != nil {
		newtemplate.MerkleRoot = &chainhash.Hash{}
		copy(newtemplate.MerkleRoot[:], t.MerkleRoot[:])
	}
	newtemplate.NetworkDiff = t.NetworkDiff
	/// why isnt this copyable???
	newtemplate.Bits, _ = hex.DecodeString(hex.EncodeToString(t.Bits))

	newtemplate.Subsidy = t.Subsidy
	newtemplate.Height = t.Height
	newtemplate.Clear = t.Clear
	return &newtemplate
}
