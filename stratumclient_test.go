package main

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strconv"
	"testing"

	"git.0xf0xx0.eth.limo/0xf0xx0/pogolo/constants"
	"git.0xf0xx0.eth.limo/0xf0xx0/stratumv2"
	"github.com/btcsuite/btcd/address/v2"
	"github.com/btcsuite/btcd/chainhash/v2"

	"git.0xf0xx0.eth.limo/0xf0xx0/stratum"
)

/// stratum chatter

func TestSv1Configure(t *testing.T) {
	lpipe, _, _ := initClient()
	params := configureParams
	req := configureReq
	res := sendSv1ReqAndWaitForRes(t, req, lpipe)
	validateSv1Res(req, res, t)
	t.Logf("supported: %v", params.Supported)
}

func TestSv1Authorize(t *testing.T) {
	lpipe, client, _ := initClient()
	params := stratum.MiningAuthorizeParams{}
	params.Address = getAddr().EncodeAddress()
	params.Worker = "authtest"
	params.Username = params.Address + "." + params.Worker
	req := params.ToRequest(1)
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

	for {
		client.currentJobMutex.Lock()
		if client.CurrentJob.ID != 0 {
			client.currentJobMutex.Unlock()
			break
		}
		client.currentJobMutex.Unlock()
	}
	if client.CurrentJob.PrevBlock == nil {
		t.Fatal("job wasnt created, did init fail?")
	}
	client.Stop()
}
func TestSv1Submit(t *testing.T) {
	lpipe, c := ezInitSv1Client(t, true)
	c.TargetDifficulty = 0.00016 /// low-diff, bypassing min-diff
	share, found := shittyCpuMiner(c.TargetDifficulty, false, c, t)
	if !found {
		t.Error("failed to find a hash in 2^32 iters, bad luck")
		return
	}
	res := sendSv1ReqAndWaitForRes(t, share, lpipe)
	if res.Error != nil {
		t.Fatalf("share submission failed! code: %s", res.Error)
	}
}

func TestSv1SubmitDiffTooLow(t *testing.T) {
	submit := stratum.MiningSubmitParams{
		JobID:       strconv.FormatUint(currTemplateID, 16),
		Time:        uint32(currTemplate.Header.Timestamp.Unix()),
		Nonce:       0,
		Extranonce2: make([]byte, conf.ExtraNonce2Size),
		VersionMask: 0,
	}
	lpipe, _ := ezInitSv1Client(t, false)
	res := sendSv1ReqAndWaitForRes(t, submit.ToRequest(5661), lpipe)
	if res.Error == nil {
		t.Fatal("share submission succeeded??")
	}
	if res.Error != nil && res.Error.Code != constants.ERROR_LOW_DIFF.Code {
		t.Fatalf("share submission failed, but wrong error: %s", res.Error)
	}
}

func TestVersionValidationEdgeCase(t *testing.T) {
	/// set job version
	conf.BIPVersionBits = 0x00000000
	lpipe, c := ezInitSv1Client(t, true)
	c.TargetDifficulty = 0.00016 /// low-diff, bypassing min-diff
	share, found := shittyCpuMiner(c.TargetDifficulty, true, c, t)
	if !found {
		t.Error("failed to find a hash in 2^32 iters, bad luck")
		return
	}
	submitParams := &stratum.MiningSubmitParams{}
	submitParams.FromRequest(share)
	// t.Logf("%x", c.CurrentJob.Header.Version)

	/// explicitly set version mask to 0
	submitParams.VersionMask = 0
	r := submitParams.ToRequest(20)
	/// expected hash: 00000001cac12f82479784bf2687b6cf5f455f5912b3c88e0c6e97bf6dc643c3
	res := sendSv1ReqAndWaitForRes(t, r, lpipe)
	// t.Logf("%x", c.CurrentJob.Header.Version)
	// nVersion := ((uint32(currTemplate.Header.Version) | uint32(conf.BIPVersionBits)) & ^constants.VERSION_ROLLING_MASK) | (uint32(submitParams.VersionMask) & constants.VERSION_ROLLING_MASK)
	// recovered := (^uint32(c.CurrentJob.Version) & nVersion) | (nVersion & constants.VERSION_ROLLING_MASK)
	// t.Logf("%x %x", nVersion, recovered)
	/// reset state for later tests
	submitParams.VersionMask = -1
	conf.BIPVersionBits = 0

	if res.Error != nil {
		t.Fatalf("share submission failed: %s", res.Error)
	}
}

/// TODO: takes too long to run (5s)
// func TestVersionValidation(t *testing.T) {
// 	jobVersion := uint32(0x20202020)
//
// 	for i := range uint32(0xffffffff) {
// 		/// this efffectively rolls the version bits
// 		shareMask := i & constants.VERSION_ROLLING_MASK
// 		/// do bip 310 nVersion calc...
// 		nVersion := (jobVersion & ^constants.VERSION_ROLLING_MASK) | (shareMask & constants.VERSION_ROLLING_MASK)
// 		/// then attempt to recover mask by doing the inverse of the ver calc
// 		recoveredMask := (^jobVersion & nVersion) | (nVersion & constants.VERSION_ROLLING_MASK)
//
// 		if recoveredMask != shareMask {
// 			t.Logf("ver: %x, shareMask: %x, recoveredMask: %x", nVersion, shareMask, recoveredMask)
// 			t.Fatalf("recoveredMask should be %x, got %x", shareMask, recoveredMask)
// 		}
// 	}
// }

func TestSv1SubmitUnkJob(t *testing.T) {
	lpipe, _ := ezInitSv1Client(t, false)

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

func TestParseIdentity(t *testing.T) {
	var nickname string
	var addr address.Address
	var ok bool
	mockAddr := getAddr().EncodeAddress()
	mockWorker := "violet.nicky.colette"

	// empty
	if _, _, ok := parseIdentityTest("", t); ok {
		t.Fatal("parseIdentity should return false for empty string")
	}

	// addr + nickname
	if nickname, addr, ok = parseIdentityTest(mockAddr+"."+mockWorker, t); !ok {
		t.Fatal("parseIdentity errored during parse")
	}
	if addr.EncodeAddress() != mockAddr {
		t.Fatalf("parseIdentity failed to parse address: expected %s, got %s", mockAddr, addr.EncodeAddress())
	}
	if nickname != mockWorker {
		t.Fatalf("parseIdentity failed to parse worker name: expected %s, got %s", mockWorker, nickname)
	}

	// just addr
	t.Log("===== just addr =====")
	if nickname, addr, ok = parseIdentityTest(mockAddr, t); !ok {
		t.Fatal("parseIdentity errored during parse")
	}
	if addr.EncodeAddress() != mockAddr {
		t.Fatalf("parseIdentity failed to parse address: expected %s, got %s", mockAddr, addr.EncodeAddress())
	}
	if nickname != "" {
		t.Fatalf("parseIdentity failed to parse worker name: expected empty, got %s", nickname)
	}

	// just nickname
	t.Log("===== just nickname (with periods) =====")
	if nickname, addr, ok = parseIdentityTest(mockWorker, t); !ok {
		t.Fatal("parseIdentity errored during parse")
	}
	if addr.EncodeAddress() != defaultMiningAddr.EncodeAddress() {
		t.Fatalf("parseIdentity failed to parse username: expected %s, got %s", defaultMiningAddr.EncodeAddress(), addr.EncodeAddress())
	}
	if nickname != mockWorker {
		t.Fatalf("parseIdentity failed to parse worker name: expected %s, got %s", mockWorker, nickname)
	}
}

// sv2 tests

func TestSv2Ident(t *testing.T) {
	init := func(setup stratumv2.SetupConnection) *StratumClient {
		lpipe, c, _ := initClient()
		defer lpipe.Close()
		hs := &stratumv2.HandshakeState{}
		send, recv, err := hs.PerformHandshakeInitiator(lpipe)
		if err != nil {
			t.Fatal(err)
		}
		f := &stratumv2.Frame{}
		f.FromParams(stratumv2.MessageSetupConnection, &setup)
		_, err = send.EncryptFrameToWriter(f, lpipe)
		if err != nil {
			t.Fatal(err)
		}
		_, err = recv.DecryptFrameFromReader(lpipe)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}

	setup := stratumv2.SetupConnection{
		Protocol:     stratumv2.MiningProtocol,
		MinVersion:   stratumv2.ProtocolVersion,
		MaxVersion:   stratumv2.ProtocolVersion,
		Flags:        stratumv2.RequiresVersionRollingFlag,
		EndpointPort: 5661,
		EndpointHost: "pogolo@umbrel",
		// DeviceVendor:          "bitaxe",
		// DeviceHardwareVersion: "FTXGOXX",
		// DeviceFirmware:        "v2021-08-24",
		// DeviceID:              "",
	}

	cocli := init(setup)
	EXPECTED := "unknown"
	if cocli.UserAgent != EXPECTED {
		t.Fatalf("useragent mismatch: expected %q, got %q", EXPECTED, cocli.UserAgent)
	}

	setup.DeviceVendor = "bitaxe"
	cocli = init(setup)
	EXPECTED = "bitaxe"
	if cocli.UserAgent != EXPECTED {
		t.Fatalf("useragent mismatch: expected %q, got %q", EXPECTED, cocli.UserAgent)
	}

	setup.DeviceID = "D-Vize"
	cocli = init(setup)
	EXPECTED = "bitaxe/D-Vize"
	if cocli.UserAgent != EXPECTED {
		t.Fatalf("useragent mismatch: expected %q, got %q", EXPECTED, cocli.UserAgent)
	}

	setup.DeviceVendor = ""
	setup.DeviceHardwareVersion = "BM1370"
	cocli = init(setup)
	EXPECTED = "BM1370/D-Vize"
	if cocli.UserAgent != EXPECTED {
		t.Fatalf("useragent mismatch: expected %q, got %q", EXPECTED, cocli.UserAgent)
	}

	setup.DeviceID = ""
	cocli = init(setup)
	EXPECTED = "BM1370"
	if cocli.UserAgent != EXPECTED {
		t.Fatalf("useragent mismatch: expected %q, got %q", EXPECTED, cocli.UserAgent)
	}
}
func TestSv2FullSetup(t *testing.T) {
	ezInitSv2Client(t)
	// lpipe, send, recv := ezInitSv2Client(t)
}

// kinda pointless but eh
func BenchmarkSv1Submit(b *testing.B) {
	lpipe, _ := ezInitSv1Client(b, true)
	sub := &stratum.MiningSubmitParams{
		Name:        "ngoiwp",
		JobID:       submitParams.JobID,
		Time:        submitParams.Time,
		Nonce:       submitParams.Nonce,
		Extranonce2: []byte{0, 255, 0, 255},
		VersionMask: 0xff0000,
	}
	bin, err := sub.ToRequest(5).Marshal()
	if err != nil {
		b.Fatalf("error marshalling req: %s", err)
	}

	for b.Loop() {
		_, err = lpipe.Write(bin)
		if err != nil {
			b.Fatal(err.Error())
		}

		res := readSv1Pipe(b, lpipe)
		if res.Error != nil && res.Error.Code != constants.ERROR_LOW_DIFF.Code {
			b.Fatalf("share submission failed! code: %s", res.Error)
		}
	}
}

func BenchmarkSv2Submit(b *testing.B) {
	lpipe, send, recv := ezInitSv2Client(b)

	en1 := stratum.ID(123)
	params := &stratumv2.SubmitSharesExtended{
		SubmitSharesStandard: stratumv2.SubmitSharesStandard{
			ChannelID: uint32(en1),
			Sequence:  123556,
			JobID:     uint32(currTemplateID),
			Nonce:     submitParams.Nonce,
			Time:      submitParams.Time,
			Version:   0x20beef00,
		},
		Extranonce: []byte{0, 255, 0, 255},
	}
	f, _ := stratumv2.NewFrameFromParams(stratumv2.MessageSubmitSharesExtended, stratumv2.ExtensionTypeCore, params)
	for b.Loop() {
		bin, _ := send.EncryptFrame(f)
		res := sendSv2ReqAndWaitForRes(b, bin, lpipe, recv)
		if res.MessageType != stratumv2.MessageSubmitSharesSuccess &&
			res.MessageType != stratumv2.MessageSubmitSharesError {
			b.Fatalf("share submission failed! code: %s", res.MessageType)
		}
	}
}

// util

func parseIdentityTest(userIdentity string, t *testing.T) (nickname string, decoded address.Address, ok bool) {
	t.Log(userIdentity)
	decoded, nickname, err := parseIdentity(userIdentity)
	if err == nil {
		ok = true
	} else {
		t.Log(err)
		return nickname, decoded, ok
	}
	t.Logf("address: %q\tnickname: %q", decoded.EncodeAddress(), nickname)
	return nickname, decoded, ok
}

func sendSv1ReqAndWaitForRes(t testing.TB, r stratum.Message, lpipe net.Conn) stratum.Response {
	b, err := r.Marshal()
	if err != nil {
		t.Fatalf("error marshalling req: %s", err)
	}
	// t.Logf("sending message: %s", b)

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
	client := createClient(poolPipe, submissionChan)
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
func ezInitSv1Client(t testing.TB, suggDiff bool) (net.Conn, *StratumClient) {
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

	for {
		c.currentJobMutex.Lock()
		if c.CurrentJob.ID != 0 {
			c.currentJobMutex.Unlock()
			break
		}
		c.currentJobMutex.Unlock()
	}

	c.currentJobMutex.Lock()
	defer c.currentJobMutex.Unlock()
	c.CurrentJob.MinTime = 0
	c.CurrentJob.MaxTime = 0

	return lpipe, c
}

func ezInitSv2Client(t testing.TB) (net.Conn, *stratumv2.CipherState, *stratumv2.CipherState) {
	lpipe, c, _ := initClient()
	_ = c
	hs := &stratumv2.HandshakeState{}
	send, recv, err := hs.PerformHandshakeInitiator(lpipe)
	if err != nil {
		t.Fatal(err)
	}
	_, err = send.EncryptFrameToWriter(MOCK_SETUPCONNECTION, lpipe)
	if err != nil {
		t.Fatal(err)
	}
	_, err = recv.DecryptFrameFromReader(lpipe)
	if err != nil {
		t.Fatal(err)
	}
	_, err = send.EncryptFrameToWriter(MOCK_OPENEXTENDEDCHANNEL, lpipe)
	if err != nil {
		t.Fatal(err)
	}
	_, err = recv.DecryptFrameFromReader(lpipe)
	if err != nil {
		t.Fatal(err)
	}

	/// settarget
	_, err = recv.DecryptFrameFromReader(lpipe)
	if err != nil {
		t.Fatal(err)
	}
	/// recv job
	_, err = recv.DecryptFrameFromReader(lpipe)
	if err != nil {
		t.Fatal(err)
	}
	/// recv prevhash
	_, err = recv.DecryptFrameFromReader(lpipe)
	if err != nil {
		t.Fatal(err)
	}

	for {
		c.currentJobMutex.Lock()
		if c.CurrentJob.ID != 0 {
			c.currentJobMutex.Unlock()
			break
		}
		c.currentJobMutex.Unlock()
	}
	c.currentJobMutex.Lock()
	defer c.currentJobMutex.Unlock()
	c.CurrentJob.MinTime = 0
	c.CurrentJob.MaxTime = 0

	return lpipe, send, recv
}
func sendSv2ReqAndWaitForRes(t testing.TB, frame []byte, lpipe net.Conn, readcrypt *stratumv2.CipherState) *stratumv2.Frame {
	_, err := lpipe.Write(frame)
	if err != nil {
		t.Log("write")
		t.Fatal(err.Error())
	}

	res, err := readcrypt.DecryptFrameFromReader(lpipe)
	if err != nil {
		t.Log("read")
		t.Fatal(err.Error())
	}
	return res
}

func shittyCpuMiner(target float64, versionMask bool, c *StratumClient, t *testing.T) (*stratum.Request, bool) {
	job := c.CurrentJob
	header := job.MsgBlock.Copy().Header
	submit := stratum.MiningSubmitParams{
		JobID:       strconv.FormatUint(job.ID, 16),
		Time:        uint32(header.Timestamp.Unix()),
		Nonce:       0,
		Extranonce2: make([]byte, conf.ExtraNonce2Size),
		VersionMask: -1,
	}
	if versionMask {
		submit.VersionMask = 0
		header.Version = int32((uint32(header.Version) & ^constants.VERSION_ROLLING_MASK) | (uint32(submit.VersionMask) & constants.VERSION_ROLLING_MASK))
	}

	branches := make([]*chainhash.Hash, 1, len(job.MerkleBranch)+1)
	coinbaseTxHash := simdSha256d(job.CoinbaseBytes)
	branches[0] = &coinbaseTxHash
	branches = append(branches, job.MerkleBranch...)
	header.MerkleRoot = *merkleRootFromBranches(branches)

	stop := uint32(4294967290)

	t.Logf("mining for %d iters (target: %f)", stop, target)
	hash := chainhash.Hash{}
	for {
		if header.Nonce >= stop {
			break
		}
		hash = simdHeaderHash(&header)
		diff := calcDifficulty(hash)
		if diff >= target {
			submit.Nonce = header.Nonce
			t.Logf("found share: %f", diff)
			return submit.ToRequest(5661), true
		}
		header.Nonce++
	}
	return nil, false
}
