package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// api server

const API_PFX = "/api"
const API_VER = 1

type detailedWorkerInfo struct {
	Nickname         string  `json:"nickname"`
	UserAgent        string  `json:"userAgent"`
	ExtraNonce1      string  `json:"extranonce1"`
	Uptime           uint64  `json:"uptime"`
	AcceptedShares   uint64  `json:"sharesAccepted"`
	RejectedShares   uint64  `json:"sharesRejected"`
	Hashrate         float64 `json:"hashrate"`
	TargetDifficulty float64 `json:"targetDifficulty"`
	BestDifficulty   float64 `json:"bestDifficulty"`
}

// only the neccesary details
type miniWorkerInfo struct {
	UserAgent   string `json:"userAgent"`
	ExtraNonce1 string `json:"extranonce1"`
}
type getInfoRes struct {
	Uptime        uint64           `json:"uptime"`
	BlockHeight   uint64           `json:"blockHeight"`
	TotalWorkers  uint64           `json:"totalGophers"`
	TotalHashrate float64          `json:"totalHashrate"`
	BestDiff      float64          `json:"bestDifficulty"`
	Tag           string           `json:"tag"`
	Workers       []miniWorkerInfo `json:"gophers"`
}

func initAPI() {
	/// TODO: wip
	pfx := fmt.Sprintf("GET %s/v%d", API_PFX, API_VER)
	http.HandleFunc(pfx+"/info", getInfo)
	http.HandleFunc(pfx+"/gopher/{extranonce1}", getWorkerInfo)

	/// default handlers
	http.HandleFunc("GET /", func(res http.ResponseWriter, _ *http.Request) {
		writeError(res, http.StatusNotFound, "nothing to see here, move along $citizen")
	})
	http.HandleFunc("/", func(res http.ResponseWriter, _ *http.Request) {
		writeError(res, http.StatusMethodNotAllowed, "method not allowed")
	})
}

func getInfo(res http.ResponseWriter, req *http.Request) {
	allClients := clients.All()
	workerStats := make([]miniWorkerInfo, 0, len(allClients))
	hashrateSum := float64(0)
	bestDiff := float64(0)
	for _, client := range allClients {
		workerStats = append(workerStats, miniWorkerInfo{
			UserAgent:   client.UserAgent,
			ExtraNonce1: client.ID.String(),
		})
		bestDiff = max(bestDiff, client.stats.bestDiff)
		hashrateSum += client.stats.HashrateH()
	}
	marshalAndWrite(res, getInfoRes{
		Uptime:        uint64(time.Since(serverStartTime).Seconds()),
		Workers:       workerStats,
		Tag:           conf.Pogolo.Tag,
		TotalHashrate: hashrateSum,
		BestDiff:      bestDiff,
		TotalWorkers:  uint64(len(allClients)),
		BlockHeight:   uint64(currTemplate.Height),
	})
}

// Takes a name (id or worker name) and returns a snapshot of the matching client, if any
func getWorkerInfo(res http.ResponseWriter, req *http.Request) {
	name := req.PathValue("extranonce1")
	worker := getClientFromNameOrID(name)
	if worker == nil {
		logError(fmt.Sprintf("failed to find client %s", name))
		writeError(res, http.StatusBadRequest, "failed to find client")
		return
	}

	info := detailedWorkerInfo{
		Nickname:         worker.Nickname,
		UserAgent:        worker.UserAgent,
		ExtraNonce1:      worker.ID.String(),
		Hashrate:         worker.stats.HashrateH(),
		TargetDifficulty: worker.TargetDifficulty,
		BestDifficulty:   worker.stats.bestDiff,
		AcceptedShares:   worker.stats.sharesAccepted,
		RejectedShares:   worker.stats.sharesRejected,
		Uptime:           worker.stats.Uptime(),
	}
	marshalAndWrite(res, info)
}

func writeError(res http.ResponseWriter, code int, msg string) error {
	res.WriteHeader(code)
	if msg != "" {
		return writeResponse(res, []byte(fmt.Sprintf(`{"error":%q}`, msg)))
	}
	return nil
}
func marshalAndWrite(res http.ResponseWriter, v any) error {
	x, err := json.Marshal(v)
	if err != nil {
		writeError(res, http.StatusInternalServerError, "")
		return err
	}
	return writeResponse(res, x)
}

func writeResponse(res http.ResponseWriter, x []byte) error {
	res.Header().Set("Content-Type", "application/json")
	res.Header().Set("Server", NAME+"/"+VERSION)
	res.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	_, err := res.Write(x)
	return err
}

func getClientFromNameOrID(name string) *StratumClient {
	for _, client := range clients.All() {
		if client.Nickname == name || client.ID.String() == name {
			return client
		}
	}
	return nil
}
