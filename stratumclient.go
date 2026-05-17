package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"time"

	"git.0xf0xx0.eth.limo/0xf0xx0/pogolo/constants"

	"git.0xf0xx0.eth.limo/0xf0xx0/stratum"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
)

// aka gopher
type StratumClient struct {
	CurrentJob          MiningJob
	conn                net.Conn
	User                btcutil.Address
	Password            string
	Nickname            string
	UserAgent           string
	TargetDifficulty    float64
	SuggestedDifficulty float64 // overloaded, initially set by client (optional) then used by diff adjust
	templateChan        chan *JobTemplate
	submissionChan      chan<- blockSubmission
	shareHashes         map[chainhash.Hash]struct{} // stores hashes for dupe share detection, resets on new job
	stats               *StratumClientStats
	ID                  stratum.ID
	VersionRollingMask  uint32
}

// used for hashrate calc
type timeSlot struct {
	time.Time
	accDiff uint64 // accumulated difficulty, used for hashrate calc
}

type blockSubmission struct {
	Block    btcutil.Block
	Share    stratum.Share
	ClientID stratum.ID // for lookup in client map
}

func (client *StratumClient) Run(ctx context.Context) {
	defer client.Stop()
	go client.readTemplateChanRoutine()
	stratumInited := false
	isAuthed := false
	isSubscribed := false

	/// 5 secs to send the initial stratum message
	client.conn.SetDeadline(time.Now().Add(time.Second * 5))
	reader := bufio.NewScanner(client.conn)

	/// processing loop
	for reader.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}

		/// messages are newline separated (either lf or crlf)
		line := bytes.TrimSpace(reader.Bytes())

		/// process the message
		m, err := DecodeStratumMessage(line)
		if err != nil {
			client.logError("stratum decode error: %s", err)
			return
		}

		switch m.GetMethod() {
		case stratum.MethodMiningSubmit:
			{
				if !stratumInited {
					client.logError("submit before subscribe")
					client.writeRes(m.RespondError(constants.ERROR_NOT_SUBBED))
					return
				}
				s := stratum.Share{}
				if err := s.FromRequest(m); err != nil {
					client.logError("error processing %s: %s", m.Method, err)
					client.writeRes(m.RespondError(constants.ERROR_UNPROCESSABLE))
					break
				}
				client.validateShareSubmission(s, m)
			}
		case stratum.MethodMiningConfigure:
			{
				params := stratum.MiningConfigureParams{}
				if err := params.FromRequest(m); err != nil {
					client.logError("error processing %s: %s", m.Method, err)
					client.writeRes(m.RespondError(constants.ERROR_UNPROCESSABLE))
					break
				}
				res := stratum.MiningConfigureResult{}
				if params.Supports(stratum.ExtensionVersionRolling) {
					rollingConfig, err := params.GetVersionRolling()
					if err != nil {
						client.logError("couldnt parse version rolling config: %s", err)
						client.writeRes(m.RespondError(constants.ERROR_UNPROCESSABLE))
						return
					}
					/// bip-310
					client.VersionRollingMask = uint32(rollingConfig.Mask) & constants.VERSION_ROLLING_MASK

					err = res.SetVersionRolling(stratum.VersionRollingConfigurationResult{Accepted: true, Mask: client.VersionRollingMask})
					if err != nil {
						/// uhhhhhhhhhhhhhhhh
						/// honestly just leave this as a panic
						panic(err)
					}
				}

				client.writeRes(res.ToResponse(m.MessageID))
			}
		case stratum.MethodMiningAuthorize:
			{
				if isAuthed {
					break
				}
				params := stratum.MiningAuthorizeParams{}
				if err := params.FromRequest(m); err != nil {
					client.logError("error processing %s: %s", m.Method, err)
					client.writeRes(m.RespondError(constants.ERROR_UNPROCESSABLE))
					break
				}
				if conf.Pogolo.Password != "" && params.Password != conf.Pogolo.Password {
					client.logError("invalid password")
					client.writeRes(m.RespondError(constants.ERROR_UNAUTHORIZED))
					return
				}
				decoded, err := btcutil.DecodeAddress(params.Username, backendChainParams)
				if err != nil {
					if defaultMiningAddr == nil {
						client.logError("failed decoding address: %s", err)
						client.writeRes(m.RespondError(constants.ERROR_UNPROCESSABLE))
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
				client.writeRes(stratum.NewBooleanResponse(m.MessageID, true))
				isAuthed = true
			}
		case stratum.MethodMiningSubscribe:
			{
				if isSubscribed {
					break
				}
				params := stratum.MiningSubscribeParams{}
				if err := params.FromRequest(m); err != nil {
					client.logError("error processing %s: %s", m.Method, err)
					client.writeRes(m.RespondError(constants.ERROR_UNPROCESSABLE))
					break
				}
				client.UserAgent = parseUserAgent(params.UserAgent)
				if client.UserAgent == "luckyminer" {
					/// unsupported
					client.writeRes(m.RespondError(constants.ERROR_NOT_ACCEPTED))
					return
				}
				responseParams := stratum.MiningSubscribeResult{
					Subscriptions: []stratum.MiningSubscription{
						{
							Method:    stratum.MethodMiningNotify,
							SessionID: client.ID,
						},
					},
					Extranonce1:     client.ID,
					Extranonce2Size: uint32(conf.Pogolo.ExtraNonce2Size),
				}
				client.writeRes(responseParams.ToResponse(m.MessageID))
				isSubscribed = true
			}
		case stratum.MethodMiningSuggestDifficulty:
			{
				/// only accept a suggested difficulty if we haven't got one before
				if conf.Pogolo.IgnoreSuggDiff || client.SuggestedDifficulty > 0 {
					client.writeRes(m.RespondError(constants.ERROR_NOT_ACCEPTED))
					break
				}

				params := stratum.MiningSuggestDifficultyParams{}
				if err := params.FromRequest(m); err != nil {
					client.logError("error processing %s: %s", m.Method, err)
					client.writeRes(m.RespondError(constants.ERROR_UNPROCESSABLE))
					break
				}
				suggestedDiff := math.Abs(params.Difficulty)
				if suggestedDiff >= constants.MIN_DIFFICULTY {
					/// this comment is just for visual spacing
					client.SuggestedDifficulty = suggestedDiff
					client.log("suggested difficulty {blue}%g", suggestedDiff)
					client.writeRes(stratum.NewBooleanResponse(m.MessageID, true))
				} else {
					client.logError("rejected suggested difficulty")
					client.writeRes(m.RespondError(constants.ERROR_NOT_ACCEPTED))
				}
			}
		case stratum.MethodMiningExtranonceSubscribe:
			{
				client.writeRes(m.RespondError(constants.ERROR_UNSUPP_METHOD))
			}
		default:
			{
				client.writeRes(m.RespondError(constants.ERROR_UNK_METHOD))
				client.logError("unknown stratum message: %+v", m)
			}
		}

		/// we only send work after authed and subbed (and set a flag so we dont do this again)
		if isAuthed && isSubscribed && !stratumInited {
			stratumInited = true

			log(fmt.Sprintf(
				/// dig, cause gophers, get it?
				"==<<>>=<<>>=<{green}%s{/green} has joined the dig!>=<<>>=<<>>==\n\tid: {green}%s{/green}\n\taddr: {green}%s",
				client.Name(), client.ID, client.Addr(),
			))

			if defaultMiningAddr != nil && client.User.EncodeAddress() == (*defaultMiningAddr).EncodeAddress() {
				client.log("{yellow}mining to pool address")
			}
			if client.VersionRollingMask > 0 {
				client.log("version rolling enabled! mask: {blue}%#x", client.VersionRollingMask)
			}
			/// the client may have suggested a difficulty before fully initialized
			/// if they haven't, we alert them to our default diff here
			if client.SuggestedDifficulty == 0 {
				if client.UserAgent == "cpuminer" || client.UserAgent == "nerdminer" {
					if conf.Benchmarking {
						client.setDifficulty(0.000001) /// lowest diff before cpuminer deadlocks
					} else {
						/// use the hardcoded min
						client.setDifficulty(constants.MIN_DIFFICULTY)
					}
				} else {
					client.setDifficulty(conf.Pogolo.DefaultDifficulty)
				}
			}

			/// i dont think the order matters, but lets send the current template
			/// before adding to the client map, just in case notifyClients gets
			/// called in between (and rapid-fires jobs)
			if currTemplate != nil {
				client.TemplateChannel() <- currTemplate
			}
			clients.Add(client)
			client.stats.startTime = time.Now()
		}

		/// deadline is a minute + 10x target share interval
		client.conn.SetDeadline(time.Now().Add(time.Minute + time.Second*10*time.Duration(conf.Pogolo.TargetShareInterval)))
	}

	switch err := reader.Err(); err {
	case nil:
	case io.ErrClosedPipe:
	case io.EOF:
	case err.(*net.OpError):
		ne := err.(*net.OpError)
		client.logError("%s", ne.Err)
	default:
		client.logError("%s", err)
	}
}
func (client *StratumClient) Stop() {
	if client.templateChan == nil {
		return
	}
	close(client.templateChan)
	/// nil because receive-side closure
	client.templateChan = nil

	/// remove ourselves from the client map
	if client.ID != 0 {
		clients.Delete(client.ID)
		log(fmt.Sprintf("==<<>>=<<>>=<{green}%s{/green} has left the dig!>=<<>>=<<>>==", client.Name()))
	}

	client.conn.Close()

	if conf.Benchmarking {
		sharesPS := float64(client.stats.sharesAccepted) / float64(client.stats.Uptime())
		totalSharesPerSec += sharesPS
		log(fmt.Sprintf("shares/s: %f", sharesPS))
	}
}

// aims for the .TargetShareInterval
func (client *StratumClient) adjustDiff() {
	if client.stats.avgSubmissionDelta == 0 {
		return
	}
	/// negative = running slow, positive = running fast
	/// round it to avoid floating point madness
	difference := math.Round(float64(conf.Pogolo.TargetShareInterval) - client.stats.avgSubmissionDelta/1000)
	absDifference := math.Abs(difference)
	/// natural variance is +- 1-3s, this adjustment routine seems to consistently
	/// tighten it to +-1s
	if absDifference < 1 {
		return
	}
	/// cap the adjustment at +-256
	delta := min(math.Pow(2, absDifference*2), 256)
	if difference < 0 {
		delta = -(delta / 2) /// we want to be more conservative when adjusting downwards
	}

	newDiff := max(client.TargetDifficulty+delta, constants.MIN_DIFFICULTY)
	client.SuggestedDifficulty = newDiff
	client.log("queued diff adjustment by {blue}%+g{/blue} to {blue}%g", delta, client.SuggestedDifficulty)
}

func (client *StratumClient) readTemplateChanRoutine() {
	for {
		template, ok := <-client.templateChan
		if !ok {
			/// closed
			return
		}
		client.CurrentJob = client.createJob(template)
		/// adjusted by vardiff
		/// stratum spec applies diff changes to next job, so announce changes before announcing job
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
		err := client.writeNotif(client.CurrentJob.MiningNotifyParams.ToNotification())
		if err != nil {
			client.logError("error sending job: %s", err)
		}
	}
}

func (client *StratumClient) TemplateChannel() chan<- *JobTemplate {
	return client.templateChan
}

// returns the nickname if set and falls back to the id
func (client *StratumClient) Name() string {
	if client.Nickname != "" {
		return client.Nickname
	}
	return client.ID.String()
}
func (client *StratumClient) Addr() net.Addr {
	return client.conn.RemoteAddr()
}

func (client *StratumClient) setDifficulty(newDiff float64) error {
	if newDiff == client.TargetDifficulty {
		return nil
	}
	setdiff := &stratum.MiningSetDifficultyParams{
		Difficulty: newDiff,
	}
	if err := client.writeNotif(setdiff.ToNotification()); err != nil {
		return err
	}
	client.TargetDifficulty = newDiff
	return nil
}
func (client *StratumClient) validateShareSubmission(share stratum.Share, m *stratum.Request) {
	if share.JobID != client.CurrentJob.MiningNotifyParams.JobID {
		client.stats.sharesRejected++
		client.logError("share rejected: unknown job")
		client.writeRes(m.RespondError(constants.ERROR_UNK_JOB))
		return
	}
	/// we'll only verify the difficulty
	/// the backing node will do the full block validation, we only care if the
	/// submission was high enough
	updatedBlock, err := client.CurrentJob.UpdateBlock(client.ID, share, client.CurrentJob.MiningNotifyParams)
	if err != nil {
		client.logError(err.Error())
		client.writeRes(m.RespondError(constants.ERROR_UNPROCESSABLE))
		return
	}

	/// check if share is dupe
	shareHash := updatedBlock.Header.BlockHash()
	if _, ok := client.shareHashes[shareHash]; ok {
		client.writeRes(m.RespondError(constants.ERROR_DUPE_SHARE))
		client.stats.sharesRejected++
		client.logError("share rejected: duplicate")
		return
	}
	/// add to dupe map
	client.shareHashes[shareHash] = struct{}{}

	shareDiff := CalcDifficulty(shareHash)
	if shareDiff >= client.TargetDifficulty {
		if !conf.Benchmarking && shareDiff >= client.CurrentJob.NetworkDiff {
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
		client.log("diff {blue}%s{/blue} of {blue}%s{/blue} (best: {bluebright}%s{/bluebright})\n{blackbright}%s\n\tversion: {blue}%08x{/blue} nonce: {green}%08x{/green} extranonce: {blue}%s{green}%x{/blue}{/green}\n\t{green}%s{/green}, avg submit delta: {blue}%.2fs{/blue}",
			FormatDifficulty(shareDiff), FormatDifficulty(client.TargetDifficulty), FormatDifficulty(client.stats.bestDiff),
			shareHash,
			updatedBlock.Header.Version, share.Nonce, client.ID, share.Extranonce2,
			FormatHashrate(client.stats.HashrateMH()), client.stats.avgSubmissionDelta/1000)
	} else {
		client.writeRes(m.RespondError(constants.ERROR_LOW_DIFF))
		client.stats.sharesRejected++
		client.logError("share rejected: diff too low (%.5g/%g)", shareDiff, client.TargetDifficulty)
	}

	// attempt to queue a diff adjustment every `constants.SUBMISSION_DELTA_WINDOW`
	if !conf.Pogolo.DisableVarDiff && (client.stats.sharesAccepted+client.stats.sharesRejected)%constants.DIFF_ADJUST_PERIOD == 0 {
		client.adjustDiff()
	}
}
func (client *StratumClient) createJob(template *JobTemplate) MiningJob {
	block := btcutil.NewBlock(template.MsgBlock.Copy())
	blockHeader := block.MsgBlock().Header

	merkleBranches := make([][]byte, len(template.MerkleBranch))
	for i, branch := range template.MerkleBranch {
		merkleBranches[i] = branch[:]
	}

	coinbaseTx := FillCoinbaseTx(client.User, block, template.Subsidy, backendChainParams)

	/// serialized without the witness, we handle that on submission
	serializedCoinbaseTx := SerializeCoinbaseTx(coinbaseTx.MsgTx())

	inputScript := coinbaseTx.MsgTx().TxIn[0].SignatureScript
	/// find the split point, right after the input
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
		MiningNotifyParams: stratum.MiningNotifyParams{
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
	/// reset dupe share map
	for h := range client.shareHashes {
		delete(client.shareHashes, h)
	}

	return job
}

// chatter
func (client *StratumClient) submitBlock(block blockSubmission) {
	client.submissionChan <- block
}
func (client *StratumClient) writeRes(res *stratum.Response) error {
	return client.writeMsg(res)
}
func (client *StratumClient) writeMsg(res stratum.Message) error {
	bytes, err := res.Marshal()
	if err != nil {
		client.logError("failed to marshal response: %s", err)
		return err
	}

	return client.writeConn(bytes)
}
func (client *StratumClient) writeNotif(n *stratum.Notification) error {
	bytes, err := n.Marshal()
	if err != nil {
		client.logError("failed to marshal notification: %s", err)
		return err
	}

	return client.writeConn(bytes)
}
func (client *StratumClient) writeConn(b []byte) error {
	_, err := client.conn.Write(b)
	// client.log("{blackbright}< %s", b)
	return err
}

// logging
func (client *StratumClient) log(s string, a ...any) {
	s = fmt.Sprintf(s, a...)
	log("[{green}" + client.Name() + "{/green}]{cyan} " + s)
}
func (client *StratumClient) logError(s string, a ...any) {
	s = fmt.Sprintf(s, a...)
	logError("{cyan}[{red}" + client.Name() + "{/red}]{/cyan} " + s)
}

// stats for the api
type StratumClientStats struct {
	lastTimeSlot,
	currTimeSlot timeSlot
	startTime, // time the client subscribed
	lastSubmission time.Time // used for calcing delta between `mining.submit`s
	avgSubmissionDelta float64 // in ms
	sharesAccepted,
	sharesRejected uint64
	bestDiff, // session
	hashrate float64
}

func (stats *StratumClientStats) update(currTargetDiff float64) {
	now := time.Now()
	if stats.lastSubmission.Unix() > 0 {
		/// exponential moving average
		/// wikipedia my beloved
		/// https://en.wikipedia.org/wiki/Exponential_smoothing
		delta := float64(now.Sub(stats.lastSubmission).Milliseconds())
		/// start the avg calc with the furst delta, not 0
		if stats.avgSubmissionDelta == 0 {
			stats.avgSubmissionDelta = delta
		} else {
			// avg = smoothing*delta + (1-smoothing)*avg
			smoothing := 0.01
			stats.avgSubmissionDelta =
				smoothing*delta + (1-smoothing)*stats.avgSubmissionDelta
		}
	}

	stats.calcHashrate(now, currTargetDiff)
	stats.lastSubmission = now
}

// getters
func (stats *StratumClientStats) Uptime() uint64 {
	return uint64(time.Since(stats.startTime).Seconds())
}
func (stats *StratumClientStats) HashrateMH() float64 {
	return stats.hashrate / 1e6
}
func (stats *StratumClientStats) HashrateH() float64 {
	return stats.hashrate
}

// live hashrate in H/s
func (stats *StratumClientStats) calcHashrate(shareTime time.Time, currTargetDiff float64) {
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
			time := shareTime.Sub(stats.lastTimeSlot.Time).Seconds()
			/// sum the two time slots for the total accumulated diff
			stats.hashrate = float64((stats.lastTimeSlot.accDiff+stats.currTimeSlot.accDiff)*4_294_967_296) / time
		}
	}
}

// clients are given an id, a job, and a channel to submit blocks on
func CreateClient(conn net.Conn, submissionChannel chan<- blockSubmission) StratumClient {
	client := StratumClient{
		ID:             ClientIDHash(conn.RemoteAddr().String()),
		stats:          &StratumClientStats{},
		conn:           conn,
		templateChan:   make(chan *JobTemplate),
		submissionChan: submissionChannel,
		shareHashes:    make(map[chainhash.Hash]struct{}, 15),
	}
	return client
}
