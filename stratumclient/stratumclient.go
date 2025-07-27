package stratumclient

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"pogolo/config"
	"pogolo/constants"
	"slices"
	"strings"
	"time"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/0xf0xx0/stratum"
)

type StratumClient struct {
	ID                  stratum.ID
	User                btcutil.Address
	Worker              string
	Password            string
	UserAgent           string
	Difficulty          float64
	VersionRolling      bool
	VersionRollingMask  uint32
	SuggestedDifficulty float64
	/// internal
	conn           net.Conn
	messageChan    chan any // messages to/from client
	submissionChan chan<- *btcutil.Block
	CurrentJob     MiningJob
	//activeJobs []stratum.NotifyParams 	// TODO: remove? maybe have only 2 jobs (the current and the previous)?
}

func (client *StratumClient) Run(noCleanup bool) {
	if !noCleanup {
		defer client.Stop()
	}
	go client.readChanRoutine()
	stratumInited := false
	isAuthed := false
	isSubscribed := false

	reader := bufio.NewReader(client.conn)
	client.conn.SetDeadline(time.Now().Add(time.Second * 5))

readloop:
	for {
		if isAuthed && isSubscribed && !stratumInited {
			stratumInited = true
			/// the initial difficulty was set in CreateClient,
			/// but the client may also suggested a difficulty before
			/// fully initialized
			/// if they haven't, alert them to our default diff
			if client.SuggestedDifficulty == 0 {
				if strings.Contains(client.UserAgent, "cpuminer") {
					client.Difficulty = 0.16
				}
				client.AdjustDifficulty(client.Difficulty)
			}
			client.messageChan <- "ready"
		}
		line, err := reader.ReadBytes([]byte("\n")[0])
		if err != nil {
			if err != io.ErrClosedPipe {
				client.log(fmt.Sprintf("read error: %s", err))
			}
			break readloop
		}
		client.conn.SetDeadline(time.Now().Add(time.Minute * 5))
		m, err := DecodeStratumMessage(line)
		if err != nil {
			client.log(fmt.Sprintf("stratum decode error: %s", err))
			break readloop
		}

		switch m.GetMethod() {
		case stratum.MiningConfigure:
			{
				params := stratum.ConfigureParams{}
				params.Read(m)
				res := stratum.ConfigureResult{}
				if slices.Contains(params.Supported, "version-rolling") {
					if _, ok := params.Parameters["version-rolling.mask"]; ok {
						client.VersionRolling = true
						client.VersionRollingMask = 0xffffffff
						//client.VersionRollingMask = mask ^ constants.VERSION_ROLLING_MASK

						err = res.Add(stratum.VersionRollingConfigurationResult{Accepted: true, Mask: client.VersionRollingMask})
						if err != nil {
							panic(err)
						}
					} else {
						/// uhhhhhhhhhhhhhhhh
						break readloop
					}
				}
				client.writeRes(stratum.ConfigureResponse(m.MessageID, res))
			}
		case stratum.MiningAuthorize:
			{
				/// TODO: disconnect?
				if isAuthed {
					break
				}
				params := stratum.AuthorizeParams{}
				params.Read(m)
				split := strings.Split(params.Username, ".")
				decoded, err := btcutil.DecodeAddress(split[0], config.CHAIN)
				if err != nil {
					client.log(fmt.Sprintf("failed decoding address: %s", err))
					client.writeRes(stratum.NewErrorResponse(m.MessageID, constants.ERROR_INTERNAL))
					return
				}
				client.User = decoded
				if len(split) > 1 {
					client.Worker = split[1]
				}
				client.Password = params.Password
				client.writeRes(stratum.AuthorizeResponse(m.MessageID, true))
				isAuthed = true
			}
		case stratum.MiningSubscribe:
			{
				if isSubscribed {
					break
				}
				subParams := stratum.SubscribeParams{}
				subParams.Read(m)
				client.UserAgent = subParams.UserAgent
				params := stratum.SubscribeResult{
					Subscriptions: []stratum.Subscription{
						{
							Method:    stratum.MiningNotify,
							SessionID: client.ID,
						},
					},
					ExtraNonce1:     client.ID,
					ExtraNonce2Size: constants.EXTRANONCE_SIZE,
				}
				client.writeRes(stratum.SubscribeResponse(m.MessageID, params))
				isSubscribed = true
			}
		case stratum.MiningSuggestDifficulty:
			{
				params := stratum.SuggestDifficultyParams{}

				if err := params.Read(m); err != nil {
					println("invalid difficulty from", client.ID, err.Error())
					break
				}
				suggestedDiff := params.Difficulty.(float64)
				if suggestedDiff > 0.1 &&
					stratum.ValidDifficulty(suggestedDiff) &&
					client.SuggestedDifficulty == 0 {
					/// only accept a suggested difficulty
					/// if we haven't got one before
					client.SuggestedDifficulty = suggestedDiff
					client.Difficulty = suggestedDiff
					/// and only send a mining.set_difficulty if accepted,
					/// we wanna ignore
					if err := client.AdjustDifficulty(client.Difficulty); err != nil {
						panic(err)
					}
				}
			}
		case stratum.MiningSubmit:
			{
				/// TODO: adjust diff every 6 submissions
				if !stratumInited {
					println("submit before subscribe")
					return
				}
				s := stratum.Share{}
				err := s.Read(m)
				if err != nil {
					panic(err)
				}
				/// TODO: validate share
				client.validateShareSubmission(s, m)
			}
		default:
			{
				client.writeRes(stratum.NewErrorResponse(m.MessageID, constants.ERROR_UNK_METHOD))
				client.log(fmt.Sprintf("unhandled stratum message: %+v", m))
			}
		}
	}
}

// TODO: more validation?
func (client *StratumClient) validateShareSubmission(s stratum.Share, m *stratum.Request) {
	if s.JobID != client.CurrentJob.NotifyParams.JobID {
		client.writeRes(stratum.NewErrorResponse(m.MessageID, constants.ERROR_UNK_JOB))
		return
	}
	updatedBlock, err := client.CurrentJob.Template.UpdateBlock(client, s, client.CurrentJob.NotifyParams)
	if err != nil {
		client.writeRes(stratum.NewErrorResponse(m.MessageID, constants.ERROR_INTERNAL))
		return
	}
	shareDiff := CalcDifficulty(updatedBlock.Header)
	if shareDiff >= float64(client.Difficulty) {
		if shareDiff >= client.CurrentJob.Template.NetworkDiff {
			client.submitBlock(&client.CurrentJob.Template.Block)
		}
		client.writeRes(stratum.BooleanResponse(m.MessageID, true))
	} else {
		client.writeRes(stratum.NewErrorResponse(m.MessageID, constants.ERROR_DIFF_TOO_LOW))
	}
}
func (client *StratumClient) Stop() {
	client.writeChan("done")
	close(client.messageChan)
	client.conn.Close()
}

func (client *StratumClient) Channel() chan any {
	return client.messageChan
}
func (client *StratumClient) Addr() net.Addr {
	return client.conn.RemoteAddr()
}
func (client *StratumClient) AdjustDifficulty(newDiff float64) error {
	return client.writeNotif(stratum.SetDifficulty(newDiff))
}
func (client *StratumClient) readChanRoutine() {
	for {
		switch m := (<-client.messageChan).(type) {
		case string:
			{
				//println(m)
			}
		case *JobTemplate:
			{
				client.CurrentJob = client.createJob(DeepCopyTemplate(m))
				client.writeNotif(stratum.Notify(client.CurrentJob.NotifyParams))
			}
		default:
			{
				/// closed
				return
			}
		}
	}
}
func (client *StratumClient) CreateJob(template *JobTemplate) MiningJob {
	return client.createJob(template)
}
func (client *StratumClient) createJob(template *JobTemplate) MiningJob {
	merkleBranches := make([][]byte, len(template.MerkleBranch))
	for i, branch := range template.MerkleBranch {
		merkleBranches[i] = branch[:]
	}

	blockHeader := template.Block.MsgBlock().Header
	coinbaseTx := CreateCoinbaseTx(client.User, *template, config.CHAIN)
	serializedCoinbaseTx, err := SerializeTx(coinbaseTx.MsgTx(), false)
	if err != nil {
		panic(err)
	}
	inputScript := coinbaseTx.MsgTx().TxIn[0].SignatureScript
	partOneIndex := bytes.Index(serializedCoinbaseTx, inputScript)
	/// TODO/FIXME: honestly this shouldnt be less than halfway
	if partOneIndex < 0 {
		panic("partOneIndex shoudnt be below 0")
	}
	partOneIndex += len(inputScript)

	params := stratum.NotifyParams{
		JobID:          template.ID,
		PrevBlockHash:  &blockHeader.PrevBlock,
		MerkleBranches: merkleBranches,
		Version:        uint32(blockHeader.Version),
		Bits:           template.Bits,
		Timestamp:      blockHeader.Timestamp,
		CoinbasePart1:  serializedCoinbaseTx[:partOneIndex-8],
		CoinbasePart2:  serializedCoinbaseTx[partOneIndex:],
		Clean:          template.Clear,
	}
	job := MiningJob{
		Template:     template,
		NotifyParams: params,
	}

	return job
}

// TODO: how to get errors back?
func (client *StratumClient) submitBlock(block *btcutil.Block) {
	client.submissionChan <- block
}
func (client *StratumClient) writeRes(res stratum.Response) error {
	bytes, err := res.Marshal()
	if err != nil {
		client.log(fmt.Sprintf("failed to marshal response: %s", err))
		return err
	}

	return client.writeConn(bytes)
}
func (client *StratumClient) writeNotif(n stratum.Notification) error {
	bytes, err := n.Marshal()
	if err != nil {
		client.log(fmt.Sprintf("failed to marshal notification: %s", err))
		return err
	}

	return client.writeConn(bytes)
}
func (client *StratumClient) writeConn(b []byte) error {
	_, err := client.conn.Write(b)
	return err
}

// pass a message back to the server
func (client *StratumClient) writeChan(msg any) {
	client.messageChan <- msg
}
func (client *StratumClient) log(s string) {
	println(fmt.Sprintf("%s: %s", client.ID.String(), s))
}

func CreateClient(conn net.Conn, submissionChannel chan<- *btcutil.Block) StratumClient {
	client := StratumClient{
		conn:           conn,
		ID:             ClientIDHash(),
		messageChan:    make(chan any),
		submissionChan: submissionChannel,
		Difficulty:     config.DEFAULT_DIFFICULTY,
	}
	return client
}
