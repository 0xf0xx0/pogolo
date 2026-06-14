package main

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strconv"
	"testing"
	"time"

	"git.0xf0xx0.eth.limo/0xf0xx0/pogolo/constants"

	"git.0xf0xx0.eth.limo/0xf0xx0/stratum"
)

func TestMain(t *testing.T) {
	clients.Init()
	jobid, _ := strconv.Atoi(notifyParams.JobID)
	/// CreateJobTemplate increments the template id, and mock job could be at any number
	currTemplateID = uint64(jobid) - 1
	currTemplate, _ = CreateJobTemplate(MOCK_BLOCK_TEMPLATE)
	disableLogs = true
}

/// stratum chatter

func TestConfigure(t *testing.T) {
	lpipe, client, _ := initClient()
	params := configureParams
	req := configureReq
	res := sendReqAndWaitForRes(t, req, lpipe)
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
	validateRes(req, res, t)
	resp := stratum.BooleanResult{}
	resp.FromResponse(&res)
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
	r := stratum.MiningSubscribeResult{}
	err := r.FromResponse(&res)
	if err != nil {
		t.Fatal(err.Error())
	}
	if r.Subscriptions[0].Method != stratum.MethodMiningNotify {
		t.Errorf("subscription method mismatch: expected %q, got %q", stratum.MethodMiningNotify, r.Subscriptions[0].Method)
	}
	if r.Extranonce1 != client.ID {
		t.Errorf("extranonce1 mismatch: expected %q, got %q", client.ID, r.Extranonce1)
	}
	if r.Extranonce2Size != constants.EXTRANONCE_SIZE {
		t.Errorf("extranonce2 size mismatch: expected %d, got %d",
			constants.EXTRANONCE_SIZE, r.Extranonce2Size)
	}
	validateRes(req, res, t)
}

func TestSuggestDifficulty(t *testing.T) {
	lpipe, client, _ := initClient()
	res := sendReqAndWaitForRes(t, suggestDifficultyReq, lpipe)
	validateRes(suggestDifficultyReq, res, t)
	if client.SuggestedDifficulty != suggestDiffParams.Difficulty {
		t.Error("failed to store suggested diff")
	}
}

func TestUnimplementedMethod(t *testing.T) {
	lpipe, _, _ := initClient()
	res := sendReqAndWaitForRes(t, stratum.NewRequest(29, stratum.MethodClientGetVersion, []any{}), lpipe)
	if res.Error.Code != constants.ERROR_UNK_METHOD.Code {
		t.Fatalf("expected code %d, got code %d",
			constants.ERROR_UNK_METHOD.Code, res.Error.Code,
		)
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

	/// set diff
	readPipe(t, lpipe)
	/// notify
	readPipe(t, lpipe)

	/// WHYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYY
	time.Sleep(time.Millisecond)

	if client.CurrentJob.PrevBlockHash == nil {
		t.Fatal("job wasnt created, did init fail?")
	}
	client.Stop()
}

/*
FIXME: get updated job+share
* im lazy

	func TestSubmit(t *testing.T) {
		lpipe := ezInitClient(t, true)

		res := sendReqAndWaitForRes(t, submitReq, lpipe)
		if res.Error != nil {
			t.Fatalf("share submission failed! code: %s", res.Error)
		}
	}
*/
func TestSubmitDiffTooLow(t *testing.T) {
	lpipe := ezInitClient(t, false)

	res := sendReqAndWaitForRes(t, submitReq, lpipe)
	if res.Error == nil {
		t.Fatal("share submission succeeded??")
	}
}

func TestSubmitUnkJob(t *testing.T) {
	lpipe := ezInitClient(t, false)

	share := submitParams
	share.JobID = "fffffff"
	req := share.ToRequest(9)
	res := sendReqAndWaitForRes(t, req, lpipe)
	if res.Error == nil {
		t.Fatal("share submission succeeded??")
	}
	if res.Error != nil && res.Error.Code != constants.ERROR_STALE.Code {
		t.Fatalf("share submission failed, but wrong error: %s", res.Error)
	}
}
func TestSubmitBeforeSub(t *testing.T) {
	lpipe, _, _ := initClient()

	share := submitParams
	share.JobID = ""
	req := share.ToRequest(9)
	res := sendReqAndWaitForRes(t, req, lpipe)
	if res.Error == nil {
		t.Fatal("share submission succeeded??")
	}
	if res.Error != nil && res.Error.Code != constants.ERROR_NOT_SUBBED.Code {
		t.Fatalf("share submission failed, but wrong error: %s", res.Error)
	}
}

// kinda pointless but eh
func BenchmarkSubmit(b *testing.B) {
	lpipe := ezInitClient(b, true)

	for b.Loop() {
		res := sendReqAndWaitForRes(b, submitReq, lpipe)
		if res.Error != nil {
			b.Fatalf("share submission failed! code: %s", res.Error)
		}
	}
}

// util

func sendReqAndWaitForRes(t testing.TB, r stratum.Message, lpipe net.Conn) stratum.Response {
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

func readPipe(t testing.TB, lpipe net.Conn) stratum.Response {
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
func validateRes(req *stratum.Request, res stratum.Response, t *testing.T) {
	if req.MessageID != res.MessageID {
		t.Errorf("Message ID mismatch: expected %d, got %d", req.MessageID, res.MessageID)
	}
	if res.Error != nil {
		t.Errorf("Error in response: %s", res.Error.Message)
	}
}
func initClient() (net.Conn, *StratumClient, chan blockSubmission) {
	submissionChan := make(chan blockSubmission, 8)
	clientPipe, poolPipe := net.Pipe()
	client := CreateClient(poolPipe, submissionChan)
	client.ID, _ = stratum.DecodeID(MOCK_EXTRANONCE)
	go client.Run(context.Background())
	/// discard submissions
	go func() {
		for {
			<-submissionChan
		}
	}()
	return clientPipe, &client, submissionChan
}

// flip suggDiff to true to drop the diff to 0.16
func ezInitClient(t testing.TB, suggDiff bool) net.Conn {
	lpipe, c, _ := initClient()
	sendReqAndWaitForRes(t, authorizeReq, lpipe)
	sendReqAndWaitForRes(t, configureReq, lpipe)
	if suggDiff {
		sendReqAndWaitForRes(t, suggestDifficultyReq, lpipe)
	}
	sendReqAndWaitForRes(t, subscribeReq, lpipe)

	/// set diff
	readPipe(t, lpipe)
	/// notify
	readPipe(t, lpipe)

	// i think this helps with race conditions idk
	c.currentJobMutex.Lock()
	defer c.currentJobMutex.Unlock()
	c.CurrentJob.MinTime = 0
	c.CurrentJob.MaxTime = 0
	return lpipe
}
