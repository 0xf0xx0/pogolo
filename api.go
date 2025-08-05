package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// api server

const API_VER = "v1"
const API_PFX = "/api/" + API_VER

func initAPI() {
	http.HandleFunc("/*", func(req http.ResponseWriter, res *http.Request) {
		res.Write(bytes.NewBufferString("peepeepoopoo"))
	})
	http.HandleFunc(API_PFX+"/info", getInfo)
}

type highScore struct {
	UpdatedAt               string  `json:"updatedAt"` // yyyy-mm-dd hh:mm:ss
	BestDifficulty          float64 `json:"bestDifficulty"`
	BestDifficultyUserAgent float64 `json:"bestDifficultyUserAgent"`
}
type getInfoRes struct {
	Uptime     uint64      `json:"uptime"`
	UserAgents []string    `json:"userAgents"`
	BlockData  []string    `json:"blockData"`
	HighScores []highScore `json:"highScores"`
	// FoundBlocks []string `json:"foundBlocks"` // block hashes
}
type getPoolRes struct {
	TotalHashrate uint64   `json:"totalHashRate"`
	TotalMiners   uint64   `json:"totalMiners"`
	BlockHeight   uint64   `json:"blockHeight"`
	BlocksFound   []string `json:"blocksFound"`
	Fee           uint64   `json:"fee"`
}

func getInfo(res http.ResponseWriter, req *http.Request) {
	uas := make([]string, 0, len(clients))
	for _, client := range clients {
		uas = append(uas, client.UserAgent)
	}
	marshalAndWrite(res, getInfoRes{
		Uptime:     uint64(time.Now().Sub(serverStartTime).Seconds()),
		UserAgents: uas,
		BlockData:  []string{},
		HighScores: []highScore{},
	})
}
func getPool(res http.ResponseWriter, req *http.Request) {

}
func marshalAndWrite(res http.ResponseWriter, v any) error {
	x, err := json.Marshal(v)
	if err != nil {
		return err
	}
	res.Header().Set("Content-Type", "application/json")
	res.Header().Set("Server", fmt.Sprintf("%s/%s", NAME, VERSION))
	_, err = res.Write(x)
	return err
}
