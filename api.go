package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// api server

const API_PFX = "/api"
const API_VER = 1

type detailedWorkerInfo struct {
	Address         string  `json:"address"`
	Extranonce1     string  `json:"extranonce1"`
	Nickname        string  `json:"nickname"`
	UserAgent       string  `json:"userAgent"`
	AcceptedShares  uint64  `json:"sharesAccepted"`
	RejectedShares  uint64  `json:"sharesRejected"`
	Hashrate        float64 `json:"hashrate"`
	BestDiff        float64 `json:"bestDifficulty"`
	TargetDiff      float64 `json:"targetDifficulty"`
	Uptime          uint64  `json:"uptime"`
	AvgShareTime    float64 `json:"averageShareTime"`
	ProtocolVersion uint8   `json:"protocolVersion"`
}

// only the neccesary details
type miniWorkerInfo struct {
	UserAgent   string `json:"userAgent"`
	Extranonce1 string `json:"extranonce1"`
}
type getInfoRes struct {
	Uptime        uint64           `json:"uptime"`
	BlockHeight   uint64           `json:"blockHeight"`
	TotalWorkers  uint64           `json:"totalGophers"`
	TotalHashrate float64          `json:"totalHashrate"`
	BestDiff      float64          `json:"bestDifficulty"`
	Tag           string           `json:"tag"`
	Workers       []miniWorkerInfo `json:"gophers"`
	BlocksFound   []string         `json:"blocksFound"`
}

type pogoloMetrics struct {
	TotalWorkers  prometheus.Gauge
	TotalHashrate prometheus.Gauge
	BestDiff      prometheus.Counter
	Uptime        prometheus.Counter
	BlockHeight   prometheus.Counter
	BlocksFound   prometheus.Counter
}

func initAPI() {
	pfx := fmt.Sprintf("GET %s/v%d", API_PFX, API_VER)
	http.HandleFunc(pfx+"/info", getInfo)
	http.HandleFunc(pfx+"/gopher/{idOrNickname}", getWorkerInfo)

	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(collectors.WithGoCollectorRuntimeMetrics(
			collectors.MetricsGC,
			collectors.MetricsMemory,
			collectors.MetricsScheduler,
		)),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{Namespace: "pogolo"}),
	)
	http.Handle("GET /metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))

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
	workerStats := make([]miniWorkerInfo, len(allClients))
	hashrateSum := float64(0)
	bestDiff := float64(0)
	for idx, client := range allClients {
		workerStats[idx] = miniWorkerInfo{
			UserAgent:   client.UserAgent,
			Extranonce1: client.ID.String(),
		}
		bestDiff = max(bestDiff, client.stats.bestDiff)
		hashrateSum += client.stats.HashrateH()
	}
	currTemplateLock.RLock()
	defer currTemplateLock.RUnlock()
	if currTemplate == nil {
		/// skip block height and avoid nil pointer deref
		/// also skip found blocks cause if currTemplate is nil the backend node
		/// hasnt fully inited
		marshalAndWrite(res, getInfoRes{
			Uptime:        uint64(time.Since(serverStartTime).Seconds()),
			Workers:       workerStats,
			Tag:           conf.Tag,
			TotalHashrate: hashrateSum / 1e6,
			BestDiff:      bestDiff,
			TotalWorkers:  uint64(len(allClients)),
		})
		return
	}
	marshalAndWrite(res, getInfoRes{
		/// NOTE: compiling with -race mistakenly calls this a race condition
		Uptime:        uint64(time.Since(serverStartTime).Seconds()),
		Workers:       workerStats,
		Tag:           conf.Tag,
		TotalHashrate: hashrateSum / 1e6,
		BestDiff:      bestDiff,
		TotalWorkers:  uint64(len(allClients)),
		BlockHeight:   uint64(currTemplate.Height),
		BlocksFound:   foundBlocks,
	})
}

// Takes a name (id or worker name) and returns a snapshot of the matching client, if any
func getWorkerInfo(res http.ResponseWriter, req *http.Request) {
	name := req.PathValue("idOrNickname")
	worker := getClientFromNameOrID(name)
	if worker == nil {
		logError(fmt.Sprintf("failed to find client %s", name))
		writeError(res, http.StatusBadRequest, "failed to find client")
		return
	}

	info := detailedWorkerInfo{
		Address:         worker.User.EncodeAddress(),
		Nickname:        worker.Nickname,
		UserAgent:       worker.UserAgent,
		Extranonce1:     worker.ID.String(),
		Hashrate:        worker.stats.HashrateMH(),
		TargetDiff:      worker.TargetDifficulty,
		BestDiff:        worker.stats.bestDiff,
		AcceptedShares:  worker.stats.sharesAccepted,
		RejectedShares:  worker.stats.sharesRejected,
		Uptime:          worker.stats.Uptime(),
		AvgShareTime:    worker.stats.avgSubmissionDelta,
		ProtocolVersion: worker.protocol,
	}
	marshalAndWrite(res, info)
}

func writeError(res http.ResponseWriter, code int, msg string) error {
	res.WriteHeader(code)
	if msg != "" {
		return writeResponse(res, fmt.Appendf(nil, `{"error":%q}`, msg))
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
