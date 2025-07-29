package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"math"
	"net"
	"pogolo/constants"
	"slices"
	"strings"
	"time"

	"github.com/0xf0xx0/stratum"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/fatih/color"
)

// stats for the api
type ClientStats struct {
	lastSubmission     time.Time
	avgSubmissionDelta uint64 // in ms
	sharesAccepted,
	sharesRejected uint64
	bestDiff,
	hashrate float64
}
type StratumClient struct {
	ID                  stratum.ID
	User                btcutil.Address
	Worker              string
	Password            string
	UserAgent           string
	Difficulty          float64
	SuggestedDifficulty float64
	VersionRolling      bool
	VersionRollingMask  uint32
	/// internal
	conn           net.Conn
	messageChan    chan any // messages to/from client
	submissionChan chan<- *btcutil.Block
	CurrentJob     MiningJob
	Stats          ClientStats
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
	/// 15 secs to send the initial stratum message
	client.conn.SetDeadline(time.Now().Add(time.Second * 15))

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
					//client.setDifficulty(0.16)
				} else {
					client.setDifficulty(conf.Pogolo.DefaultDifficulty)
				}
			}
			go client.adjustDiffRoutine()
			client.messageChan <- "ready"
		}

		line, err := reader.ReadBytes([]byte("\n")[0])
		if err != nil {
			if err != io.ErrClosedPipe {
				client.error(fmt.Sprintf("read error: %s", err))
			}
			break readloop
		}
		client.conn.SetDeadline(time.Now().Add(time.Minute * 5))
		m, err := DecodeStratumMessage(line)
		if err != nil {
			client.error(fmt.Sprintf("stratum decode error: %s", err))
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
							/// uhhhhhhhhhhhhhhhh
							/// honestly just leave this as a panic
							panic(err)
						}
					} else {
						/// *uhhhhhhhhhhhhhhhh*
						client.error("couldnt read version rolling mask? shouldnt happen i *think*")
						break readloop
					}
				}
				client.writeRes(stratum.ConfigureResponse(m.MessageID, res))
			}
		case stratum.MiningAuthorize:
			{
				if isAuthed {
					break
				}
				params := stratum.AuthorizeParams{}
				params.Read(m)
				split := strings.Split(params.Username, ".")
				if len(split) > 1 {
					client.Worker = split[1]
				}
				if conf.Pogolo.Password != "" && params.Password != conf.Pogolo.Password {
					client.error("invalid password")
					client.writeRes(stratum.NewErrorResponse(m.MessageID, constants.ERROR_NOT_ACCEPTED))
				}
				decoded, err := btcutil.DecodeAddress(split[0], conf.Backend.ChainParams)
				if err != nil {
					client.error(fmt.Sprintf("failed decoding address: %s", err))
					client.writeRes(stratum.NewErrorResponse(m.MessageID, constants.ERROR_INTERNAL))
					return
				}
				client.User = decoded
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
					client.error(fmt.Sprintf("couldnt read mining.suggest_difficulty: %s", err))
					break
				}
				suggestedDiff := params.Difficulty.(float64)
				/// only accept a suggested difficulty
				/// if we haven't got one before
				if client.SuggestedDifficulty == 0 &&
					suggestedDiff != client.Difficulty &&
					suggestedDiff > constants.MIN_DIFFICULTY &&
					stratum.ValidDifficulty(suggestedDiff) {
					client.SuggestedDifficulty = suggestedDiff
					/// and only send a mining.set_difficulty if accepted,
					/// we wanna ignore
					if err := client.setDifficulty(suggestedDiff); err != nil {
						client.error(fmt.Sprintf("failed to adjust difficulty: %s", err))
						client.writeRes(stratum.NewErrorResponse(m.MessageID, constants.ERROR_INTERNAL))
					}
				} else {
					client.writeRes(stratum.NewErrorResponse(m.MessageID, constants.ERROR_NOT_ACCEPTED))
				}
			}
		case stratum.MiningSubmit:
			{
				/// TODO: adjust diff every 6 submissions?
				if !stratumInited {
					client.error("submit before subscribe")
					return
				}
				s := stratum.Share{}
				err := s.Read(m)
				if err != nil {
					panic(err)
				}
				client.validateShareSubmission(s, m)
			}
		default:
			{
				client.writeRes(stratum.NewErrorResponse(m.MessageID, constants.ERROR_UNK_METHOD))
				client.error(fmt.Sprintf("unhandled stratum message: %+v", m))
			}
		}
	}
}
func (client *StratumClient) Stop() {
	client.writeChan("done")
	close(client.messageChan)
	client.conn.Close()
}

func (client *StratumClient) adjustDiffRoutine() {
	for {
		time.Sleep(time.Minute * 3)
		if client.Stats.avgSubmissionDelta == 0 {
			continue
		}
		difference := int64(conf.Pogolo.TargetShareInterval) - int64(client.Stats.avgSubmissionDelta/1000)
		/// natural variance is +- 3s
		if +difference <= 3 {
			continue
		}
		/// cap the adjustment at +-2^12
		delta := min(math.Pow(2, float64(+difference)), 4096)
		if difference < 0 {
			client.setDifficulty(client.Difficulty - delta)
		} else {
			client.setDifficulty(client.Difficulty + delta)
		}
		client.log(fmt.Sprintf("adjusted share target: %f (delta: %d)", client.Difficulty, delta))
	}
}
func (client *StratumClient) readChanRoutine() {
	for {
		m, ok := <-client.messageChan
		if !ok {
			/// closed
			return
		}
		switch m.(type) {
		case *JobTemplate:
			{
				client.CurrentJob = client.createJob(DeepCopyTemplate(m.(*JobTemplate)))
				client.writeNotif(stratum.Notify(client.CurrentJob.NotifyParams))
			}
		}
	}
}

func (client *StratumClient) Channel() chan any {
	return client.messageChan
}
func (client *StratumClient) Addr() net.Addr {
	return client.conn.RemoteAddr()
}
func (client *StratumClient) setDifficulty(newDiff float64) error {
	if newDiff == client.Difficulty {
		client.error(fmt.Sprintf("failed to set new diff %f: old %f", newDiff, client.Difficulty))
		return nil
	}
	if err := client.writeNotif(stratum.SetDifficulty(newDiff)); err != nil {
		return err
	}
	client.log(fmt.Sprintf("set new diff: %f", newDiff))
	client.Difficulty = newDiff
	return nil
}

// TODO: more validation?
func (client *StratumClient) validateShareSubmission(s stratum.Share, m *stratum.Request) {
	if s.JobID != client.CurrentJob.NotifyParams.JobID {
		client.Stats.sharesRejected++
		client.writeRes(stratum.NewErrorResponse(m.MessageID, constants.ERROR_UNK_JOB))
		return
	}

	updatedBlock, err := client.CurrentJob.Template.UpdateBlock(client, s, client.CurrentJob.NotifyParams)
	if err != nil {
		/// maybe add client.Stats.sharesLost? lmfao
		client.writeRes(stratum.NewErrorResponse(m.MessageID, constants.ERROR_INTERNAL))
		return
	}

	shareDiff := CalcDifficulty(updatedBlock.Header)
	if shareDiff >= float64(client.Difficulty) {
		if shareDiff >= client.CurrentJob.Template.NetworkDiff {
			client.submitBlock(btcutil.NewBlock(updatedBlock))
		}
		client.Stats.sharesAccepted++
		if shareDiff > client.Stats.bestDiff {
			client.Stats.bestDiff = shareDiff
			client.log("new best session diff!")
		}
		now := time.Now()
		if client.Stats.lastSubmission.Unix() > 0 {
			/// rolling avg
			/// wikipedia my beloved
			/// https://en.wikipedia.org/wiki/Moving_average#Cumulative_average
			submission := uint64(now.Sub(client.Stats.lastSubmission).Milliseconds())
			/// start the avg calc with the furst delta, not 0
			if client.Stats.avgSubmissionDelta == 0 {
				client.Stats.avgSubmissionDelta = submission
			} else {
				client.Stats.avgSubmissionDelta =
					((client.Stats.avgSubmissionDelta * (constants.SUBMISSION_DELTA_WINDOW - 1)) + submission) / constants.SUBMISSION_DELTA_WINDOW
			}
		}
		client.Stats.lastSubmission = now
		client.log(fmt.Sprintf("share accepted: diff %.3f (best: %.3f), avg submission delta %ds", shareDiff, client.Stats.bestDiff, client.Stats.avgSubmissionDelta/1000))
		client.writeRes(stratum.BooleanResponse(m.MessageID, true))
	} else {
		client.Stats.sharesRejected++
		client.writeRes(stratum.NewErrorResponse(m.MessageID, constants.ERROR_DIFF_TOO_LOW))
		client.log(fmt.Sprintf("share rejected: diff %f", shareDiff))

		hdr := bytes.NewBuffer([]byte{})
		updatedBlock.Header.Serialize(hdr)
		println("share:", fmt.Sprintf("%+v", s))
		println(fmt.Sprintf("header: %x", hdr))
		println(fmt.Sprintf("diff: %f", shareDiff))
	}
}
func (client *StratumClient) CreateJob(template *JobTemplate) MiningJob {
	return client.createJob(template)
}
func (client *StratumClient) createJob(template *JobTemplate) MiningJob {
	blockHeader := template.Block.MsgBlock().Header

	merkleBranches := make([][]byte, len(template.MerkleBranch))
	for i, branch := range template.MerkleBranch {
		merkleBranches[i] = branch[:]
	}

	coinbaseTx := CreateCoinbaseTx(client.User, *template, conf.Backend.ChainParams)
	/// serialized without the witness, we handle that on submission
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
		/// minus 8 cause we wanna lop off the extranonce padding
		CoinbasePart1: serializedCoinbaseTx[:partOneIndex-8],
		CoinbasePart2: serializedCoinbaseTx[partOneIndex:],
		Clean:         template.Clear,
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
	client.log("block submitted")
}
func (client *StratumClient) writeRes(res stratum.Response) error {
	bytes, err := res.Marshal()
	if err != nil {
		client.error(fmt.Sprintf("failed to marshal response: %s", err))
		return err
	}

	return client.writeConn(bytes)
}
func (client *StratumClient) writeNotif(n stratum.Notification) error {
	bytes, err := n.Marshal()
	if err != nil {
		client.error(fmt.Sprintf("failed to marshal notification: %s", err))
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
	if client.Worker != "" {
		fmt.Printf("%s: %s\n", color.CyanString(client.Worker), s)
	} else {
		fmt.Printf("(%s): %s\n", color.CyanString(client.ID.String()), s)
	}
}
func (client *StratumClient) error(s string) {
	if client.Worker != "" {
		println(fmt.Sprintf("%s: %s", color.CyanString(client.Worker), color.RedString(s)))
	} else {
		println(fmt.Sprintf("%s: %s", color.CyanString(client.ID.String()), color.RedString(s)))
	}
}

func CreateClient(conn net.Conn, submissionChannel chan<- *btcutil.Block) StratumClient {
	client := StratumClient{
		conn:           conn,
		ID:             ClientIDHash(),
		messageChan:    make(chan any),
		submissionChan: submissionChannel,
		Difficulty:     1,
	}
	return client
}
