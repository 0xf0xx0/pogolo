package main

import (
	"bufio"
	"context"
	"encoding/hex"
	"fmt"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"git.0xf0xx0.eth.limo/0xf0xx0/pogolo/constants"
	"github.com/btcsuite/btcd/address/v2"
	"github.com/btcsuite/btcd/chaincfg/v2"

	"git.0xf0xx0.eth.limo/0xf0xx0/stratum"
)

func TestMain(t *testing.T) {
	clients.Init()
	jobid, _ := strconv.Atoi(notifyParams.JobID)
	/// CreateJobTemplate increments the template id, and mock job could be at any number
	currTemplateID = uint64(jobid) - 1
	currTemplate, _ = CreateJobTemplate(MOCK_BLOCK_TEMPLATE)
	backendChainParams = &chaincfg.RegressionNetParams
	b, _ := hex.DecodeString("8033d13ee81500afe03a9f48ed142b15724816dd9247c9cf55ae447a5b867449")
	defaultMiningAddr, _ = address.NewAddressTaproot(b, backendChainParams)
	disableLogs = true
}

/// stratum chatter

func TestSv1Configure(t *testing.T) {
	lpipe, client, _ := initClient()
	params := configureParams
	req := configureReq
	res := sendSv1ReqAndWaitForRes(t, req, lpipe)
	validateSv1Res(req, res, t)
	t.Logf("ver rolling mask: %x, supported: %v", client.VersionRollingMask, params.Supported)
	if client.VersionRollingMask == 0 {
		t.Error("version rolling is wrong")
	}
}

func TestSv1Authorize(t *testing.T) {
	lpipe, client, _ := initClient()
	params := authorizeParams
	req := authorizeReq
	res := sendSv1ReqAndWaitForRes(t, req, lpipe)
	validateSv1Res(req, res, t)
	resp := stratum.BooleanResult{}
	resp.FromResponse(&res)
	if resp.Result == false {
		t.Error("result was false")
	}
	t.Logf("user: %q, worker: %q", client.User, client.Nickname)
	rebuiltUser := fmt.Sprintf("%s.%s", client.User, client.Nickname)
	if rebuiltUser != params.Username {
		t.Errorf("username mismatch: expected %q, got %q", params.Username, rebuiltUser)
	}
}

func TestSv1Subscribe(t *testing.T) {
	lpipe, client, _ := initClient()

	req := subscribeReq
	res := sendSv1ReqAndWaitForRes(t, req, lpipe)
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
	validateSv1Res(req, res, t)
}

func TestSv1SuggestDifficulty(t *testing.T) {
	lpipe, client, _ := initClient()
	res := sendSv1ReqAndWaitForRes(t, suggestDifficultyReq, lpipe)
	validateSv1Res(suggestDifficultyReq, res, t)
	if client.SuggestedDifficulty != suggestDiffParams.Difficulty {
		t.Error("failed to store suggested diff")
	}
}

func TestSv1UnimplementedMethod(t *testing.T) {
	lpipe, _, _ := initClient()
	res := sendSv1ReqAndWaitForRes(t, stratum.NewRequest(29, stratum.MethodClientGetVersion, []any{}), lpipe)
	if res.Error.Code != constants.ERROR_UNK_METHOD.Code {
		t.Fatalf("expected code %d, got code %d",
			constants.ERROR_UNK_METHOD.Code, res.Error.Code,
		)
	}
}

func TestSv1InitSequence(t *testing.T) {
	lpipe, client, _ := initClient()

	res := sendSv1ReqAndWaitForRes(t, authorizeReq, lpipe)
	validateSv1Res(authorizeReq, res, t)

	res = sendSv1ReqAndWaitForRes(t, configureReq, lpipe)
	validateSv1Res(configureReq, res, t)

	res = sendSv1ReqAndWaitForRes(t, subscribeReq, lpipe)
	validateSv1Res(subscribeReq, res, t)

	/// set diff
	readSv1Pipe(t, lpipe)
	/// notify
	readSv1Pipe(t, lpipe)

	/// WHYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYYY
	time.Sleep(time.Millisecond)

	if client.CurrentJob.PrevHash == nil {
		t.Fatal("job wasnt created, did init fail?")
	}
	client.Stop()
}

func TestSv1Submit(t *testing.T) {
	lpipe := ezInitSv1Client(t, true)

	res := sendSv1ReqAndWaitForRes(t, submitReq, lpipe)
	if res.Error != nil {
		t.Fatalf("share submission failed! code: %s", res.Error)
	}
}
func TestSv1SubmitDiffTooLow(t *testing.T) {
	lpipe := ezInitSv1Client(t, false)
	res := sendSv1ReqAndWaitForRes(t, submitReq, lpipe)
	if res.Error == nil {
		t.Fatal("share submission succeeded??")
	}
	if res.Error != nil && res.Error.Code != constants.ERROR_LOW_DIFF.Code {
		t.Fatalf("share submission failed, but wrong error: %s", res.Error)
	}
}

func TestSv1SubmitUnkJob(t *testing.T) {
	lpipe := ezInitSv1Client(t, false)

	share := submitParams
	share.JobID = "fffffff"
	req := share.ToRequest(9)
	res := sendSv1ReqAndWaitForRes(t, req, lpipe)
	if res.Error == nil {
		t.Fatal("share submission succeeded??")
	}
	if res.Error != nil && res.Error.Code != constants.ERROR_STALE.Code {
		t.Fatalf("share submission failed, but wrong error: %s", res.Error)
	}
}
func TestSv1SubmitBeforeSub(t *testing.T) {
	lpipe, _, _ := initClient()

	share := submitParams
	share.JobID = ""
	req := share.ToRequest(9)
	res := sendSv1ReqAndWaitForRes(t, req, lpipe)
	if res.Error == nil {
		t.Fatal("share submission succeeded??")
	}
	if res.Error != nil && res.Error.Code != constants.ERROR_NOT_SUBBED.Code {
		t.Fatalf("share submission failed, but wrong error: %s", res.Error)
	}
}

func TestSv1ParseIdentity(t *testing.T) {
	var nickname string
	var addr address.Address
	var ok bool

	// empty
	if _, _, ok := parseIdentity("", t); ok {
		t.Fatal("parseSv2Identity should return false for empty string")
	}

	// addr + nickname
	if nickname, addr, ok = parseIdentity(authorizeParams.Username, t); !ok {
		t.Fatal("parseSv2Identity errored during parse")
	}
	if addr.EncodeAddress() != authorizeParams.Address {
		t.Fatalf("parseSv2Identity failed to parse address: expected %s, got %s", authorizeParams.Username, addr.EncodeAddress())
	}
	if nickname != authorizeParams.Worker {
		t.Fatalf("parseSv2Identity failed to parse worker name: expected %s, got %s", authorizeParams.Worker, nickname)
	}

	// just addr
	if nickname, addr, ok = parseIdentity(authorizeParams.Address, t); !ok {
		t.Fatal("parseSv2Identity errored during parse")
	}
	if addr.EncodeAddress() != authorizeParams.Address {
		t.Fatalf("parseSv2Identity failed to parse address: expected %s, got %s", authorizeParams.Address, addr.EncodeAddress())
	}
	if nickname != "" {
		t.Fatalf("parseSv2Identity failed to parse worker name: expected empty, got %s", nickname)
	}

	// just nickname
	if nickname, addr, ok = parseIdentity(authorizeParams.Worker, t); !ok {
		t.Fatal("parseSv2Identity errored during parse")
	}
	if addr.EncodeAddress() != defaultMiningAddr.EncodeAddress() {
		t.Fatalf("parseSv2Identity failed to parse username: expected %s, got %s", authorizeParams.Username, addr.EncodeAddress())
	}
	if nickname != authorizeParams.Worker {
		t.Fatalf("parseSv2Identity failed to parse worker name: expected %s, got %s", authorizeParams.Worker, nickname)
	}
}

// kinda pointless but eh
func BenchmarkSv1Submit(b *testing.B) {
	lpipe := ezInitSv1Client(b, true)

	time.Sleep(time.Second)
	for b.Loop() {
		res := sendSv1ReqAndWaitForRes(b, submitReq, lpipe)
		if res.Error != nil {
			b.Fatalf("share submission failed! code: %s", res.Error)
		}
	}
}

// sv2 tests
func TestSv2SetupConnection(t *testing.T) {
}

// util

func parseIdentity(userIdentity string, t *testing.T) (nickname string, decoded address.Address, ok bool) {
	if userIdentity == "" {
		return "", nil, false
	}
	split := strings.Split(userIdentity, ".")
	if len(split) > 1 {
		nickname = split[1]
	}
	decoded, err := address.DecodeAddress(split[0], backendChainParams)
	if err != nil {
		if defaultMiningAddr == nil {
			return "", nil, false
		}
		/// assume just the workername was passed
		if split[0] != "" {
			nickname = split[0]
		}
		decoded = defaultMiningAddr
	}
	t.Logf("address: %q\tnickname: %q", decoded.EncodeAddress(), nickname)
	return nickname, decoded, true
}

func sendSv1ReqAndWaitForRes(t testing.TB, r stratum.Message, lpipe net.Conn) stratum.Response {
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

	res := readSv1Pipe(t, lpipe)
	return res
}

func readSv1Pipe(t testing.TB, lpipe net.Conn) stratum.Response {
	reader := bufio.NewReader(lpipe)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Error(err.Error())
	}
	//t.Log("response:", string(line))
	res := stratum.Response{}
	res.Unmarshal(line)
	return res
}
func validateSv1Res(req *stratum.Request, res stratum.Response, t *testing.T) {
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
	return clientPipe, client, submissionChan
}

// flip suggDiff to true to drop the diff to 0.16
func ezInitSv1Client(t testing.TB, suggDiff bool) net.Conn {
	/// init pool state
	rng.Seed(MOCK_SEEDA, MOCK_SEEDB)
	s, _ := strconv.ParseUint(submitParams.JobID, 16, 64)
	currTemplateID = s - 1
	currTemplate, _ = CreateJobTemplate(MOCK_BLOCK_TEMPLATE)
	lpipe, c, _ := initClient()

	sendSv1ReqAndWaitForRes(t, authorizeReq, lpipe)
	sendSv1ReqAndWaitForRes(t, configureReq, lpipe)
	if suggDiff {
		sendSv1ReqAndWaitForRes(t, suggestDifficultyReq, lpipe)
	}
	sendSv1ReqAndWaitForRes(t, subscribeReq, lpipe)

	/// set diff
	readSv1Pipe(t, lpipe)
	/// notify
	readSv1Pipe(t, lpipe)

	// FIXME: race condition :\
	time.Sleep(time.Millisecond)

	c.currentJobMutex.Lock()
	defer c.currentJobMutex.Unlock()
	c.CurrentJob.MinTime = 0
	c.CurrentJob.MaxTime = 0

	return lpipe
}

// TODO
func ezInitSv2Client(t testing.TB) net.Conn {
	lpipe, c, _ := initClient()
	/// reset rng
	rng.Seed(MOCK_SEEDA, MOCK_SEEDB)
	lpipe.Write(encodeSv2(MOCK_SETUPCONNECTION, t))
	return lpipe
}
