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
	"strconv"
	"strings"
	"sync"
	"time"

	"git.0xf0xx0.eth.limo/0xf0xx0/pogolo/constants"

	"git.0xf0xx0.eth.limo/0xf0xx0/stratum"
	"git.0xf0xx0.eth.limo/0xf0xx0/stratumv2"
	"github.com/btcsuite/btcd/address/v2"
	"github.com/btcsuite/btcd/btcutil/v2"
	"github.com/btcsuite/btcd/chainhash/v2"
	"github.com/btcsuite/btcd/wire/v2"
)

// aka gopher
type StratumClient struct {
	currentJobMutex     sync.RWMutex
	CurrentJob          MiningJob
	conn                net.Conn
	User                address.Address
	Nickname            string
	UserAgent           string
	TargetDifficulty    float64
	SuggestedDifficulty float64    // overloaded, initially set by client (optional) then used by diff adjust
	ID                  stratum.ID // used for the extranonce1 (sv1) and channel ID (sv2)
	VersionRollingMask  uint32
	templateChan        chan *JobTemplate
	submissionChan      chan<- blockSubmission
	shareHashMutex      sync.Mutex
	shareHashes         map[chainhash.Hash]struct{} // stores hashes for dupe share detection, resets on new job
	stats               StratumClientStats
	protocol            uint8
	extendedChannel     bool
}

// used for hashrate calc
type timeSlot struct {
	time.Time
	accDiff uint64 // accumulated difficulty, used for hashrate calc
}

// all shares are converted into this common struct
type commonShare struct {
	ChannelID   uint32
	JobID       uint32
	Time        uint32
	Version     uint32
	Nonce       uint32
	Extranonce2 []byte
	Sequence    uint32 // only for sv2
}

type blockSubmission struct {
	Header   wire.BlockHeader
	Coinbase *wire.MsgTx
	Share    *commonShare
	ClientID stratum.ID // for lookup in client map
}

func (client *StratumClient) Run(ctx context.Context) {
	defer client.Stop()
	go client.readTemplateChanRoutine()

	/// 5 secs to send the initial stratum message
	client.conn.SetDeadline(time.Now().Add(time.Second * 5))

	/// peek to determine protocol
	r := bufio.NewReaderSize(client.conn, 512) // 512 bytes, we dont need much more
	b, err := r.Peek(1)
	if err != nil {
		return
	}

	// sv1 always starts with '{' and might start with whitespace
	if b[0] == '{' || b[0] == ' ' || b[0] == '\n' || b[0] == '\r' {
		client.protocol = 1
		client.processSv1Loop(ctx, r)
	} else {
		/// 1. handle SetupConnection
		frame := stratumv2.Frame{}
		if err := frame.DecodeFromReader(r); err != nil {
			return
		}
		if frame.MessageType != stratumv2.MessageSetupConnection {
			return
		}
		msg := stratumv2.SetupConnection{}
		if err = msg.Decode(frame.Payload); err != nil {
			client.logError("error decoding SetupConnection: %s", err)
			return
		}

		/// validate message
		if msg.MaxVersion != 2 || msg.MinVersion != 2 {
			client.writeSv2Msg(&stratumv2.SetupConnectionError{
				ErrorCode: stratumv2.ProtocolVersionMismatchError,
			}, stratumv2.MessageSetupConnectionError)
			client.logError("invalid SV2 version")
			return
		}
		if msg.Protocol != stratumv2.MiningProtocol {
			client.writeSv2Msg(&stratumv2.SetupConnectionError{
				ErrorCode: stratumv2.UnsupportedProtocolError,
			}, stratumv2.MessageSetupConnectionError)
			client.logError("wrong SV2 protocol")
			return
		}
		if msg.Flags & ^stratumv2.RequiresWorkSelectionFlag != 0 {
			client.writeSv2Msg(&stratumv2.SetupConnectionError{
				// TODO: extract into an UNSUPPORTED_FLAGS constant
				Flags:     stratumv2.RequiresWorkSelectionFlag,
				ErrorCode: stratumv2.UnsupportedFeatureFlagsError,
			}, stratumv2.MessageSetupConnectionError)
			client.logError("unsupported feature flags")
			return
		}
		/// TODO: figure out sv2 uas
		client.UserAgent = parseUserAgent(msg.DeviceVendor)

		/// write success
		client.writeSv2Msg(&stratumv2.SetupConnectionSuccess{UsedVersion: 2}, stratumv2.MessageSetupConnectionSuccess)

		/// 2. handle channel open
		if err := frame.DecodeFromReader(r); err != nil {
			return
		}
		switch frame.MessageType {
		case stratumv2.MessageOpenStandardMiningChannel:
			{
				msg := stratumv2.OpenStandardMiningChannel{}
				if err = msg.Decode(frame.Payload); err != nil {
					client.logError("error decoding OpenStandardMiningChannel: %s", err)
					break
				}

				if !client.validateSv2ChannelOpen(msg.RequestID, msg.MaxTarget, msg.NominalHashRate) {
					return
				}
				if !client.parseIdentity(msg.UserIdentity, msg.RequestID, nil) {
					return
				}

				client.writeSv2Msg(&stratumv2.OpenStandardMiningChannelSuccess{
					RequestID:        msg.RequestID,
					ChannelID:        uint32(client.ID),
					Target:           msg.MaxTarget,
					ExtranoncePrefix: client.ID.Bytes(),
				}, stratumv2.MessageOpenStandardMiningChannelSuccess)
			}
		case stratumv2.MessageOpenExtendedMiningChannel:
			{
				msg := stratumv2.OpenExtendedMiningChannel{}
				if err = msg.Decode(frame.Payload); err != nil {
					client.logError("error decoding OpenExtendedMiningChannel: %s", err)
					break
				}

				if msg.MinExtranonceSize > conf.ExtraNonce2Size {
					client.logError("min extranonce size (%d) is greater than configured size (%d)", msg.MinExtranonceSize, conf.ExtraNonce2Size)
					break
				}
				if !client.validateSv2ChannelOpen(msg.RequestID, msg.MaxTarget, msg.NominalHashRate) {
					return
				}
				if !client.parseIdentity(msg.UserIdentity, msg.RequestID, nil) {
					return
				}
				client.extendedChannel = true

				client.writeSv2Msg(&stratumv2.OpenExtendedMiningChannelSuccess{
					OpenStandardMiningChannelSuccess: stratumv2.OpenStandardMiningChannelSuccess{
						RequestID:        msg.RequestID,
						ChannelID:        uint32(client.ID),
						Target:           msg.MaxTarget,
						ExtranoncePrefix: client.ID.Bytes(),
					},
					ExtranonceSize: uint16(conf.ExtraNonce2Size),
				}, stratumv2.MessageOpenExtendedMiningChannelSuccess)
			}
		default:
			client.logError("second message wasn't a channel open")
			return
		}

		client.protocol = 2
		client.startMining()
		client.processSv2Loop(ctx, r)
	}
}
func (client *StratumClient) startMining() {
	log(fmt.Sprintf(
		/// dig, cause gophers, get it?
		"==<<>>=<<>>=<{green}%s{/green} has joined the dig!>=<<>>=<<>>==\n\tid: {green}%s{/green}\n\taddr: {green}%s",
		client.Name(), client.ID, client.Addr(),
	))

	if defaultMiningAddr != nil && client.User.EncodeAddress() == defaultMiningAddr.EncodeAddress() {
		client.log("{yellow}mining to pool address")
	}
	if client.VersionRollingMask > 0 {
		client.log("version rolling enabled! mask: {blue}%#x", client.VersionRollingMask)
	}
	/// the client may have suggested a difficulty before fully initialized
	/// if they haven't, we alert them to our default diff here
	if client.SuggestedDifficulty == 0 {
		if client.UserAgent == "cpuminer" || client.UserAgent == "nerdminer" {
			/// use the hardcoded min
			// client.setDifficulty(0.0001)
			client.setDifficulty(constants.MIN_DIFFICULTY)
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
func (client *StratumClient) Stop() {
	select {
	/// we've already stopped, avoid a panic
	case _, ok := <-client.templateChan:
		if !ok {
			return
		}
	default:
	}
	close(client.templateChan)

	/// remove ourselves from the client map
	if client.ID != 0 {
		clients.Delete(client.ID)
		log(fmt.Sprintf("==<<>>=<<>>=<{green}%s{/green} has left the dig!>=<<>>=<<>>==", client.Name()))
	}

	client.conn.Close()

	if conf.Benchmarking {
		sharesPS := float64(client.stats.sharesAccepted+client.stats.sharesRejected) / float64(client.stats.Uptime())
		totalSharesPerSec += sharesPS
		println(fmt.Sprintf("shares/s: %f", sharesPS))
	}
	client = nil
}

func (client *StratumClient) processSv2Loop(ctx context.Context, reader *bufio.Reader) {
	var err error
	/// this is allocated when a standard channel is opened and
	/// is used to pad the extranonce2 field for the coinbase
	var emptyExtranonce []byte
	if !client.extendedChannel {
		// alloc padding
		emptyExtranonce = make([]byte, conf.ExtraNonce2Size)
	}
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		frame := stratumv2.Frame{}
		if conf.Sv2Encryption {
			noised := stratumv2.NoiseFrame{}
			err = noised.DecodeFromReader(reader)
			frame = noised.Frame
		} else {
			err = frame.DecodeFromReader(reader)
		}
		switch err {
		case io.ErrClosedPipe:
		case io.EOF:
			return
		case nil:
		default:
			client.logError("failed to read frame: %s", err)
		}

		switch frame.MessageType {
		case stratumv2.MessageSubmitSharesExtended:
			{
				if !client.extendedChannel {
					client.logError("extended share submitted on standard channel")
					break
				}
				share := stratumv2.SubmitSharesExtended{}
				if err = share.Decode(frame.Payload); err != nil {
					client.logError("error decoding SubmitSharesExtended: %s", err)
					break
				}
				s := commonShare{
					ChannelID:   share.ChannelID,
					JobID:       share.JobID,
					Time:        share.Time,
					Version:     share.Version,
					Nonce:       share.Nonce,
					Extranonce2: share.Extranonce,
					Sequence:    share.Sequence,
				}
				client.currentJobMutex.RLock()
				client.validateShareSubmission(s, nil)
				client.currentJobMutex.RUnlock()
			}
		case stratumv2.MessageSubmitSharesStandard:
			{
				if client.extendedChannel {
					client.logError("standard share submitted on extended channel")
					break
				}
				share := stratumv2.SubmitSharesStandard{}
				if err = share.Decode(frame.Payload); err != nil {
					client.logError("error decoding SubmitSharesStandard: %s", err)
					break
				}
				s := commonShare{
					ChannelID:   share.ChannelID,
					JobID:       share.JobID,
					Time:        share.Time,
					Version:     share.Version,
					Nonce:       share.Nonce,
					Extranonce2: emptyExtranonce,
					Sequence:    share.Sequence,
				}
				client.currentJobMutex.RLock()
				client.validateShareSubmission(s, nil)
				client.currentJobMutex.RUnlock()
			}
		case stratumv2.MessageUpdateChannel:
			{
				msg := stratumv2.UpdateChannel{}
				if err = msg.Decode(frame.Payload); err != nil {
					client.logError("error decoding UpdateChannel: %s", err)
					return
				}

				if msg.ChannelID != uint32(client.ID) {
					client.writeSv2Msg(&stratumv2.UpdateChannelError{
						ChannelID: msg.ChannelID,
						ErrorCode: constants.ERROR_INV_CHAN_ID.Message,
					}, stratumv2.MessageUpdateChannelError)
					break
				}
				newTargetIsOld := constants.Target1U256.IsEqual(&msg.MaxTarget)
				newTargetMeetsMin := constants.Target1U256.IsMetBy(&msg.MaxTarget)
				if newTargetIsOld {
					/// calc the new difficulty from the new hashrate and switch immediately
					if msg.NominalHashRate > 0 {
						client.setDifficulty(calcDiffFromHashrate(float64(msg.NominalHashRate)))
					}
					break
				}
				/// if its too large, error
				if !newTargetMeetsMin {
					client.logError("new target difficulty too low")
					client.writeSv2Msg(&stratumv2.UpdateChannelError{
						ChannelID: msg.ChannelID,
						ErrorCode: constants.ERROR_LOW_DIFF.Message,
					}, stratumv2.MessageUpdateChannelError)
					break
				}

				/// "When maximum_target is smaller than currently used maximum target
				///  for the channel, upstream node MUST reflect the client’s request
				///  (and send appropriate SetTarget message)."
				client.setDifficulty(calcDifficulty(chainhash.Hash(msg.MaxTarget)))
			}
		case stratumv2.MessageCloseChannel:
			{
				msg := stratumv2.CloseChannel{}
				if err = msg.Decode(frame.Payload); err != nil {
					client.logError("error decoding CloseChannel: %s", err)
					return
				}
				client.log("leaving: %s", msg.ReasonCode)
				return
			}

		// ignored
		case stratumv2.MessageSetupConnection:
		case stratumv2.MessageOpenStandardMiningChannel:
		case stratumv2.MessageOpenExtendedMiningChannel:
			{
				break
			}
		default:
			client.logError("unknown method: %x", frame.MessageType)
			continue
		}
		/// deadline is a minute + 10x target share interval
		client.conn.SetDeadline(time.Now().Add(time.Minute + time.Second*10*time.Duration(conf.Pogolo.TargetShareInterval)))
	}
}
func (client *StratumClient) processSv1Loop(ctx context.Context, reader *bufio.Reader) {
	stratumInited := false
	isAuthed := false
	isSubscribed := false

	/// processing loop
	/// this should be async but
	/// 1) it complicates shutdown and
	/// 2) theres no point imo, everything gets handled in order anyway
	/// its fast enough
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		/// messages are newline separated (either lf or crlf)
		line, err := reader.ReadBytes('\n')
		if err != nil {
			switch err {
			case io.ErrClosedPipe:
			case io.EOF:
			case err.(*net.OpError):
				ne := err.(*net.OpError)
				client.logError("%s", ne.Err)
			default:
				client.logError("read error: %s", err)
			}
			return
		}

		/// process the message
		m, err := decodeStratumMessage(line)
		if err != nil {
			client.logError("stratum decode error: %s", err)
			return
		}

		switch m.GetMethod() {
		case stratum.MethodMiningSubmit:
			{
				if !stratumInited {
					client.logError("submit before subscribe")
					client.writeSv1Msg(m.RespondError(constants.ERROR_NOT_SUBBED))
					return
				}
				share := stratum.Share{}
				if err := share.FromRequest(m); err != nil {
					client.logError("error processing %s: %s", m.Method, err)
					client.writeSv1Msg(m.RespondError(constants.ERROR_UNPROCESSABLE))
					break
				}
				jobID, _ := strconv.ParseUint(share.JobID, 16, 64)

				client.currentJobMutex.RLock()
				s := commonShare{
					JobID:       uint32(jobID),
					Time:        share.Time,
					Version:     uint32(client.CurrentJob.Version) + share.VersionMask,
					Nonce:       share.Nonce,
					Extranonce2: share.Extranonce2,
				}
				client.validateShareSubmission(s, m)
				client.currentJobMutex.RUnlock()
			}
		case stratum.MethodMiningConfigure:
			{
				params := stratum.MiningConfigureParams{}
				if err := params.FromRequest(m); err != nil {
					client.logError("error processing %s: %s", m.Method, err)
					client.writeSv1Msg(m.RespondError(constants.ERROR_UNPROCESSABLE))
					break
				}
				res := stratum.MiningConfigureResult{}
				if params.Supports(stratum.ExtensionVersionRolling) {
					rollingConfig, err := params.GetVersionRolling()
					if err != nil {
						client.logError("couldnt parse version rolling config: %s", err)
						client.writeSv1Msg(m.RespondError(constants.ERROR_UNPROCESSABLE))
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

				client.writeSv1Msg(res.ToResponse(m.MessageID))
			}
		case stratum.MethodMiningAuthorize:
			{
				if isAuthed {
					break
				}
				params := stratum.MiningAuthorizeParams{}
				if err := params.FromRequest(m); err != nil {
					client.logError("error processing %s: %s", m.Method, err)
					client.writeSv1Msg(m.RespondError(constants.ERROR_UNPROCESSABLE))
					break
				}
				if conf.Sv1Password != "" && params.Password != conf.Sv1Password {
					client.logError("invalid password")
					client.writeSv1Msg(m.RespondError(constants.ERROR_UNAUTHORIZED))
					return
				}

				if !client.parseIdentity(params.Username, 0, m) {
					client.writeSv1Msg(m.RespondError(constants.ERROR_UNPROCESSABLE))
					break
				}

				client.writeSv1Msg(stratum.NewBooleanResponse(m.MessageID, true))
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
					client.writeSv1Msg(m.RespondError(constants.ERROR_UNPROCESSABLE))
					break
				}
				client.UserAgent = parseUserAgent(params.UserAgent)
				if client.UserAgent == "luckyminer" {
					/// unsupported
					client.writeSv1Msg(m.RespondError(constants.ERROR_NOT_ACCEPTED))
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
				client.writeSv1Msg(responseParams.ToResponse(m.MessageID))
				isSubscribed = true
			}
		case stratum.MethodMiningSuggestDifficulty:
			{
				/// only accept a suggested difficulty if we haven't got one before
				if conf.Pogolo.IgnoreSuggDiff || client.SuggestedDifficulty > 0 {
					client.writeSv1Msg(m.RespondError(constants.ERROR_NOT_ACCEPTED))
					break
				}

				params := stratum.MiningSuggestDifficultyParams{}
				if err := params.FromRequest(m); err != nil {
					client.logError("error processing %s: %s", m.Method, err)
					client.writeSv1Msg(m.RespondError(constants.ERROR_UNPROCESSABLE))
					break
				}
				suggestedDiff := math.Abs(params.Difficulty)
				if suggestedDiff >= constants.MIN_DIFFICULTY {
					/// this comment is just for visual spacing
					client.SuggestedDifficulty = suggestedDiff
					client.log("suggested difficulty {blue}%g", suggestedDiff)
					client.writeSv1Msg(stratum.NewBooleanResponse(m.MessageID, true))
				} else {
					client.logError("rejected suggested difficulty")
					client.writeSv1Msg(m.RespondError(constants.ERROR_NOT_ACCEPTED))
				}
			}
		case stratum.MethodMiningExtranonceSubscribe:
			{
				client.writeSv1Msg(m.RespondError(constants.ERROR_UNSUPP_METHOD))
			}
		default:
			{
				client.writeSv1Msg(m.RespondError(constants.ERROR_UNK_METHOD))
				client.logError("unknown stratum message: %+v", m)
			}
		}

		/// we only send work after authed and subbed (and set a flag so we dont do this again)
		if isAuthed && isSubscribed && !stratumInited {
			stratumInited = true

			client.startMining()
		}

		/// deadline is a minute + 10x target share interval
		client.conn.SetDeadline(time.Now().Add(time.Minute + time.Second*10*time.Duration(conf.Pogolo.TargetShareInterval)))
	}

}

// aims for the .TargetShareInterval
func (client *StratumClient) calcNextDifficulty() {
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
func (client *StratumClient) setDifficulty(newDiff float64) error {
	if newDiff <= 0 || newDiff == client.TargetDifficulty {
		return nil
	}
	if client.protocol == 1 {
		setdiff := &stratum.MiningSetDifficultyParams{
			Difficulty: newDiff,
		}
		if err := client.writeSv1Msg(setdiff.ToNotification()); err != nil {
			return err
		}
	} else {
		target := diffToTarget(newDiff)
		msg := &stratumv2.SetTarget{
			ChannelID: uint32(client.ID),
			MaxTarget: target,
		}
		if err := client.writeSv2Msg(msg, stratumv2.MessageSetTarget); err != nil {
			return err
		}
	}
	client.TargetDifficulty = newDiff
	return nil
}
func (client *StratumClient) createJob(template *JobTemplate) MiningJob {
	block := btcutil.NewBlock(template.MsgBlock.Copy())
	blockHeader := block.MsgBlock().Header

	coinbaseTx := fillCoinbaseTx(client.User, block, template.Subsidy, backendChainParams)
	/// serialized without the witness, we handle that on submission
	serializedCoinbaseTx := serializeCoinbaseTx(coinbaseTx.MsgTx())

	inputScript := coinbaseTx.MsgTx().TxIn[0].SignatureScript
	/// find the split point, right after the input
	partOneIndex := bytes.Index(serializedCoinbaseTx, inputScript)
	if partOneIndex < 0 {
		panic("partOneIndex shoudnt be below 0")
	}
	partOneIndex += len(inputScript)

	return MiningJob{
		JobIDInt:      template.ID,
		Header:        blockHeader,
		CoinbaseTx:    coinbaseTx,
		Version:       blockHeader.Version,
		MerkleBranch:  template.MerkleBranch,
		MinTime:       template.MinTime,
		MaxTime:       template.MaxTime,
		NetworkDiff:   template.NetworkDiff,
		PrevHash:      &blockHeader.PrevBlock,
		CoinbasePart1: serializedCoinbaseTx[:partOneIndex-int(constants.EXTRANONCE_SIZE+conf.Pogolo.ExtraNonce2Size)],
		CoinbasePart2: serializedCoinbaseTx[partOneIndex:],
		Timestamp:     blockHeader.Timestamp,
		Bits:          template.Bits,
	}
}
func (client *StratumClient) submitBlock(block blockSubmission) {
	client.submissionChan <- block
}
func (client *StratumClient) readTemplateChanRoutine() {
	for {
		template, ok := <-client.templateChan
		if !ok {
			/// closed
			return
		}

		newJob := client.createJob(template)

		/// vardiff
		if !conf.Pogolo.DisableVarDiff {
			client.calcNextDifficulty()
		}
		/// stratum spec applies diff changes to next job, so announce diff before announcing job

		if err := client.setDifficulty(client.SuggestedDifficulty); err != nil {
			if errors.Is(err, net.ErrClosed) {
				/// client died and we didnt notice?
				client.Stop()
				return
			}
			client.logError("error adjusting difficulty: %s", err)
		} else {
			client.log("adjusting share target to {blue}%g", client.SuggestedDifficulty)
		}

		if client.protocol == 1 {
			merkleBranches := make([][]byte, len(template.MerkleBranch))
			for i, branch := range template.MerkleBranch {
				merkleBranches[i] = branch[:]
			}
			params := &stratum.MiningNotifyParams{
				JobID:          strconv.FormatUint(template.ID, 16),
				PrevBlockHash:  newJob.PrevHash,
				MerkleBranches: merkleBranches,
				Version:        uint32(newJob.Version),
				Bits:           template.Bits,
				Timestamp:      newJob.Timestamp,
				CoinbasePart1:  newJob.CoinbasePart1,
				CoinbasePart2:  newJob.CoinbasePart2,
				Clean:          true,
			}
			newJob.MiningNotifyParams = *params
			err := client.writeSv1Msg(params.ToNotification())
			if err != nil {
				client.logError("error sending job: %s", err)
			}
		} else {
			prevhash := &stratumv2.SetNewPrevHash{
				ChannelID: uint32(client.ID),
				JobID:     uint32(currTemplateID),
				PrevHash:  stratumv2.U256(*newJob.PrevHash),
				MinTime:   uint32(newJob.Timestamp.Unix()), /// should be equiv to Clean=true
				Bits:      newJob.Header.Bits,
			}
			if client.extendedChannel {
				merklePath := make([]stratumv2.U256, len(newJob.MerkleBranch))
				for i, h := range newJob.MerkleBranch {
					merklePath[i] = stratumv2.U256(*h)
				}
				job := &stratumv2.NewExtendedMiningJob{
					ChannelID:             uint32(client.ID),
					JobID:                 uint32(currTemplateID),
					MinTime:               []uint32{uint32(newJob.MinTime)},
					Version:               uint32(newJob.Version),
					MerklePath:            merklePath,
					VersionRollingAllowed: true,
					CoinbasePrefix:        newJob.CoinbasePart1,
					CoinbaseSuffix:        newJob.CoinbasePart2,
				}
				client.writeSv2Msg(job, stratumv2.MessageNewExtendedMiningJob)
			} else {
				job := &stratumv2.NewMiningJob{
					ChannelID:  uint32(client.ID),
					JobID:      uint32(currTemplateID),
					MinTime:    []uint32{uint32(newJob.MinTime)},
					Version:    uint32(newJob.Version),
					MerkleRoot: stratumv2.U256(newJob.Header.MerkleRoot),
				}
				client.writeSv2Msg(job, stratumv2.MessageNewMiningJob)
			}
			client.writeSv2Msg(prevhash, stratumv2.MessageSetNewPrevHash)
		}

		/// reset dupe share map
		client.shareHashMutex.Lock()
		for h := range client.shareHashes {
			delete(client.shareHashes, h)
		}
		client.shareHashMutex.Unlock()

		/// NOTE: update job after everything to give the new job some time to be sent and switched to
		/// (reduces the chance of shares being submitted mid-job change)
		/// MAYBE: guess RTT and sleep for half?
		client.currentJobMutex.Lock()
		client.CurrentJob = newJob
		client.currentJobMutex.Unlock()
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

// parses `addr[.workername]` into client.User and client.Nickname
func (client *StratumClient) parseIdentity(userIdentity string, requestID uint32, msg *stratum.Request) (ok bool) {
	split := strings.Split(userIdentity, ".")
	if len(split) > 1 {
		client.Nickname = split[1]
	}
	decoded, err := address.DecodeAddress(split[0], backendChainParams)
	if err != nil {
		if defaultMiningAddr == nil {
			client.logError("failed decoding address: %s", err)
			if msg != nil {
				client.writeSv1Msg(msg.RespondError(constants.ERROR_UNPROCESSABLE))
			} else {
				client.writeSv2Msg(&stratumv2.OpenMiningChannelError{
					RequestID: requestID,
					ErrorCode: stratumv2.UnknownUserError,
				}, stratumv2.MessageOpenMiningChannelError)
			}
			return false
		}
		/// assume just the workername was passed
		if split[0] != "" {
			client.Nickname = split[0]
		}
		decoded = defaultMiningAddr
	}
	client.User = decoded
	return true
}
func (client *StratumClient) validateSv2ChannelOpen(requestID uint32, maxTarget stratumv2.U256, nominalHashRate float32) bool {
	// validate max target
	// TODO: verify Target1U256 is a valid maximum
	// maybe "steal" from a different pool lol
	if !constants.Target1U256.IsMetBy(&maxTarget) {
		client.logError("provided max target is out of range")
		client.writeSv2Msg(&stratumv2.OpenMiningChannelError{
			RequestID: requestID,
			ErrorCode: stratumv2.MaxTargetOutOfRangeError,
		}, stratumv2.MessageOpenMiningChannelError)
		return false
	}

	// guesstimate target difficulty from hashrate
	if nominalHashRate > 0 {
		cast := float64(nominalHashRate)
		client.SuggestedDifficulty = calcDiffFromHashrate(cast)
		client.stats.hashrate = cast
		client.log("guessed initial difficulty {blue}%d", client.SuggestedDifficulty)
	}
	return true
}
func (client *StratumClient) validateShareSubmission(share commonShare, m *stratum.Request) {
	if share.JobID != uint32(client.CurrentJob.JobIDInt) {
		client.stats.sharesRejected++
		if share.JobID == uint32(client.CurrentJob.JobIDInt-1) {
			if m != nil {
				client.writeSv1Msg(m.RespondError(constants.ERROR_SHARE_BETWEEN_JOBS))
			} else {
				client.writeSv2Msg(&stratumv2.SubmitSharesError{
					ChannelID:      uint32(client.ID),
					SequenceNumber: share.Sequence,
					ErrorCode:      stratumv2.Error(constants.ERROR_SHARE_BETWEEN_JOBS.Message),
				}, stratumv2.MessageSubmitSharesError)
			}
			client.logError("share submitted during job change")
			return
		}
		if m != nil {
			client.writeSv1Msg(m.RespondError(constants.ERROR_STALE))
		} else {
			client.writeSv2Msg(&stratumv2.SubmitSharesError{
				ChannelID:      uint32(client.ID),
				SequenceNumber: share.Sequence,
				ErrorCode:      stratumv2.StaleShareError,
			}, stratumv2.MessageSubmitSharesError)
		}
		client.logError("share rejected: stale job")
		return
	}

	if client.protocol == 2 && share.ChannelID != uint32(client.ID) {
		client.stats.sharesRejected++
		client.writeSv2Msg(&stratumv2.SubmitSharesError{
			ChannelID:      uint32(client.ID),
			SequenceNumber: share.Sequence,
			ErrorCode:      stratumv2.InvalidChannelIDError,
		}, stratumv2.MessageSubmitSharesError)
		client.logError("share rejected: invalid channel ID")
		return
	}

	/// no version rolling means the version is left untouched, its already valid
	currJobVer := uint32(client.CurrentJob.Version)
	if share.Version != currJobVer &&
		// version rolling means we NAND the share version with the version mask
		// if the result is not equal to the original version its invalid
		(share.Version & ^constants.VERSION_ROLLING_MASK) != currJobVer {
		println(client.CurrentJob.Version, share.Version, share.Version&^constants.VERSION_ROLLING_MASK)
		client.stats.sharesRejected++
		if m != nil {
			client.writeSv1Msg(m.RespondError(constants.ERROR_INV_VER_MASK))
		} else {
			client.writeSv2Msg(&stratumv2.SubmitSharesError{
				ChannelID:      uint32(client.ID),
				SequenceNumber: share.Sequence,
				ErrorCode:      stratumv2.Error(constants.ERROR_INV_VER_MASK.Message),
			}, stratumv2.MessageSubmitSharesError)
		}
		client.logError("share rejected: invalid version mask")
		return
	}
	println(client.CurrentJob.Version, share.Version)
	/// verify the difficulty
	/// the backing node will do the full block validation, we only care if the
	/// submission was high enough
	updatedHeader, ok := client.CurrentJob.UpdateHeader(client.ID, share, client.CurrentJob.MiningNotifyParams)
	if !ok {
		if m != nil {
			client.writeSv1Msg(m.RespondError(constants.ERROR_UNPROCESSABLE))
		} else {
			client.writeSv2Msg(&stratumv2.SubmitSharesError{
				ChannelID:      uint32(client.ID),
				SequenceNumber: share.Sequence,
				ErrorCode:      stratumv2.Error(constants.ERROR_UNPROCESSABLE.Message),
			}, stratumv2.MessageSubmitSharesError)
		}
		client.logError("invalid extranonce2 length")
		return
	}

	ntime := updatedHeader.Timestamp.Unix()
	if (client.CurrentJob.MinTime > 0 && ntime < client.CurrentJob.MinTime) || (client.CurrentJob.MaxTime > 0 && ntime > client.CurrentJob.MaxTime) {
		if m != nil {
			client.writeSv1Msg(m.RespondError(constants.ERROR_BAD_TIME))
		} else {
			client.writeSv2Msg(&stratumv2.SubmitSharesError{
				ChannelID:      uint32(client.ID),
				SequenceNumber: share.Sequence,
				ErrorCode:      stratumv2.Error(constants.ERROR_BAD_TIME.Message),
			}, stratumv2.MessageSubmitSharesError)
		}
		client.stats.sharesRejected++
		client.logError("share rejected: invalid timestamp")
		return
	}

	shareHash := updatedHeader.BlockHash()
	shareDiff := calcDifficulty(shareHash)
	if shareDiff < client.TargetDifficulty {
		if m != nil {
			client.writeSv1Msg(m.RespondError(constants.ERROR_LOW_DIFF))
		} else {
			client.writeSv2Msg(&stratumv2.SubmitSharesError{
				ChannelID:      uint32(client.ID),
				SequenceNumber: share.Sequence,
				ErrorCode:      stratumv2.DifficultyTooLowError,
			}, stratumv2.MessageSubmitSharesError)
		}
		client.stats.sharesRejected++
		client.logError("share rejected: diff too low (%.5g/%g)", shareDiff, client.TargetDifficulty)
		return
	}

	client.shareHashMutex.Lock()
	defer client.shareHashMutex.Unlock()
	/// check if share is dupe
	if _, ok := client.shareHashes[shareHash]; ok {
		if m != nil {
			client.writeSv1Msg(m.RespondError(constants.ERROR_DUPE_SHARE))
		} else {
			client.writeSv2Msg(&stratumv2.SubmitSharesError{
				ChannelID:      uint32(client.ID),
				SequenceNumber: share.Sequence,
				ErrorCode:      stratumv2.Error(constants.ERROR_DUPE_SHARE.Message),
			}, stratumv2.MessageSubmitSharesError)
		}
		client.stats.sharesRejected++
		client.logError("share rejected: duplicate")
		return
	}
	/// add to dupe map
	client.shareHashes[shareHash] = struct{}{}

	if shareDiff >= client.CurrentJob.NetworkDiff {
		/// !!! block! dont say ANYTHING until after submitted
		submission := blockSubmission{
			ClientID: client.ID,
			Header:   updatedHeader,
			Coinbase: client.CurrentJob.CoinbaseTx.MsgTx().Copy(),
			Share:    &share,
		}

		client.submitBlock(submission)
		client.log("{yellow}block candidate submitted")
	}

	if m != nil {
		client.writeSv1Msg(stratum.NewBooleanResponse(m.MessageID, true))
	} else {
		/// TODO: figure out batching
		client.writeSv2Msg(&stratumv2.SubmitSharesSuccess{
			ChannelID:               share.ChannelID,
			LastSequenceNumber:      share.Sequence,
			NewSubmitsAcceptedCount: 1,
			NewSharesSum:            uint64(shareDiff),
		}, stratumv2.MessageSubmitSharesSuccess)
	}

	/// vanity things
	if shareDiff > client.stats.bestDiff {
		client.stats.bestDiff = shareDiff
		client.log("{green}new best session diff!")
	}
	client.stats.sharesAccepted++
	/// update with the target diff for a more accurate estimation
	client.stats.update(client.TargetDifficulty)
	client.log("diff {blue}%s{/blue} of {blue}%s{/blue} (best: {bluebright}%s{/bluebright})\n{blackbright}%s\n\tversion: {blue}%08x{/blue} nonce: {green}%08x{/green} extranonce: {blue}%s{green}%x{/blue}{/green}\n\t{green}%s{/green}, avg submit delta: {blue}%.2fs{/blue}",
		formatDifficulty(shareDiff), formatDifficulty(client.TargetDifficulty), formatDifficulty(client.stats.bestDiff),
		shareHash,
		updatedHeader.Version, share.Nonce, client.ID, share.Extranonce2,
		formatHashrate(client.stats.HashrateMH()), client.stats.avgSubmissionDelta/1000)
}

// chatter
func (client *StratumClient) writeSv1Msg(msg stratum.Message) error {
	b, err := msg.Marshal()
	if err != nil {
		client.logError("failed to marshal message: %s", err)
		return err
	}

	return client.writeConn(b)
}
func (client *StratumClient) writeSv2Msg(payload stratumv2.Codable, messageType stratumv2.MessageType) error {
	b, err := payload.Encode()
	if err != nil {
		client.logError("failed to encode payload: %s", err)
		return err
	}
	frame := stratumv2.Frame{
		MessageType:   messageType,
		ExtensionType: stratumv2.ExtensionTypeCore,
		MessageLength: stratumv2.U24(len(b)),
		Payload:       b,
	}
	if conf.Sv2Encryption {
		noiseFrame := stratumv2.NoiseFrame{
			Frame: frame,
		}
		f, err := noiseFrame.Encode()
		if err != nil {
			client.logError("failed to encode encrypted frame: %s", err)
			return err
		}
		return client.writeConn(f)
	}
	f, err := frame.Encode()
	if err != nil {
		client.logError("failed to encode frame: %s", err)
		return err
	}
	// client.log("TX: %x", f)
	return client.writeConn(f)
}
func (client *StratumClient) writeConn(b []byte) error {
	_, err := client.conn.Write(b)
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
	sharesAccepted,
	sharesRejected uint64
	avgSubmissionDelta, // in ms
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
		/// start the avg calc with the target delta, not 0
		if stats.avgSubmissionDelta == 0 {
			stats.avgSubmissionDelta = float64(conf.Pogolo.TargetShareInterval)
		} else {
			/// avg = smoothing*delta + (1-smoothing)*avg
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
func CreateClient(conn net.Conn, submissionChannel chan<- blockSubmission) *StratumClient {
	client := &StratumClient{
		ID:             clientIDHash(conn.LocalAddr().String() + conn.RemoteAddr().String()),
		stats:          StratumClientStats{},
		conn:           conn,
		templateChan:   make(chan *JobTemplate, 1),
		submissionChan: submissionChannel,
		// allocate enough space to store the expected number of share hashes before a new job is sent out,
		// plus some extra to account for luck
		shareHashes: make(map[chainhash.Hash]struct{}, 5+conf.JobInterval/conf.TargetShareInterval),
	}
	return client
}
