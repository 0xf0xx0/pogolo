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
	"strconv"
	"strings"
	"time"

	"github.com/0xf0xx0/stratum"
	"github.com/btcsuite/btcd/btcutil"
)

// aka gopher
type StratumClient struct {
	ID                  stratum.ID
	User                btcutil.Address
	Nickname            string
	Password            string
	UserAgent           string
	TargetDifficulty    float64
	SuggestedDifficulty float64 // overloaded, initially set by client (optional) then used by diff adjust
	VersionRollingMask  uint32
	// internal
	conn           net.Conn
	statusChan     chan string // messages to server
	templateChan   chan *JobTemplate
	submissionChan chan<- blockSubmission
	CurrentJob     MiningJob // TODO: store the previous temporarily when switching?
	stats          *ClientStats
}

// used for hashrate calc
type timeSlot struct {
	time.Time
	accDiff uint64 // accumulated difficulty, used for hashrate calc
}

type blockSubmission struct {
	ClientID stratum.ID // for lookup in client map
	Block    btcutil.Block
	Share    stratum.Share
}

func (client *StratumClient) Run(noCleanup bool) {
	if !noCleanup {
		defer client.Stop()
	}
	go client.readJobChanRoutine()
	stratumInited := false
	isAuthed := false
	isSubscribed := false

	/// 5 secs to send the initial stratum message
	client.conn.SetDeadline(time.Now().Add(time.Second * 5))
	reader := bufio.NewReader(client.conn)
	for {
		/// we only send work after authed and subbed (and set a flag so we dont do this again)
		if isAuthed && isSubscribed && !stratumInited {
			stratumInited = true
			log(fmt.Sprintf(
				/// dig, cause gophers, get it?
				"==<<>>=<<>>=<{green}%s{/green} has joined the dig!>=<<>>=<<>>==\n\tid: {green}%s{/green}\n\taddr: {green}%s",
				client.Name(), client.ID, client.Addr(),
			))
			/// the client may have suggested a difficulty before
			/// fully initialized
			/// if they haven't, we alert them to our default diff here
			if client.SuggestedDifficulty == 0 {
				if strings.Contains(client.UserAgent, "cpuminer") {
					client.setDifficulty(constants.DEFAULT_DIFFICULTY_LOW_POWER)
				} else {
					client.setDifficulty(conf.Pogolo.DefaultDifficulty)
				}
			}
			if defaultMiningAddr != nil && client.User.EncodeAddress() == (*defaultMiningAddr).EncodeAddress() {
				client.log("{yellow}mining to pool address")
			}
			if client.VersionRollingMask > 0 {
				client.log("version rolling enabled! mask: {blue}%#x", client.VersionRollingMask)
			}
			client.stats.startTime = time.Now()
			client.writeChan("ready")
		}

		/// messages are newline separated (either lf or crlf)
		line, err := reader.ReadBytes(byte('\n'))

		switch err {
		case nil:
		case io.ErrClosedPipe:
			fallthrough
		case io.EOF:
			return
		default:
			client.logError("%s", err)
			return
		}
		client.conn.SetDeadline(time.Now().Add(time.Minute * 3))
		/// TODO: add stratum log option
		//client.log("{blackbright}> %#q", line)

		/// we dont need the trailing newline
		line = bytes.Trim(line, "\n\r ")

		/// process the message
		m, err := DecodeStratumMessage(line)
		if err != nil {
			client.logError("stratum decode error: %s", err)
			return
		}

		switch m.GetMethod() {
		case stratum.MiningSubmit:
			{
				if !stratumInited {
					client.logError("submit before subscribe")
					client.writeRes(stratum.NewErrorResponse(m.MessageID, constants.ERROR_NOT_SUBBED))
					return
				}
				s := stratum.Share{}
				if err := s.Read(m); err != nil {
					client.logError("error processing %s: %s", m.Method, err)
					client.writeRes(stratum.NewErrorResponse(m.MessageID, constants.ERROR_UNPROCESSABLE))
					break
				}
				client.validateShareSubmission(s, m)
			}
		case stratum.MiningConfigure:
			{
				params := stratum.ConfigureParams{}
				params.Read(m)
				if err := params.Read(m); err != nil {
					client.logError("error processing %s: %s", m.Method, err)
					client.writeRes(stratum.NewErrorResponse(m.MessageID, constants.ERROR_UNPROCESSABLE))
					break
				}
				res := stratum.ConfigureResult{}
				if slices.Contains(params.Supported, "version-rolling") {
					if rawMask, ok := params.Parameters["version-rolling.mask"]; ok {
						mask, err := strconv.ParseUint(rawMask.(string), 16, 32)
						if err != nil {
							client.logError("couldnt parse version rolling mask %v", rawMask)
							client.writeRes(stratum.NewErrorResponse(m.MessageID, constants.ERROR_UNPROCESSABLE))
							return
						}
						/// bip-310
						client.VersionRollingMask = uint32(mask) & constants.VERSION_ROLLING_MASK

						err = res.Add(stratum.VersionRollingConfigurationResult{Accepted: true, Mask: client.VersionRollingMask})
						if err != nil {
							/// uhhhhhhhhhhhhhhhh
							/// honestly just leave this as a panic
							panic(err)
						}
					} else {
						/// *uhhhhhhhhhhhhhhhh*
						client.logError("couldnt read version rolling mask? shouldnt happen i *think*")
						client.writeRes(stratum.NewErrorResponse(m.MessageID, constants.ERROR_UNPROCESSABLE))
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
				if err := params.Read(m); err != nil {
					client.logError("error processing %s: %s", m.Method, err)
					client.writeRes(stratum.NewErrorResponse(m.MessageID, constants.ERROR_UNPROCESSABLE))
					break
				}
				if conf.Pogolo.Password != "" && params.Password != conf.Pogolo.Password {
					client.logError("invalid password")
					client.writeRes(stratum.NewErrorResponse(m.MessageID, constants.ERROR_UNAUTHORIZED))
					return
				}
				decoded, err := btcutil.DecodeAddress(params.Username, activeChainParams)
				if err != nil {
					if defaultMiningAddr == nil {
						client.logError("failed decoding address: %s", err)
						client.writeRes(stratum.NewErrorResponse(m.MessageID, constants.ERROR_UNPROCESSABLE))
						return
					}
					/// assume just the workername was passed
					if params.Username != "" {
						params.Worker = params.Username
					}
					decoded = *defaultMiningAddr
				}
				client.User = decoded
				client.Nickname = params.Worker
				client.Password = params.Password
				client.writeRes(stratum.AuthorizeResponse(m.MessageID, true))
				isAuthed = true
			}
		case stratum.MiningSubscribe:
			{
				if isSubscribed {
					break
				}
				params := stratum.SubscribeParams{}
				params.Read(m)
				if err := params.Read(m); err != nil {
					client.logError("error processing %s: %s", m.Method, err)
					client.writeRes(stratum.NewErrorResponse(m.MessageID, constants.ERROR_UNPROCESSABLE))
					break
				}
				client.UserAgent = parseUserAgent(params.UserAgent)
				if client.UserAgent == "luckyminer" {
					/// unsupported
					client.writeRes(stratum.NewErrorResponse(m.MessageID, constants.ERROR_NOT_ACCEPTED))
					return
				}
				responseParams := stratum.SubscribeResult{
					Subscriptions: []stratum.Subscription{
						{
							Method:    stratum.MiningNotify,
							SessionID: client.ID,
						},
					},
					ExtraNonce1:     client.ID,
					ExtraNonce2Size: uint32(conf.Pogolo.ExtraNonce2Size),
				}
				client.writeRes(stratum.SubscribeResponse(m.MessageID, responseParams))
				isSubscribed = true
			}
		case stratum.MiningSuggestDifficulty:
			{
				/// only accept a suggested difficulty if we haven't got one before
				if conf.Pogolo.IgnoreSuggDiff || client.SuggestedDifficulty > 0 {
					client.writeRes(stratum.NewErrorResponse(m.MessageID, constants.ERROR_NOT_ACCEPTED))
					break
				}

				params := stratum.SuggestDifficultyParams{}
				if err := params.Read(m); err != nil {
					client.logError("error processing %s: %s", m.Method, err)
					client.writeRes(stratum.NewErrorResponse(m.MessageID, constants.ERROR_UNPROCESSABLE))
					break
				}
				suggestedDiff := math.Abs(params.Difficulty)
				if suggestedDiff != client.TargetDifficulty && suggestedDiff > constants.MIN_DIFFICULTY {
					/// this comment is just for visual spacing
					client.SuggestedDifficulty = suggestedDiff
					client.log("suggested difficulty {blue}%g", suggestedDiff)
					client.writeRes(stratum.NewBooleanResponse(m.MessageID, true))
				} else {
					client.logError("rejected suggested difficulty")
					client.writeRes(stratum.NewErrorResponse(m.MessageID, constants.ERROR_NOT_ACCEPTED))
				}
			}
		case stratum.MiningExtranonceSubscribe:
			{
				client.writeRes(stratum.NewErrorResponse(m.MessageID, constants.ERROR_UNSUPP_METHOD))
			}
		default:
			{
				client.writeRes(stratum.NewErrorResponse(m.MessageID, constants.ERROR_UNK_METHOD))
				client.logError("unknown stratum message: %+v", m)
			}
		}
	}
}
func (client *StratumClient) Stop() {
	if client.statusChan == nil {
		return
	}
	close(client.statusChan)
	close(client.templateChan)
	/// nil because receive-side closure
	client.statusChan = nil
	client.templateChan = nil
	client.conn.Close()
	log(fmt.Sprintf("==<<>>=<<>>=<{green}%s{/green} has left the dig!>=<<>>=<<>>==", client.Name()))
}

// aims for the target_share_interval
// and attempts to queue an adjustment every `constants.SUBMISSION_DELTA_WINDOW`
func (client *StratumClient) adjustDiffRoutine() {
	if client.stats.avgSubmissionDelta == 0 {
		return
	}
	/// negative = running slow, positive = running fast
	difference := float64(conf.Pogolo.TargetShareInterval) - float64(client.stats.avgSubmissionDelta/1000)
	absDifference := math.Abs(difference)
	/// natural variance is +- 1-3s, this adjustment routine seems to consistently
	/// tighten it to +-1s
	if absDifference < 2 {
		return
	}
	/// cap the adjustment at +-256
	delta := min(math.Pow(2, absDifference), 256)
	if difference < 0 {
		delta = -delta / 2 /// we want to be more conservative when adjusting downwards
	}

	/// FIXME: this assumes the adjustments will happen less often than jobs, is that a problem?
	newDiff := max(client.TargetDifficulty+delta, constants.MIN_DIFFICULTY)
	client.SuggestedDifficulty = newDiff
	client.log("queued diff adjustment by {blue}%+g{/blue} to {blue}%g", delta, client.SuggestedDifficulty)
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
		/// adjusted by vardiff
		/// stratum spec applied diff changes to next job, so announce changes before announcing job
		if client.SuggestedDifficulty > 0 && client.SuggestedDifficulty != client.TargetDifficulty {
			if err := client.setDifficulty(client.SuggestedDifficulty); err != nil {
				if errors.Is(err, net.ErrClosed) {
					/// client died and we didnt notice?
					client.Stop()
					return
				}
			}
			client.log("adjusting share target to {blue}%g", client.SuggestedDifficulty)
		}
		err := client.writeNotif(stratum.Notify(client.CurrentJob.NotifyParams))
		if err != nil {
			client.logError("error sending job: %s", err)
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

// returns the nickname if set and falls back to the id
func (client *StratumClient) Name() string {
	if client.Nickname != "" {
		return client.Nickname
	}
	return client.ID.String()
}
func (client *StratumClient) setDifficulty(newDiff float64) error {
	if newDiff == client.TargetDifficulty {
		return nil
	}
	if err := client.writeNotif(stratum.SetDifficulty(newDiff)); err != nil {
		return err
	}
	client.TargetDifficulty = newDiff
	return nil
}

func (client *StratumClient) validateShareSubmission(share stratum.Share, m *stratum.Request) {
	if share.JobID != client.CurrentJob.NotifyParams.JobID {
		client.stats.sharesRejected++
		client.logError("share rejected: unknown job")
		client.writeRes(stratum.NewErrorResponse(m.MessageID, constants.ERROR_UNK_JOB))
		return
	}

	/// we'll only verify the difficulty
	/// the backing node will do the full block validation, we only care if the
	/// submission was high enough
	updatedBlock, err := client.CurrentJob.UpdateBlock(client, share, client.CurrentJob.NotifyParams)
	if err != nil {
		client.logError(err.Error())
		client.writeRes(stratum.NewErrorResponse(m.MessageID, constants.ERROR_UNPROCESSABLE))
		return
	}

	shareDiff := CalcDifficulty(updatedBlock.Header)
	if shareDiff >= client.TargetDifficulty {
		if shareDiff >= client.CurrentJob.NetworkDiff {
			/// !!! block! dont say ANYTHING until after submitted
			submission := blockSubmission{
				ClientID: client.ID,
				Block:    *btcutil.NewBlock(updatedBlock),
				Share:    share,
			}

			client.submitBlock(submission)
			client.log("{yellow}block candidate submitted")
		}
		client.writeRes(stratum.NewBooleanResponse(m.MessageID, true))

		if shareDiff > client.stats.bestDiff {
			client.stats.bestDiff = shareDiff
			client.log("{green}new best session diff!")
		}
		client.stats.sharesAccepted++

		/// update with the target diff for a more accurate estimation
		client.stats.update(client.TargetDifficulty)
		client.log("diff {blue}%s{/blue} of {blue}%s{/blue} (best: {bluebright}%s{/bluebright})\n\t{blackbright}%s, avg submit delta: %ds",
			DiffFormat(shareDiff), DiffFormat(client.TargetDifficulty), DiffFormat(client.stats.bestDiff),
			FormatHashrate(client.stats.HashrateMH()), client.stats.avgSubmissionDelta/1000)
	} else {
		client.writeRes(stratum.NewErrorResponse(m.MessageID, constants.ERROR_LOW_DIFF))
		client.stats.sharesRejected++
		client.logError("share rejected: diff too low (%.5g/%g)", shareDiff, client.TargetDifficulty)
	}

	if !conf.Pogolo.DisableVarDiff && (client.stats.sharesAccepted+client.stats.sharesRejected)%constants.SUBMISSION_DELTA_WINDOW == 0 {
		client.adjustDiffRoutine()
	}
}
func (client *StratumClient) createJob(template *JobTemplate) MiningJob {
	block := btcutil.NewBlock(template.MsgBlock.Copy())
	blockHeader := block.MsgBlock().Header

	merkleBranches := make([][]byte, len(template.MerkleBranch))
	for i, branch := range template.MerkleBranch {
		merkleBranches[i] = branch[:]
	}

	coinbaseTx := FillCoinbaseTx(client.User, block, template.Subsidy, activeChainParams)
	/// serialized without the witness, we handle that on submission
	serializedCoinbaseTx, err := SerializeTx(coinbaseTx.MsgTx(), false)
	if err != nil {
		panic(err)
	}
	inputScript := coinbaseTx.MsgTx().TxIn[0].SignatureScript
	partOneIndex := bytes.Index(serializedCoinbaseTx, inputScript)
	if partOneIndex < 0 {
		panic("partOneIndex shoudnt be below 0")
	}
	partOneIndex += len(inputScript)

	job := MiningJob{
		NetworkDiff:  template.NetworkDiff,
		Block:        *block,
		Version:      block.MsgBlock().Header.Version,
		MerkleBranch: template.MerkleBranch,
		NotifyParams: stratum.NotifyParams{
			JobID:          template.ID,
			PrevBlockHash:  &blockHeader.PrevBlock,
			MerkleBranches: merkleBranches,
			Version:        uint32(blockHeader.Version),
			Bits:           template.Bits,
			Timestamp:      blockHeader.Timestamp,
			/// we wanna lop off the extranonce padding
			CoinbasePart1: serializedCoinbaseTx[:partOneIndex-int(constants.EXTRANONCE_SIZE+conf.Pogolo.ExtraNonce2Size)],
			CoinbasePart2: serializedCoinbaseTx[partOneIndex:],
			Clean:         true, /// we don't support multiple active jobs
		},
	}

	return job
}

// chatter
func (client *StratumClient) submitBlock(block blockSubmission) {
	client.submissionChan <- block
}
func (client *StratumClient) writeRes(res stratum.Response) error {
	bytes, err := res.Marshal()
	if err != nil {
		client.logError("failed to marshal response: %s", err)
		return err
	}

	return client.writeConn(bytes)
}
func (client *StratumClient) writeNotif(n stratum.Notification) error {
	bytes, err := n.Marshal()
	if err != nil {
		client.logError("failed to marshal notification: %s", err)
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
// maybe: pick random color for client?
func (client *StratumClient) log(s string, a ...any) {
	s = fmt.Sprintf(s, a...)
	/// MAYBE: move prefix to StratumClient?
	log("[{green}" + client.Name() + "{/green}]{cyan} " + s)
}
func (client *StratumClient) logError(s string, a ...any) {
	s = fmt.Sprintf(s, a...)
	logError("[{green}" + client.Name() + "{/green}] " + s)
}

// stats for the api
type ClientStats struct {
	lastTimeSlot,
	currTimeSlot timeSlot
	startTime, // time the client subscribed
	lastSubmission time.Time // used for calcing delta between `mining.submit`s
	avgSubmissionDelta uint64 // in ms
	sharesAccepted,
	sharesRejected uint64
	bestDiff, // session
	hashrate float64
}

func (stats *ClientStats) update(currTargetDiff float64) {
	now := time.Now()
	if stats.lastSubmission.Unix() > 0 {
		/// rolling avg
		/// wikipedia my beloved
		/// https://en.wikipedia.org/wiki/Moving_average#Cumulative_average
		delta := uint64(now.Sub(stats.lastSubmission).Milliseconds())
		/// start the avg calc with the furst delta, not 0
		if stats.avgSubmissionDelta == 0 {
			stats.avgSubmissionDelta = delta
		} else {
			stats.avgSubmissionDelta =
				((stats.avgSubmissionDelta * (constants.SUBMISSION_DELTA_WINDOW - 1)) + delta) / constants.SUBMISSION_DELTA_WINDOW
		}
	}

	stats.lastSubmission = now
	stats.calcHashrate(now, currTargetDiff)
}

// getters
func (stats *ClientStats) Uptime() uint64 {
	return uint64(time.Since(stats.startTime).Milliseconds())
}
func (stats *ClientStats) HashrateMH() float64 {
	return stats.hashrate / 1e6
}
func (stats *ClientStats) HashrateH() float64 {
	return stats.hashrate
}

// live hashrate in MH/s
func (stats *ClientStats) calcHashrate(shareTime time.Time, currTargetDiff float64) {
	/// calc copied from public-pool
	windowStart := time.Unix((shareTime.Unix()/constants.HASHRATE_WINDOW)*constants.HASHRATE_WINDOW, 0)
	/// first call, make the current slot (and set the last as the init time)
	if stats.currTimeSlot.Unix() <= 0 {
		stats.currTimeSlot.Time = windowStart
		stats.lastTimeSlot.Time = stats.startTime
		/// if we're in the next chunk of time, snapshot the curr* and move over
	} else if stats.currTimeSlot.Unix() != windowStart.Unix() {
		stats.lastTimeSlot = stats.currTimeSlot

		stats.currTimeSlot.accDiff = uint64(currTargetDiff)
		stats.currTimeSlot.Time = windowStart
		/// otherwise just update stats
	} else {
		/// we wanna use the target difficulty for a stable number
		stats.currTimeSlot.accDiff += uint64(currTargetDiff)
		if stats.currTimeSlot.accDiff > 0 {
			/// "Hashrate = (share difficulty x 2^32) / time" - ben
			/// "2^32 represents the average number of hash attempts needed to find a valid hash at difficulty 1." - skot
			time := float64(shareTime.Sub(stats.lastTimeSlot.Time).Seconds())
			/// sum the two time slots for the total accumulated diff
			stats.hashrate = float64((stats.lastTimeSlot.accDiff+stats.currTimeSlot.accDiff)*4_294_967_296) / time
		}
	}
}

// clients are given an id, a job, and a channel to submit blocks on
func CreateClient(conn net.Conn, submissionChannel chan<- blockSubmission) StratumClient {
	client := StratumClient{
		ID:             ClientIDHash(conn.RemoteAddr().String()),
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

	if strings.Contains(strings.ToLower(ua), "axe"){
		split := strings.Split(ua, "/")
		if len(split) != 3 {
			/// confusion
			return ua
		}
		/// format is *axe/<chip>/<fw_version>, drop the version
		return strings.Join(split[:2], "/")
	} else if strings.Contains(ua, "luckyminer") {
		return "luckyminer"
	}
	/// otherwise fall back to public-pools parsing
	ua = strings.Split(ua, " ")[0]
	ua = strings.Split(ua, "/")[0]
	ua = strings.Split(ua, "v")[0]
	ua = strings.Split(ua, "-")[0]
	return ua
}
