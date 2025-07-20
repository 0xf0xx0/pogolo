package stratumclient_test

import (
	"bufio"
	"fmt"
	"net"
	"pogolo/config"
	"pogolo/stratumclient"
	"testing"

	stratum "github.com/kbnchk/go-Stratum"
)

func TestConfigure(t *testing.T) {
	lpipe, client := initClient()

	params := configureParams
	req := configureReq
	res := sendReqAndWaitForRes(t, req, lpipe)
	client.Stop()
	validateRes(req, res, t)
	t.Logf("is ver rolling: %v, ver rolling mask: %x, supported: %v", client.VersionRolling, client.VersionRollingMask, params.Supported)
	if client.VersionRolling == false ||
		client.VersionRollingMask != config.VERSION_ROLLING_MASK {
		t.Error("version rolling or mask is wrong")
	}
}

func TestAuthorize(t *testing.T) {
	lpipe, client := initClient()
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
	t.Logf("user: %q, worker: %q", *client.User, client.Worker)
	rebuiltUser := fmt.Sprintf("%s.%s", *client.User, client.Worker)
	if rebuiltUser != params.Username {
		t.Errorf("username mismatch: expected %q, got %q", params.Username, rebuiltUser)
	}
}

func TestSubscribe(t *testing.T) {
	lpipe, client := initClient()

	req := subscribeReq
	res := sendReqAndWaitForRes(t, req, lpipe)
	client.Stop()
	r := stratum.SubscribeResult{}
	err := r.Read(&res)
	if err != nil {
		t.Fatal(err.Error())
	}
	if r.ExtraNonce1 != client.ID {
		t.Errorf("extranonce1 mismatch: expected %q, got %q", client.ID, r.ExtraNonce1)
	}
	if r.ExtraNonce2Size != config.EXTRANONCE_SIZE {
		t.Errorf("extranonce2 size mismatch: expected %d, got %d",
			config.EXTRANONCE_SIZE, r.ExtraNonce2Size)
	}
	if r.Subscriptions[0].Method != stratum.MiningNotify {
		t.Errorf("subscription method mismatch: expected %q, got %q", stratum.MiningNotify, r.Subscriptions[0].Method)
	}
	validateRes(req, res, t)
}

func TestSuggestDifficulty(t *testing.T) {
	lpipe, client := initClient()
	res := sendReqAndWaitForRes(t, suggestDifficultyReq, lpipe)
	client.Stop()
	suggestDifficultyReq.MessageID = nil /// the request has a message id, but notifications dont
	validateRes(suggestDifficultyReq, res, t)
}

func TestInitSequence(t *testing.T) {
	lpipe, client := initClient()

	res := sendReqAndWaitForRes(t, authorizeReq, lpipe)
	validateRes(authorizeReq, res, t)

	res = sendReqAndWaitForRes(t, configureReq, lpipe)
	validateRes(configureReq, res, t)

	res = sendReqAndWaitForRes(t, subscribeReq, lpipe)
	validateRes(subscribeReq, res, t)

	fmt.Printf("%+v\n", client)
}

// func TestSerialize(t *testing.T) {
// 	decoded, _ := hex.DecodeString(MOCK_COINBASE)
// 	tx, _ := btcutil.NewTxFromBytes(decoded)
// 	serializedTx, _ := stratumclient.SerializeTx(tx.MsgTx(), true)
// 	t.Logf("%x %s, %+v", serializedTx, tx.Hash(), tx)
// 	if tx.WitnessHash().String() != tx0.Hash {
// 		t.Logf("data: %x", serializedTx)
// 		t.Fatalf("hash mismatch: expected %q, got %q", tx0.Hash, tx.Hash())
// 	}
// }

func TestCoinbaseCreation(t *testing.T) {
	lpipe, client := initClient()
	sendReqAndWaitForRes(t, authorizeReq, lpipe)
	sendReqAndWaitForRes(t, configureReq, lpipe)
	sendReqAndWaitForRes(t, subscribeReq, lpipe)
	client.ID, _ = stratum.DecodeID(MOCK_EXTRANONCE)

	template := stratumclient.CreateJobTemplate(getBlockTemplate())
	job := client.CreateJob(template)
	client.CurrentJob = job
	serializedCoinbaseTx, err := stratumclient.SerializeTx(job.CoinbaseTx.MsgTx(), true)
	if err != nil {
		t.Error(err)
	}
	t.Logf("coinbase: %x", serializedCoinbaseTx)
	t.Logf("sent: %+v", template)
	t.Logf("got: %+v", job.Notification)
	sendReqAndWaitForRes(t, submitReq, lpipe)
}

//

// util

func sendReqAndWaitForRes(t *testing.T, r stratum.Request, lpipe net.Conn) stratum.Response {
	b, _ := r.Marshal()
	t.Logf("sending message: %s", b)
	//time.Sleep(time.Millisecond * 100) /// if needed
	_, err := lpipe.Write(b)
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
func initClient() (net.Conn, *stratumclient.StratumClient) {
	lpipe, rpipe := net.Pipe()
	lpipe.LocalAddr()
	client := stratumclient.CreateClient(rpipe)
	client.ID, _ = stratum.DecodeID(MOCK_EXTRANONCE)
	go client.Run(true)
	return lpipe, &client
}
