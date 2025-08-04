package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"pogolo/constants"
	"slices"
	"strings"
	"time"

	"github.com/0xf0xx0/oigiki"
	"github.com/0xf0xx0/stratum"
	"github.com/btcsuite/btcd/btcutil"
)

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
	// internal
	conn           net.Conn
	statusChan     chan string // messages to/from client
	templateChan   chan *JobTemplate
	submissionChan chan<- BlockSubmission
	CurrentJob     MiningJob
	Stats          ClientStats
	//activeJobs []stratum.NotifyParams 	// TODO: remove? maybe have only 2 jobs (the current and the previous)?
}

// stats for the api
type ClientStats struct {
	startTime,
	lastSubmission time.Time
	avgSubmissionDelta uint64 // in ms
	sharesAccepted,
	sharesRejected uint64
	bestDiff,
	hashrate float64
}
type BlockSubmission struct {
	ClientID stratum.ID // for lookup in client map
	Block    *btcutil.Block
}

func (client *StratumClient) Run(noCleanup bool) {
	if !noCleanup {
		defer client.Stop()
	}
	go client.readChanRoutine()
	stratumInited := false
	isAuthed := false
	isSubscribed := false

	/// 15 secs to send the initial stratum message
	client.conn.SetDeadline(time.Now().Add(time.Second * 15))
	reader := bufio.NewReader(client.conn)
	for {
		if isAuthed && isSubscribed && !stratumInited {
			stratumInited = true
			log("===<{blue}%s {cyan}has joined the swarm!>===\n\tid: {blue}%s{cyan}\n\taddr: {white}%s", client.Name(), client.ID, client.Addr())
			/// the initial difficulty was set in CreateClient,
			/// but the client may also suggested a difficulty before
			/// fully initialized
			/// if they haven't, alert them to our default diff
			if client.SuggestedDifficulty == 0 {
				if strings.Contains(client.UserAgent, "cpuminer") {
					client.setDifficulty(0.16)
				} else {
					client.setDifficulty(conf.Pogolo.DefaultDifficulty)
				}
			}
			go client.adjustDiffRoutine()
			client.Stats.startTime = time.Now()
			client.statusChan <- "ready"
		}

		line, err := reader.ReadBytes([]byte("\n")[0])
		if err != nil {
			if err != io.ErrClosedPipe {
				client.error("%s", err)
			}
			return
		}
		client.conn.SetDeadline(time.Now().Add(time.Minute * 5))
		m, err := DecodeStratumMessage(line)
		if err != nil {
			client.error("stratum decode error: %s", err)
			return
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
						return
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
				decoded, err := btcutil.DecodeAddress(split[0], activeChainParams)
				if err != nil {
					client.error("failed decoding address: %s", err)
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
					client.error("couldnt read mining.suggest_difficulty: %s", err)
					break
				}
				suggestedDiff := params.Difficulty.(float64)
				/// only accept a suggested difficulty
				/// if we haven't got one before
				if client.SuggestedDifficulty == 0 &&
					suggestedDiff != client.Difficulty &&
					suggestedDiff > constants.MIN_DIFFICULTY &&
					stratum.ValidDifficulty(suggestedDiff) {
					/// this comment is just for visual spacing
					client.SuggestedDifficulty = suggestedDiff
					client.log("{white}suggested difficulty {green}%g", client.SuggestedDifficulty)
					if err := client.setDifficulty(suggestedDiff); err != nil {
						client.error("failed to adjust difficulty: %s", err)
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
				client.error("unhandled stratum message: %+v", m)
			}
		}
	}
}
func (client *StratumClient) Stop() {
	if client.statusChan == nil {
		return
	}
	log("===<{blue}%s {cyan}has left the swarm!>===", client.Name())
	client.writeChan("done")
	close(client.statusChan)
	client.statusChan = nil
	close(client.templateChan)
	client.templateChan = nil
	client.conn.Close()
}

// aims for the target_share_interval
// MAYBE: change to adjust every 16 or 32 shares?
func (client *StratumClient) adjustDiffRoutine() {
	for {
		time.Sleep(time.Second * time.Duration(conf.Pogolo.DiffAdjustInterval))
		if client.Stats.avgSubmissionDelta == 0 {
			continue
		}
		difference := int64(conf.Pogolo.TargetShareInterval) - int64(client.Stats.avgSubmissionDelta/1000)
		absDifference := math.Abs(float64(difference))
		/// natural variance is +- 1-3s, this adjustment routine seems to consistently
		/// tighten it to +-1s
		if absDifference < 1 {
			continue
		}
		/// cap the adjustment at +-2^12
		delta := float64(0)
		if difference < 0 {
			delta = -delta/2
		} else {
			delta = min(math.Pow(2, absDifference), 4096)
		}

		newDiff := client.Difficulty + delta
		client.log("{white}adjusting share target by {green}%+g{white} to {green}%g", delta, newDiff)
		if err := client.setDifficulty(newDiff); err != nil {
			if errors.Is(err, net.ErrClosed) {
				/// client died and we didnt notice?
				client.Stop()
				return
			}
			client.error("failed to set new diff %s", err)
		}
	}
}

// reads message and job channels
func (client *StratumClient) readChanRoutine() {
	for {
		select {
		// case <-client.statusChan:
		// 	{
		// 		/// eh is this needed?
		// 	}
		case template, ok := <-client.templateChan:
			{
				if !ok {
					/// closed
					return
				}
				client.CurrentJob = client.createJob(DeepCopyTemplate(template))
				err := client.writeNotif(stratum.Notify(client.CurrentJob.NotifyParams))
				if err != nil {
					client.error("error sending job: %s", err)
				}
			}
		}
	}
}

// template channel
func (client *StratumClient) Channel() chan<- *JobTemplate {
	return client.templateChan
}

// status channel
func (client *StratumClient) MsgChannel() <-chan string {
	return client.statusChan
}
func (client *StratumClient) Addr() net.Addr {
	return client.conn.RemoteAddr()
}

// returns the worker name if set and falls back to the id
func (client *StratumClient) Name() string {
	if client.Worker != "" {
		return client.Worker
	}
	return client.ID.String()
}

// live hashrate in MH/s
// FIXME: more accurate averaging?
func (client *StratumClient) calcHashrate(shareTime time.Time) float64 {
	/// "Hashrate = (share difficulty x 2^32) / time" - ben
	/// "2^32 represents the average number of hash attempts needed to find a valid hash at difficulty 1." - skot
	hashrate := (client.Difficulty * 4_294_967_296) / float64(shareTime.Sub(client.Stats.lastSubmission).Seconds())
	hashrate /= 1e6 /// turn into megahashy
	client.Stats.hashrate = (client.Stats.hashrate*127 + hashrate) / 128
	return hashrate
}
func (client *StratumClient) setDifficulty(newDiff float64) error {
	if newDiff == client.Difficulty {
		return nil
	}
	if err := client.writeNotif(stratum.SetDifficulty(newDiff)); err != nil {
		return err
	}
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
			s := BlockSubmission{
				ClientID: client.ID,
				Block:    btcutil.NewBlock(updatedBlock),
			}
			client.submitBlock(s)
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
		client.calcHashrate(now)
		client.Stats.lastSubmission = now
		// client.log(fmt.Sprintf("share accepted: diff %s", diffFormat(shareDiff)))
		client.log("diff {blue}%s{reset} (best: {blue}%s{reset}), avg submission delta {cyan}%ds", diffFormat(shareDiff), diffFormat(client.Stats.bestDiff), client.Stats.avgSubmissionDelta/1000)
		client.log("{white}hashrate: %s", formatHashrate(client.Stats.hashrate))
		client.writeRes(stratum.BooleanResponse(m.MessageID, true))
	} else {
		client.Stats.sharesRejected++
		client.writeRes(stratum.NewErrorResponse(m.MessageID, constants.ERROR_DIFF_TOO_LOW))
		// client.log(fmt.Sprintf("share rejected: diff %f", shareDiff))

		// hdr := bytes.NewBuffer([]byte{})
		// updatedBlock.Header.Serialize(hdr)
		// println("share:", fmt.Sprintf("%+v", s))
		// println(fmt.Sprintf("header: %x", hdr))
		// println(fmt.Sprintf("diff: %f", shareDiff))
	}
}
func (client *StratumClient) createJob(template *JobTemplate) MiningJob {
	blockHeader := template.Block.MsgBlock().Header

	merkleBranches := make([][]byte, len(template.MerkleBranch))
	for i, branch := range template.MerkleBranch {
		merkleBranches[i] = branch[:]
	}

	coinbaseTx := CreateCoinbaseTx(client.User, *template, activeChainParams)
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

// for testing
func (client *StratumClient) CreateJob(template *JobTemplate) MiningJob {
	return client.createJob(template)
}

// TODO: how to get errors back?
func (client *StratumClient) submitBlock(block BlockSubmission) {
	client.submissionChan <- block
	client.log("block submitted")
}
func (client *StratumClient) writeRes(res stratum.Response) error {
	bytes, err := res.Marshal()
	if err != nil {
		client.error("failed to marshal response: %s", err)
		return err
	}

	return client.writeConn(bytes)
}
func (client *StratumClient) writeNotif(n stratum.Notification) error {
	bytes, err := n.Marshal()
	if err != nil {
		client.error("failed to marshal notification: %s", err)
		return err
	}

	return client.writeConn(bytes)
}
func (client *StratumClient) writeConn(b []byte) error {
	_, err := client.conn.Write(b)
	return err
}

// pass a message back to the server
func (client *StratumClient) writeChan(msg string) {
	client.statusChan <- msg
}
func (client *StratumClient) log(s string, a ...any) {
	s = fmt.Sprintf(s, a...)
	fmt.Println(oigiki.ProcessTags("[{cyan}%s{reset}] %s", client.Name(), s))
}
func (client *StratumClient) error(s string, a ...any) {
	s = fmt.Sprintf(s, a...)
	println(oigiki.ProcessTags("[{cyan}%s{reset}] {red}%s", client.Name(), s))
}

func CreateClient(conn net.Conn, submissionChannel chan<- BlockSubmission) StratumClient {
	client := StratumClient{
		conn:           conn,
		ID:             ClientIDHash(),
		statusChan:     make(chan string),
		templateChan:   make(chan *JobTemplate),
		submissionChan: submissionChannel,
		Difficulty:     1,
	}
	return client
}
