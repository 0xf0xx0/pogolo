package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// api server

const API_PFX = "/api"
const API_VER = 1
const METRICS_NAMESPACE = "pogolo"

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
	UserAgent       string `json:"userAgent"`
	Extranonce1     string `json:"extranonce1"`
	ProtocolVersion uint8  `json:"protocolVersion"`
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
	TotalWorkers   prometheus.Gauge
	TotalHashrate  prometheus.Gauge
	BestDiff       prometheus.Gauge
	Uptime         prometheus.Gauge
	TemplateHeight prometheus.Gauge
	BlocksFound    prometheus.Gauge
}

func initAPI() {
	pfx := fmt.Sprintf("GET %s/v%d", API_PFX, API_VER)
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(collectors.WithGoCollectorRuntimeMetrics(
			collectors.MetricsGC,
			collectors.MetricsMemory,
			collectors.MetricsScheduler,
		)),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{Namespace: "pogolo"}),
	)
	metrics := &pogoloMetrics{
		TotalWorkers: promauto.With(reg).NewGauge(prometheus.GaugeOpts{
			Namespace: METRICS_NAMESPACE,
			Name:      "total_workers",
			Help:      "Number of gophers connected to the pool",
		}),
		TotalHashrate: promauto.With(reg).NewGauge(prometheus.GaugeOpts{
			Namespace: METRICS_NAMESPACE,
			Name:      "total_hashrate",
			Help:      "Total hashrate of all gophers connected to the pool",
		}),
		BestDiff: promauto.With(reg).NewGauge(prometheus.GaugeOpts{
			Namespace: METRICS_NAMESPACE,
			Name:      "best_diff",
			Help:      "Best difficulty achieved by a gopher",
		}),
		Uptime: promauto.With(reg).NewGauge(prometheus.GaugeOpts{
			Namespace: METRICS_NAMESPACE,
			Name:      "uptime",
			Help:      "Total uptime of the pool",
		}),
		TemplateHeight: promauto.With(reg).NewGauge(prometheus.GaugeOpts{
			Namespace: METRICS_NAMESPACE,
			Name:      "template_height",
			Help:      "Current template height",
		}),
		BlocksFound: promauto.With(reg).NewGauge(prometheus.GaugeOpts{
			Namespace: METRICS_NAMESPACE,
			Name:      "blocks_found",
			Help:      "Total number of blocks found",
		}),
	}
	// TODO: figure out how to update metrics without creating races and/or bogging down everything else

	http.HandleFunc(pfx+"/info", getInfo)
	http.HandleFunc(pfx+"/gopher/{idOrNickname}", getWorkerInfo)
	promHandler := promhttp.HandlerFor(reg, promhttp.HandlerOpts{})

	http.HandleFunc("GET /metrics", func(res http.ResponseWriter, req *http.Request) {
		allClients := clients.AllStats()
		allClientsLen := clients.Len()
		hashrateSum := float64(0)
		bestDiff := float64(0)
		for _, stats := range allClients {
			bestDiff = max(bestDiff, stats.bestDiff)
			hashrateSum += stats.HashrateH()
		}

		metrics.Uptime.Set(time.Since(serverStartTime).Seconds())
		metrics.TotalWorkers.Set(float64(allClientsLen))
		metrics.TotalHashrate.Set(hashrateSum)
		metrics.BestDiff.Set(bestDiff)
		metrics.BlocksFound.Set(float64(len(foundBlocks)))
		promHandler.ServeHTTP(res, req)
	})

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
			UserAgent:       client.UserAgent,
			Extranonce1:     client.ID.String(),
			ProtocolVersion: client.protocol,
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
