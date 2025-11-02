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
	Uptime           uint64  `json:"uptime"`
	Hashrate         float64 `json:"hashrate"`
	AcceptedShares   uint64  `json:"sharesAccepted"`
	RejectedShares   uint64  `json:"sharesRejected"`
	TargetDifficulty float64 `json:"targetDifficulty"`
	BestDifficulty   float64 `json:"bestDifficulty"`
	UserAgent        string  `json:"userAgent"`
	ExtraNonce1      string  `json:"extranonce1"`
}

// only the neccesary details
type workerInfo struct {
	UserAgent   string `json:"userAgent"`
	ExtraNonce1 string `json:"extranonce1"`
}
type getInfoRes struct {
	Uptime        uint64       `json:"uptime"`
	Tag           string       `json:"tag"`
	BlockHeight   uint64       `json:"blockHeight"`
	TotalHashrate float64      `json:"totalHashrate"`
	TotalWorkers  uint64       `json:"totalWorkers"`
	Workers       []workerInfo `json:"workers"` /// TODO: come up with a cool name, swarm? gophers?
}

func initAPI() {
	/// TODO: wip
	pfx := fmt.Sprintf("GET %s/v%d", API_PFX, API_VER)
	http.HandleFunc(pfx+"/", getInfo)
	http.HandleFunc(pfx+"/worker/{extranonce1}", getWorkerInfo)

	/// default handlers
	http.HandleFunc("GET /", func(res http.ResponseWriter, _ *http.Request) {
		writeError(http.StatusNotFound, res)
	})
	http.HandleFunc("/", func(res http.ResponseWriter, _ *http.Request) {
		writeError(http.StatusMethodNotAllowed, res)
	})
}

func getInfo(res http.ResponseWriter, req *http.Request) {
	workerStats := make([]workerInfo, 0, len(clients))
	hashrateSum := float64(0)
	for _, client := range clients {
		workerStats = append(workerStats, workerInfo{
			UserAgent:   client.UserAgent,
			ExtraNonce1: client.ID.String(),
		})
		hashrateSum += client.stats.HashrateH()
	}
	marshalAndWrite(res, getInfoRes{
		Uptime:        uint64(time.Since(serverStartTime).Milliseconds()),
		Workers:       workerStats,
		Tag:           conf.Pogolo.Tag,
		TotalHashrate: hashrateSum,
		TotalWorkers:  uint64(len(clients)),
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

	info := detailedWorkerInfo{
		UserAgent:        client.UserAgent,
		Uptime:           client.stats.Uptime(),
		ExtraNonce1:      client.ID.String(),
		Hashrate:         client.stats.HashrateH(),
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
	res.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	_, err = res.Write(x)
	return err
}

func findClientFromName(name string) *StratumClient {
	for _, client := range clients {
		if client.Name() == name {
			return client
		}
	}
	return nil
}
