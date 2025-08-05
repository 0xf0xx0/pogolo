package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// api server

const API_VER = "v1"
const API_PFX = "/api"

type highScore struct {
	UpdatedAt               string  `json:"updatedAt"` // yyyy-mm-dd hh:mm:ss
	BestDifficulty          float64 `json:"bestDifficulty"`
	BestDifficultyUserAgent string  `json:"bestDifficultyUserAgent"`
}
type getInfoRes struct {
	Uptime     uint64      `json:"uptime"`
	UserAgents []string    `json:"userAgents"`
	BlockData  []string    `json:"blockData"`
	HighScores []highScore `json:"highScores"`
	// FoundBlocks []string `json:"foundBlocks"`
}
type getPoolRes struct {
	TotalHashrate uint64   `json:"totalHashRate"`
	TotalMiners   uint64   `json:"totalMiners"`
	BlockHeight   uint64   `json:"blockHeight"`
	BlocksFound   []string `json:"blocksFound"`
	Fee           uint64   `json:"fee"`
}

func initAPI() {
	http.HandleFunc(fmt.Sprintf("GET %s%s", API_PFX, "/info"), getInfo)
	/// im not doin the chart either
	http.HandleFunc(fmt.Sprintf("GET %s%s", API_PFX, "/pool"), getPool)
	http.HandleFunc(fmt.Sprintf("GET %s%s", API_PFX, "/network"), getNetwork)

	http.HandleFunc("GET /", func(res http.ResponseWriter, req *http.Request) {
		writeError(http.StatusNotFound, res)
	})
	http.HandleFunc("/", func(res http.ResponseWriter, req *http.Request) {
		writeError(http.StatusMethodNotAllowed, res)
	})
}

func getInfo(res http.ResponseWriter, req *http.Request) {
	marshalAndWrite(res, getInfoRes{
		Uptime:     uint64(time.Now().Sub(serverStartTime).Seconds()),
		UserAgents: getSetOfUAs(),
		BlockData:  []string{}, /// not doin this
		HighScores: getHighScores(),
	})
}
func getPool(res http.ResponseWriter, req *http.Request) {
	marshalAndWrite(res, getPoolRes{
		TotalHashrate: 0, /// TODO: fix when hashrate calc is made more accurate
		TotalMiners:   uint64(len(clients)),
		BlockHeight:   uint64(currTemplate.Height),
		BlocksFound:   []string{}, /// nor this
	})
}
func getNetwork(res http.ResponseWriter, req *http.Request) {
	info, err := backend.GetMiningInfo()
	if err != nil {
		logError("error in getNetwork: %s", err)
		writeError(http.StatusInternalServerError, res)
		return
	}
	marshalAndWrite(res, info)
}
func writeError(code int, res http.ResponseWriter) error {
	res.WriteHeader(code)
	if code == http.StatusNotFound {
		_, err := res.Write([]byte(`{"error":"nothing to see here, move along $citizen"}`))
		return err
	}
	return nil
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

// for such a simple language go lacks a lot of basic things...
func getSetOfUAs() []string {
	uaset := make(map[string]struct{})
	for _, client := range clients {
		uaset[client.UserAgent] = struct{}{}
	}
	uas := make([]string, 0, len(uaset))
	for ua := range uaset {
		uas = append(uas, ua)
	}
	return uas
}
func getHighScores() []highScore {
	scores := make([]highScore, 0, 5)
	for _, client := range clients {
		scores = append(scores, highScore{
			UpdatedAt:               "", /// i dont wanna, so i wont
			BestDifficulty:          client.BestDiff(),
			BestDifficultyUserAgent: client.Name(), /// lets use the name :3
		})
	}
	return scores
}
