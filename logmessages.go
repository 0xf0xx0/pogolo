package main

// done to avoid fmt, printf is slow

import (
	"encoding/hex"
	"strconv"
	"strings"

	"git.0xf0xx0.eth.limo/0xf0xx0/stratum"
)

type shareAcceptLog struct {
	shareDiff  float64
	targetDiff float64
	bestDiff   float64
	shareHash  string
	version    int32
	nonce      uint32
	id         stratum.ID
	en2        []byte
	hashrate   float64
	delta      float64
}

func (l shareAcceptLog) String() string {
	sb := strings.Builder{}
	sb.Grow(341 + int(conf.ExtraNonce2Size))
	sb.WriteString("diff {blue}")
	sb.WriteString(formatDifficulty(l.shareDiff))
	sb.WriteString("{/blue} of {blue}")
	sb.WriteString(formatDifficulty(l.targetDiff))
	sb.WriteString("{/blue} (best: {bluebright}")
	sb.WriteString(formatDifficulty(l.bestDiff))
	sb.WriteString("{/bluebright})\n{blackbright}")
	sb.WriteString(l.shareHash)
	sb.WriteString("\n\tversion: {blue}")
	sb.WriteString(strconv.FormatInt(int64(l.version), 16))
	sb.WriteString("{/blue} nonce: {green}")
	sb.WriteString(strconv.FormatInt(int64(l.nonce), 16))
	sb.WriteString("{/green} extranonce: {blue}")
	sb.WriteString(l.id.String())
	sb.WriteString("{green}")
	sb.WriteString(hex.EncodeToString(l.en2))
	sb.WriteString("{/blue}\n\t")
	sb.WriteString(formatHashrate(l.hashrate))
	sb.WriteString("{/green}, avg submit delta: {blue}")
	sb.WriteString(strconv.FormatFloat(l.delta, 'f', 3, 64))
	sb.WriteString("{/blue}")

	return sb.String()
}

type diffTooLowLog struct {
	shareDiff  float64
	targetDiff float64
}

func (l diffTooLowLog) String() string {
	sb := strings.Builder{}
	sb.Grow(40)
	sb.WriteString("share rejected: diff too low (")
	sb.WriteString(formatDifficulty(l.shareDiff))
	sb.WriteString("/")
	sb.WriteString(formatDifficulty(l.targetDiff))
	sb.WriteString(")")
	return sb.String()
}
