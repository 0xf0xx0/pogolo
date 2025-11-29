package main

import (
	"bufio"
	"fmt"
	"net"
	"pogolo/constants"
	"testing"

	// main "pogolo"

	"github.com/0xf0xx0/stratum"
)

func TestMain(t *testing.T) {}

func TestConfigure(t *testing.T) {
	lpipe, client, _ := initClient()
	params := configureParams
	req := configureReq
	res := sendReqAndWaitForRes(t, req, lpipe)
	client.Stop()
	validateRes(req, res, t)
	t.Logf("ver rolling mask: %x, supported: %v", client.VersionRollingMask, params.Supported)
	if client.VersionRollingMask == 0 {
		t.Error("version rolling is wrong")
	}
}

func TestAuthorize(t *testing.T) {
	lpipe, client, _ := initClient()
	params := authorizeParams
	req := authorizeReq
	res := sendReqAndWaitForRes(t, req, lpipe)
	client.Stop()
	validateRes(req, res, t)
	resp := stratum.BooleanResult{}
	resp.Read(&res)
	if resp.Result == false {
		t.Error("result was false")
	}
	t.Logf("user: %q, worker: %q", client.User, client.Nickname)
	rebuiltUser := fmt.Sprintf("%s.%s", client.User, client.Nickname)
	if rebuiltUser != params.Username+"."+params.Worker {
		t.Errorf("username mismatch: expected %q, got %q", params.Username+"."+params.Worker, rebuiltUser)
	}
}

func TestSubscribe(t *testing.T) {
	lpipe, client, _ := initClient()

	req := subscribeReq
	res := sendReqAndWaitForRes(t, req, lpipe)
	client.Stop()
	r := stratum.SubscribeResult{}
	err := r.Read(&res)
	if err != nil {
		t.Fatal(err.Error())
	}
	if r.Subscriptions[0].Method != stratum.MiningNotify {
		t.Errorf("subscription method mismatch: expected %q, got %q", stratum.MiningNotify, r.Subscriptions[0].Method)
	}
	if r.ExtraNonce1 != client.ID {
		t.Errorf("extranonce1 mismatch: expected %q, got %q", client.ID, r.ExtraNonce1)
	}
	if r.ExtraNonce2Size != constants.EXTRANONCE_SIZE {
		t.Errorf("extranonce2 size mismatch: expected %d, got %d",
			constants.EXTRANONCE_SIZE, r.ExtraNonce2Size)
	}
	validateRes(req, res, t)
}

func TestSuggestDifficulty(t *testing.T) {
	lpipe, client, _ := initClient()
	res := sendReqAndWaitForRes(t, suggestDifficultyReq, lpipe)
	client.Stop()
	validateRes(suggestDifficultyReq, res, t)
	if client.SuggestedDifficulty != suggestDiffParams.Difficulty {
		t.Error("failed to store suggested diff")
	}
}

func TestInitSequence(t *testing.T) {
	lpipe, client, _ := initClient()

	res := sendReqAndWaitForRes(t, authorizeReq, lpipe)
	validateRes(authorizeReq, res, t)

	res = sendReqAndWaitForRes(t, configureReq, lpipe)
	validateRes(configureReq, res, t)

	res = sendReqAndWaitForRes(t, subscribeReq, lpipe)
	validateRes(subscribeReq, res, t)

	fmt.Printf("%+v\n", client)
}

// util

func sendReqAndWaitForRes(t *testing.T, r stratum.Request, lpipe net.Conn) stratum.Response {
	b, err := r.Marshal()
	if err != nil {
		t.Fatalf("error marshalling req: %s", err)
	}
	t.Logf("sending message: %s", b)
	//time.Sleep(time.Millisecond * 100) /// if needed
	_, err = lpipe.Write(b)
	if err != nil {
		t.Fatal(err.Error())
	}

	res := readPipe(t, lpipe)
	return res
}

func readPipe(t *testing.T, lpipe net.Conn) stratum.Response {
	reader := bufio.NewReader(lpipe)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Error(err.Error())
	}
	t.Log("response:", string(line))
	res := stratum.Response{}
	res.Unmarshal(line)
	return res
}
func validateRes(req stratum.Request, res stratum.Response, t *testing.T) {
	if req.MessageID != res.MessageID {
		t.Errorf("Message ID mismatch: expected %q, got %q", req.MessageID, res.MessageID)
	}
	if res.Error != nil {
		t.Errorf("Error in response: %s", res.Error.Message)
	}
}
func initClient() (net.Conn, *StratumClient, chan blockSubmission) {
	submissionChan := make(chan blockSubmission)
	lpipe, rpipe := net.Pipe()
	lpipe.LocalAddr()
	client := CreateClient(rpipe, submissionChan)
	client.ID, _ = stratum.DecodeID(MOCK_EXTRANONCE)
	go client.Run(true)
	go func() {
		/// client.Stop() will block until read, so read and discard
		<-client.MsgChannel()
	}()
	return lpipe, &client, submissionChan
}
