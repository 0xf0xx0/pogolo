package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// api server

const API_PFX = "/api"
const API_VER = "/v1"

type workerInfo struct {
	Uptime           uint64  `json:"uptime"`
	Hashrate         float64 `json:"hashrate"` // TODO: currently mh/s, use h/s?
	AcceptedShares   uint64  `json:"sharesAccepted"`
	RejectedShares   uint64  `json:"sharesRejected"`
	TargetDifficulty float64 `json:"targetDifficulty"`
	BestDifficulty   float64 `json:"bestDifficulty"`
	UserAgent        string  `json:"userAgent"`
	Extranonce1      string  `json:"extranonce1"`
}
type highScore struct {
	UpdatedAt string  `json:"updatedAt"` // yyyy-mm-dd hh:mm:ss
	BestDiff  float64 `json:"bestDifficulty"`
	UserAgent string  `json:"bestDifficultyUserAgent"`
}
type getInfoRes struct {
	Uptime     uint64             `json:"uptime"`
	UserAgents []getInfoUserAgent `json:"userAgents"`
	HighScores []highScore        `json:"highScores"`
	Tag        string             `json:"tag"`
}
type getInfoUserAgent struct {
	UserAgent      string  `json:"userAgent"`
	Uptime         uint64  `json:"uptime"`
	BestDifficulty float64 `json:"bestDifficulty"`
	TotalHashrate  float64 `json:"totalHashRate"`
}
type getPoolRes struct {
	TotalHashrate float64 `json:"totalHashRate"`
	TotalMiners   uint64  `json:"totalMiners"`
	BlockHeight   uint64  `json:"blockHeight"`
	Fee           uint64  `json:"fee"`
}

func initAPI() {
	/// public-pool-ui compat endpoints
	ppCompatPfx := "GET " + API_PFX
	http.HandleFunc(ppCompatPfx+"/info", getInfo)
	/// im not doin the chart
	http.HandleFunc(ppCompatPfx+"/pool", getPool)
	http.HandleFunc(ppCompatPfx+"/network", getNetwork)

	/// ok, now our api
	http.HandleFunc("GET "+API_PFX+API_VER+"/worker/{extranonce1}", getWorkerInfo)

	/// default handlers
	http.HandleFunc("GET /", func(res http.ResponseWriter, _ *http.Request) {
		writeError(http.StatusNotFound, res)
	})
	http.HandleFunc("/", func(res http.ResponseWriter, _ *http.Request) {
		writeError(http.StatusMethodNotAllowed, res)
	})
}

func getInfo(res http.ResponseWriter, req *http.Request) {
	workerStats := make([]getInfoUserAgent, 0, len(clients))
	for _, client := range clients {
		workerStats = append(workerStats, getInfoUserAgent{
			UserAgent:      client.Name(),
			BestDifficulty: client.stats.bestDiff,
			TotalHashrate:  client.stats.HashrateH(), /// public-pool-ui expects h/s
		})
	}
	marshalAndWrite(res, getInfoRes{
		Uptime:     uint64(time.Since(serverStartTime).Milliseconds()),
		UserAgents: workerStats,
		HighScores: getHighScores(),
		Tag:        conf.Pogolo.Tag,
	})
}
func getPool(res http.ResponseWriter, _ *http.Request) {
	hashrateSum := float64(0)
	for _, client := range clients {
		hashrateSum += client.stats.HashrateMH()
	}
	marshalAndWrite(res, getPoolRes{
		TotalHashrate: hashrateSum,
		TotalMiners:   uint64(len(clients)),
		BlockHeight:   uint64(currTemplate.Height),
	})
}
func getNetwork(res http.ResponseWriter, req *http.Request) {
	info, err := backend.GetMiningInfo()
	if err != nil {
		logError(fmt.Sprintf("error in getNetwork: %s", err))
		writeError(http.StatusInternalServerError, res)
		return
	}
	marshalAndWrite(res, info)
}
func getWorkerInfo(res http.ResponseWriter, req *http.Request) {
	name := req.PathValue("extranonce1")
	client := findClientFromName(name)
	if client == nil {
		logError(fmt.Sprintf("failed to find client %s", name))
		writeError(http.StatusBadRequest, res)
		return
	}

	info := workerInfo{
		UserAgent:        client.UserAgent,
		Uptime:           client.stats.Uptime(),
		Extranonce1:      client.ID.String(),
		Hashrate:         client.stats.HashrateMH(),
		TargetDifficulty: client.TargetDiff,
		BestDifficulty:   client.stats.bestDiff,
		AcceptedShares:   client.stats.sharesAccepted,
		RejectedShares:   client.stats.sharesRejected,
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
	res.Header().Set("Server", NAME+"/"+VERSION)
	_, err = res.Write(x)
	return err
}

func getHighScores() []highScore {
	scores := make([]highScore, 0, 5)
	for _, client := range clients {
		scores = append(scores, highScore{
			UpdatedAt: "", /// i dont wanna, so i wont
			BestDiff:  client.stats.bestDiff,
			UserAgent: client.Name(), /// lets use the name :3
		})
	}
	return scores
}
func findClientFromName(name string) *StratumClient {
	for _, client := range clients {
		if client.Name() == name {
			return client
		}
	}
	return nil
}
