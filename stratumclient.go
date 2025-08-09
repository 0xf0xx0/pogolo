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
	SuggestedDifficulty float64 // TODO: bool?
	VersionRolling      bool
	VersionRollingMask  uint32
	// internal
	conn           net.Conn
	statusChan     chan string // messages to/from client
	templateChan   chan *JobTemplate
	submissionChan chan<- BlockSubmission
	CurrentJob     MiningJob
	stats          *ClientStats // TODO: embed?
}

// stats for the api
type ClientStats struct {
	startTime, // time the client subscribes
	lastTimeSlot, // used for hashrate calc
	currTimeSlot, // used for hashrate calc
	lastSubmission time.Time // used for calcing delta between `mining.submit`s
	avgSubmissionDelta uint64 // in ms
	sharesAccepted,
	sharesRejected,
	lastAccShareDiff, // accumulated difficulty , used for hashrate calc
	currAccShareDiff uint64 // accumulated difficulty , used for hashrate calc
	bestDiff, // session
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
			client.stats.startTime = time.Now()
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
		if client.stats.avgSubmissionDelta == 0 {
			continue
		}
		difference := int64(conf.Pogolo.TargetShareInterval) - int64(client.stats.avgSubmissionDelta/1000)
		absDifference := math.Abs(float64(difference))
		/// natural variance is +- 1-3s, this adjustment routine seems to consistently
		/// tighten it to +-1s
		if absDifference <= 1 {
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

		newDiff := max(client.Difficulty+delta, constants.MIN_DIFFICULTY)
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
		template, ok := <-client.templateChan
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
		client.stats.sharesRejected++
		client.writeRes(stratum.NewErrorResponse(m.MessageID, constants.ERROR_UNK_JOB))
		return
	}

	updatedBlock, err := client.CurrentJob.Template.UpdateBlock(client, s, client.CurrentJob.NotifyParams)
	if err != nil {
		client.writeRes(stratum.NewErrorResponse(m.MessageID, constants.ERROR_INTERNAL))
		return
	}

	shareDiff := CalcDifficulty(updatedBlock.Header)
	if shareDiff >= client.Difficulty {
		if shareDiff >= client.CurrentJob.Template.NetworkDiff {
			s := BlockSubmission{
				ClientID: client.ID,
				Block:    btcutil.NewBlock(updatedBlock),
			}
			client.submitBlock(s)
		}
		client.stats.sharesAccepted++
		if shareDiff > client.stats.bestDiff {
			client.stats.bestDiff = shareDiff
			client.log("new best session diff!")
		}
		client.updateStats()
		client.log("diff {blue}%s{reset} (best: {blue}%s{reset}), avg submission delta {cyan}%ds", diffFormat(shareDiff), diffFormat(client.stats.bestDiff), client.stats.avgSubmissionDelta/1000)
		client.log("{white}hashrate: %s", formatHashrate(client.stats.hashrate))
		client.writeRes(stratum.BooleanResponse(m.MessageID, true))
	} else {
		client.stats.sharesRejected++
		client.writeRes(stratum.NewErrorResponse(m.MessageID, constants.ERROR_DIFF_TOO_LOW))
	}
}

// MAYBE: move to ClientStats?
func (client *StratumClient) updateStats() {

	now := time.Now()
	if client.stats.lastSubmission.Unix() > 0 {
		/// rolling avg
		/// wikipedia my beloved
		/// https://en.wikipedia.org/wiki/Moving_average#Cumulative_average
		submission := uint64(now.Sub(client.stats.lastSubmission).Milliseconds())
		client.stats.lastSubmission = now
		/// start the avg calc with the furst delta, not 0
		if client.stats.avgSubmissionDelta == 0 {
			client.stats.avgSubmissionDelta = submission
		} else {
			client.stats.avgSubmissionDelta =
				((client.stats.avgSubmissionDelta * (constants.SUBMISSION_DELTA_WINDOW - 1)) + submission) / constants.SUBMISSION_DELTA_WINDOW
		}
	}

	client.stats.calcHashrate(now, client.Difficulty)
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

	job := MiningJob{
		Template: template,
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
	log(oigiki.ProcessTags("[{blue}%s{cyan}]{reset} %s", client.Name(), s))
}
func (client *StratumClient) error(s string, a ...any) {
	s = fmt.Sprintf(s, a...)
	logError(oigiki.ProcessTags("[{blue}%s{red}] %s", client.Name(), s))
}

// stats

func (stats *ClientStats) Uptime() time.Duration {
	return time.Now().Sub(stats.startTime)
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
	coeff := int64(600) // 10 mins
	timeSlot := time.Unix((shareTime.Unix()/coeff)*coeff, 0)
	/// if the current time slot doesnt exist, make it (and set the last as the init time)
	if stats.currTimeSlot.Unix() <= 0 {
		stats.currTimeSlot = timeSlot
		stats.lastTimeSlot = stats.startTime
		/// if we're in the next chunk of time, snapshot the curr* and move over
	} else if stats.currTimeSlot.Unix() != timeSlot.Unix() {
		stats.lastAccShareDiff = stats.currAccShareDiff
		stats.lastTimeSlot = stats.currTimeSlot

		stats.currAccShareDiff = uint64(currTargetDiff)
		stats.currTimeSlot = timeSlot
		/// otherwise just update stats
	} else {
		/// we wanna use the target difficulty for a stable number
		/// TODO: pass in?
		stats.currAccShareDiff += uint64(currTargetDiff)
		if stats.currAccShareDiff > 0 {
			/// "Hashrate = (share difficulty x 2^32) / time" - ben
			/// "2^32 represents the average number of hash attempts needed to find a valid hash at difficulty 1." - skot
			time := float64(shareTime.Sub(stats.lastTimeSlot).Seconds())
			stats.hashrate = float64((stats.lastAccShareDiff+stats.currAccShareDiff)*4_294_967_296) / time
			stats.hashrate /= 1e6 /// turn into megahashy
		}
	}
}

func CreateClient(conn net.Conn, submissionChannel chan<- BlockSubmission) StratumClient {
	client := StratumClient{
		ID:             ClientIDHash(),
		Difficulty:     1,
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
