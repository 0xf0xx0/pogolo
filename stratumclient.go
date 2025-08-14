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
	TargetDiff          float64
	SuggestedDifficulty float64 // TODO: float32?
	VersionRollingMask  uint32
	// internal
	conn           net.Conn
	statusChan     chan string // messages to server
	templateChan   chan *JobTemplate
	submissionChan chan<- BlockSubmission
	CurrentJob     MiningJob
	stats          *ClientStats // TODO: embed?
}

// used for hashrate calc
type timeSlot struct {
	time    time.Time
	accDiff uint64 // accumulated difficulty, used for hashrate calc
}

type BlockSubmission struct {
	ClientID stratum.ID // for lookup in client map
	Block    *btcutil.Block
}

func (client *StratumClient) Run(noCleanup bool) {
	if !noCleanup {
		defer client.Stop()
	}
	go client.readJobChanRoutine()
	stratumInited := false
	isAuthed := false
	isSubscribed := false

	/// 15 secs to send the initial stratum message
	client.conn.SetDeadline(time.Now().Add(time.Second * 15))
	reader := bufio.NewReader(client.conn)
	for {
		if isAuthed && isSubscribed && !stratumInited {
			stratumInited = true
			log("===<{blue}%s{/blue} has joined the swarm!>===\n\tid: {blue}%s{cyan}\n\taddr: {white}%s", client.Name(), client.ID, client.Addr())
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
			client.stats.startTime = time.Now()
			client.writeChan("ready")
		}

		line, err := reader.ReadBytes(byte('\n'))

		switch err {
		case nil:
		case io.ErrClosedPipe:
			fallthrough
		case io.EOF:
		default:
			client.error("%s", err)
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
						/// hardcoded??? bitaxe-only???
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
					return
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
				client.UserAgent = parseUserAgent(subParams.UserAgent)
				if client.UserAgent == "luckyminer" {
					/// unsupported
					client.writeRes(stratum.NewErrorResponse(m.MessageID, constants.ERROR_NOT_ACCEPTED))
					return
				}
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
					suggestedDiff != client.TargetDiff &&
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
	client.writeChan("done")
	close(client.statusChan)
	client.statusChan = nil
	close(client.templateChan)
	client.templateChan = nil
	client.conn.Close()
	log("===<{blue}%s{/blue} has left the swarm!>===", client.Name())
}

// aims for the target_share_interval
// MAYBE: change to adjust every 16 or 32 shares?
func (client *StratumClient) adjustDiffRoutine() {
	for {
		time.Sleep(time.Second * time.Duration(conf.Pogolo.DiffAdjustInterval))
		if client.stats.avgSubmissionDelta == 0 {
			continue
		}
		difference := int64(conf.Pogolo.TargetShareInterval) - int64(client.stats.avgSubmissionDelta/1000)
		absDifference := math.Abs(float64(difference))
		/// natural variance is +- 1-3s, this adjustment routine seems to consistently
		/// tighten it to +-1s
		if absDifference < 2 {
			continue
		}
		/// cap the adjustment at +-2^12
		delta := min(math.Pow(2, absDifference), 4096)
		if difference < 0 {
			delta = -delta / 2 /// TODO: nearest power of 2 calc
		}
		if delta == 0 {
			continue
		}

		newDiff := max(client.TargetDiff+delta, constants.MIN_DIFFICULTY)
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

// reads job channel
func (client *StratumClient) readJobChanRoutine() {
	for {
		template, ok := <-client.templateChan
		if !ok {
			/// closed
			return
		}
		client.CurrentJob = client.createJob(template)
		err := client.writeNotif(stratum.Notify(client.CurrentJob.NotifyParams))
		if err != nil {
			client.error("error sending job: %s", err)
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
func (client *StratumClient) setDifficulty(newDiff float64) error {
	if newDiff == client.TargetDiff {
		return nil
	}
	if err := client.writeNotif(stratum.SetDifficulty(newDiff)); err != nil {
		return err
	}
	client.TargetDiff = newDiff
	return nil
}

func (client *StratumClient) validateShareSubmission(s stratum.Share, m *stratum.Request) {
	if s.JobID != client.CurrentJob.NotifyParams.JobID {
		client.stats.sharesRejected++
		client.writeRes(stratum.NewErrorResponse(m.MessageID, constants.ERROR_UNK_JOB))
		return
	}

	updatedBlock, err := client.CurrentJob.UpdateBlock(client, s, client.CurrentJob.NotifyParams)
	if err != nil {
		client.writeRes(stratum.NewErrorResponse(m.MessageID, constants.ERROR_INTERNAL))
		return
	}

	shareDiff := CalcDifficulty(updatedBlock.Header)
	if shareDiff >= client.TargetDiff {
		if shareDiff >= client.CurrentJob.NetworkDiff {
			s := BlockSubmission{
				ClientID: client.ID,
				Block:    btcutil.NewBlock(updatedBlock),
			}
			client.submitBlock(s)
		}
		client.stats.sharesAccepted++
		if shareDiff > client.stats.bestDiff {
			client.stats.bestDiff = shareDiff
			client.log("{green}new best session diff!")
		}
		client.stats.update(client.TargetDiff)
		client.log("diff {blue}%s{reset} of {blue}%s{reset} (best: {blue}%s{reset})", diffFormat(shareDiff), diffFormat(client.TargetDiff), diffFormat(client.stats.bestDiff))
		client.log("{white}%s, avg submit delta: %ds", formatHashrate(client.stats.hashrate), client.stats.avgSubmissionDelta/1000)
		client.writeRes(stratum.BooleanResponse(m.MessageID, true))
	} else {
		client.stats.sharesRejected++
		client.writeRes(stratum.NewErrorResponse(m.MessageID, constants.ERROR_DIFF_TOO_LOW))
	}
}
func (client *StratumClient) createJob(template *JobTemplate) MiningJob {
	block := btcutil.NewBlock(template.Block.MsgBlock().Copy())
	blockHeader := block.MsgBlock().Header

	merkleBranches := make([][]byte, len(template.MerkleBranch))
	for i, branch := range template.MerkleBranch {
		merkleBranches[i] = branch[:]
	}

	coinbaseTx := CreateCoinbaseTx(client.User, block, template.Subsidy, activeChainParams)
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

	job := MiningJob{
		NetworkDiff:  template.NetworkDiff,
		Block:        block,
		MerkleBranch: template.MerkleBranch,
		NotifyParams: stratum.NotifyParams{
			JobID:          template.ID,
			PrevBlockHash:  &blockHeader.PrevBlock,
			MerkleBranches: merkleBranches,
			Version:        uint32(blockHeader.Version),
			Bits:           template.Bits,
			Timestamp:      blockHeader.Timestamp,
			/// minus 8 cause we wanna lop off the extranonce padding
			/// TODO: variable extranonce2
			CoinbasePart1: serializedCoinbaseTx[:partOneIndex-8],
			CoinbasePart2: serializedCoinbaseTx[partOneIndex:],
			Clean:         true, /// we don't support multiple active jobs
		},
	}

	return job
}

// for testing
func (client *StratumClient) CreateJob(template *JobTemplate) MiningJob {
	return client.createJob(template)
}

// chatter
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
func (client *StratumClient) writeChan(msg string) {
	client.statusChan <- msg
}

// logging
func (client *StratumClient) log(s string, a ...any) {
	s = fmt.Sprintf(s, a...)
	log(oigiki.ProcessTags(fmt.Sprintf("[{blue}%s{cyan}]{reset} %s", client.Name(), s)))
}
func (client *StratumClient) error(s string, a ...any) {
	s = fmt.Sprintf(s, a...)
	logError(oigiki.ProcessTags(fmt.Sprintf("[{blue}%s{red}] %s", client.Name(), s)))
}

// stats for the api
type ClientStats struct {
	lastTimeSlot,
	currTimeSlot timeSlot
	startTime, // time the client subscribes
	lastSubmission time.Time // used for calcing delta between `mining.submit`s
	avgSubmissionDelta uint64 // in ms
	sharesAccepted,
	sharesRejected,
	bestDiff, // session
	hashrate float64
}

func (stats *ClientStats) update(currTargetDiff float64) {
	now := time.Now()
	if stats.lastSubmission.Unix() > 0 {
		/// rolling avg
		/// wikipedia my beloved
		/// https://en.wikipedia.org/wiki/Moving_average#Cumulative_average
		submission := uint64(now.Sub(stats.lastSubmission).Milliseconds())
		/// start the avg calc with the furst delta, not 0
		if stats.avgSubmissionDelta == 0 {
			stats.avgSubmissionDelta = submission
		} else {
			stats.avgSubmissionDelta =
				((stats.avgSubmissionDelta * (constants.SUBMISSION_DELTA_WINDOW - 1)) + submission) / constants.SUBMISSION_DELTA_WINDOW
		}
	}

	stats.lastSubmission = now
	stats.calcHashrate(now, currTargetDiff)
}

// getters
func (stats *ClientStats) Uptime() uint64 {
	return uint64(time.Now().Sub(stats.startTime).Milliseconds())
}
func (stats *ClientStats) BestDiff() float64 {
	return stats.bestDiff
}
func (stats *ClientStats) Hashrate() float64 {
	return stats.hashrate
}

// live hashrate in MH/s
func (stats *ClientStats) calcHashrate(shareTime time.Time, currTargetDiff float64) {
	/// calc copied from public-pool
	timeSlot := time.Unix((shareTime.Unix()/constants.HASHRATE_WINDOW)*constants.HASHRATE_WINDOW, 0)
	/// first call, make the current slot (and set the last as the init time)
	if stats.currTimeSlot.time.Unix() <= 0 {
		stats.currTimeSlot.time = timeSlot
		stats.lastTimeSlot.time = stats.startTime
		/// if we're in the next chunk of time, snapshot the curr* and move over
	} else if stats.currTimeSlot.time.Unix() != timeSlot.Unix() {
		stats.lastTimeSlot = stats.currTimeSlot

		stats.currTimeSlot.accDiff = uint64(currTargetDiff)
		stats.currTimeSlot.time = timeSlot
		/// otherwise just update stats
	} else {
		/// we wanna use the target difficulty for a stable number
		/// TODO: pass in?
		stats.currTimeSlot.accDiff += uint64(currTargetDiff)
		if stats.currTimeSlot.accDiff > 0 {
			/// "Hashrate = (share difficulty x 2^32) / time" - ben
			/// "2^32 represents the average number of hash attempts needed to find a valid hash at difficulty 1." - skot
			time := float64(shareTime.Sub(stats.lastTimeSlot.time).Seconds())
			stats.hashrate = float64((stats.lastTimeSlot.accDiff+stats.currTimeSlot.accDiff)*4_294_967_296) / time
			stats.hashrate /= 1e6 /// turn into megahashy
		}
	}
}

func CreateClient(conn net.Conn, submissionChannel chan<- BlockSubmission) StratumClient {
	client := StratumClient{
		ID:             ClientIDHash(),
		TargetDiff:     1,
		stats:          &ClientStats{},
		conn:           conn,
		statusChan:     make(chan string),
		templateChan:   make(chan *JobTemplate),
		submissionChan: submissionChannel,
	}
	return client
}

func parseUserAgent(ua string) string {
	ua = strings.ToLower(ua)

	if strings.Contains(ua, "axe") {
		split := strings.Split(ua, "/")
		if len(split) != 3 {
			/// confusion
			return ua
		}
		/// format is bitaxe/<chip>/<fw_version>, drop the version
		return strings.Join(split[:2], "/")
	} else if strings.Contains(ua, "luckyminer") {
		return "luckyminer"
	}
	/// copy public-pools parsing
	ua = strings.Split(ua, " ")[0]
	ua = strings.Split(ua, "/")[0]
	ua = strings.Split(ua, "v")[0]
	ua = strings.Split(ua, "-")[0]
	return ua
}
