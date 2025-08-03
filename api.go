package main

import (
	"encoding/json"
	"net/http"
	"time"
)

// api server

const API_VER = "v1"
const API_PFX = "/api/" + API_VER

func initAPI() {
	http.HandleFunc(API_PFX+"/info", getInfo)
}

type getInfoRes struct {
	Uptime      uint64   `json:"uptime"`
	UserAgents  []string `json:"userAgents"`
	// FoundBlocks []string `json:"foundBlocks"` // block hashes
}

func getInfo(res http.ResponseWriter, req *http.Request) {
	uas := make([]string, 0, len(clients))
	for _,client := range clients {
		uas = append(uas, client.UserAgent)
	}
	marshalAndWrite(res, getInfoRes{
		Uptime:     uint64(time.Now().Sub(serverStartTime).Seconds()),
		UserAgents: uas,
	})
}
func getPool(res http.ResponseWriter, req *http.Request) {

}
func marshalAndWrite(res http.ResponseWriter, v any) error {
	x, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = res.Write(x)
	return err
}
