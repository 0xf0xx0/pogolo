// stratumclient is simple :3
// we take a conn and a submission channel, provide a template channel to receive [JobTemplate]s over,
// and start mining uwu
//
// the codepath goes
//  1. .Run() starts .readTemplateChanRoutine(),
//     	peeks into the furst byte to determine whether the client is sv1 or sv2,
//     	then calls processSv1Loop() or processSv2Loop() to handle the message loop
//     	it handles the sv2 setup handshake before handing off to processSv2Loop(), per the spec
// 		if the client suggested a difficulty during setup
//      (for sv1, this MUST come before the mining.subscribe to be processed immediately),
//  	pogolo will ensure its above the hard-coded [constants.MIN_DIFFICULTY] and respond with a success.
//     	either way, once setup is done, the message loop func calls
//  2. .startMining(), which sends the furst job to the client and adds the client to the map.
//     	.readTemplateChanRoutine() creates a job with .createJob() and ships it off to the client
// 	   	after calculating the next difficulty adjustment with .calcNextDifficulty() and setDifficulty().
//
// the client submits shares, which are processed into a [commonShare] and handled by
//  3. .validateShareSubmission(), where its processed.
//     	if the share is above the client diff, its hash gets logged into the dupe map
//     	and its difficulty is used to update the hashrate estimation.
//     	if its also above network diff, it gets submitted to the backend with .submitBlock() and hopefully becomes a real block!
//
// the client maintains a [*wire.BlockHeader] and a [*wire.MsgTx]
// 	for the current job header and coinbase txn. this lets the client run independently
// 	and enables greater scaling for larger home swarms. the coinbase txn is created in the main routine
//  with createEmptyCoinbase, and populated by each client in .createJob() with fillCoinbaseTx().
//  the job header obviously comes from the job template, which comes from the backend routine.
//

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
	"sync/atomic"
	"time"

	"git.0xf0xx0.eth.limo/0xf0xx0/pogolo/constants"

	"git.0xf0xx0.eth.limo/0xf0xx0/stratum"
	"git.0xf0xx0.eth.limo/0xf0xx0/stratumv2"
	"github.com/btcsuite/btcd/address/v2"
	"github.com/btcsuite/btcd/chainhash/v2"
	"github.com/btcsuite/btcd/wire/v2"
)

// all shares are converted into this common struct
type commonShare struct {
	Extranonce2 []byte
	JobID       uint32
	ChannelID   uint32 // only for sv2
	Sequence    uint32 // only for sv2
	Time        uint32
	Version     uint32
	Nonce       uint32
}

type blockSubmission struct {
	Header   wire.BlockHeader
	Coinbase *wire.MsgTx
	Share    *commonShare
	ClientID stratum.ID // for lookup in client map
}

// aka gopher
type StratumClient struct {
	CurrentJob          MiningJob
	stats               StratumClientStats
	conn                net.Conn
	User                address.Address
	Nickname            string
	UserAgent           string
	TargetDifficulty    float64
	SuggestedDifficulty float64                     // overloaded, initially set by client (optional) then used by diff adjust
	ID                  stratum.ID                  // used for the extranonce1 (sv1) and channel ID (sv2)
	shareHashes         map[chainhash.Hash]struct{} // stores hashes for dupe share detection, resets on new job
	shareHashMutex      sync.Mutex
	currentJobMutex     sync.RWMutex
	templateChan        chan *JobTemplate
	submissionChan      chan<- blockSubmission
	protocol            uint8
	extendedChannel     bool
	send, recv          *stratumv2.CipherState

	logPrefix    string
	errLogPrefix string
}

// shitty name, but wraps a bufio.reader and net.conn for sv2 handshake
type wrapperRW struct {
	r *bufio.Reader
	c net.Conn
}

func (t *wrapperRW) Read(b []byte) (int, error) {
	return t.r.Read(b)
}
func (t *wrapperRW) Write(b []byte) (int, error) {
	return t.c.Write(b)
}

func (client *StratumClient) Run(ctx context.Context) {
	defer client.Stop()
	go client.readTemplateChanRoutine()

	/// 5 secs to init
	client.conn.SetReadDeadline(time.Now().Add(time.Second * 5))

	/// MAYBE: figure out how to start with a small 128 byte buffer and grow when a larger message comes in?
	/// the largest message we'll handle is an sv2 SetupConnection frame, at a max of ~1288 bytes
	/// shares are sub-128 bytes, everything else is sub-512, if not -256
	/// TODO: figure out if we can avoid the buffer entirely while still peeking
	r := bufio.NewReaderSize(client.conn, 660)
	/// peek to determine protocol
	b, err := r.Peek(1)
	if err != nil {
		return
	}

	/// sv1 always starts with '{' and might start with whitespace
	if b[0] == '{' || b[0] == ' ' || b[0] == '\n' || b[0] == '\r' {
		client.protocol = 1
		client.processSv1Loop(ctx, r)
	} else {
		/// perform handshake
		pawshake := &stratumv2.HandshakeState{}
		rw := &wrapperRW{
			r: r,
			c: client.conn,
		}
		recv, send, err := pawshake.PerformHandshakeResponder(rw, sv2Cert, sv2StaticKeypair)
		if err != nil {
			client.logErrorf("error during sv2 handshake: %s", err)
			return
		}
		client.send = send
		client.recv = recv

		/// 1. handle SetupConnection

		frame, err := recv.DecryptFrameFromReader(client.conn)
		if err != nil {
			client.logErrorf("error decrypting SetupConnection: %s", err)
			return
		}
		if frame.MessageType != stratumv2.MessageSetupConnection {
			client.logError("furst message not SetupConnection")
			return
		}
		b, _ := frame.Encode()
		client.logf("{blue}RX: (%x) %x", frame.MessageType, b)
		msg := stratumv2.SetupConnection{}
		if err = msg.Decode(frame.Payload); err != nil {
			client.logErrorf("error decoding SetupConnection: %s", err)
			return
		}

		/// validate message
		if msg.MaxVersion != stratumv2.ProtocolVersion || msg.MinVersion != stratumv2.ProtocolVersion {
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
		/// thou shalt not select thy own work
		if msg.Flags&stratumv2.RequiresWorkSelectionFlag == 1 {
			client.writeSv2Msg(&stratumv2.SetupConnectionError{
				// TODO: extract into an UNSUPPORTED_FLAGS constant
				Flags:     stratumv2.RequiresWorkSelectionFlag,
				ErrorCode: stratumv2.UnsupportedFeatureFlagsError,
			}, stratumv2.MessageSetupConnectionError)
			client.logError("unsupported feature flags")
			return
		}
		/// check flags, extendedChannel is used for channel opening later
		if msg.Flags&stratumv2.RequiresStandardJobsFlag == 1 {
			client.log("standard channel required")
			client.extendedChannel = false
		} else if msg.Flags&stratumv2.RequiresExtendedChannelsFlag == 1 {
			client.log("extended channel required")
			client.extendedChannel = true
		}
		/// TODO: figure out sv2 uas
		client.UserAgent = fmt.Sprintf("%s/%s", msg.DeviceVendor, msg.DeviceHardwareVersion)

		/// write success
		client.writeSv2Msg(&stratumv2.SetupConnectionSuccess{UsedVersion: stratumv2.ProtocolVersion}, stratumv2.MessageSetupConnectionSuccess)

		/// 2. handle channel open
		frame, err = recv.DecryptFrameFromReader(client.conn)
		if err != nil {
			return
		}
		b, _ = frame.Encode()
		// client.logf("{blue}RX: (%x) %x", frame.MessageType, b)
		switch frame.MessageType {
		case stratumv2.MessageOpenStandardMiningChannel:
			{
				if client.extendedChannel {
					client.logError("requires extended channel but requested standard")
					return
				}
				msg := stratumv2.OpenStandardMiningChannel{}
				if err = msg.Decode(frame.Payload); err != nil {
					client.logErrorf("error decoding OpenStandardMiningChannel: %s", err)
					return
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
				if !client.extendedChannel {
					client.logError("requires standard channel but requested extended")
					return
				}
				msg := stratumv2.OpenExtendedMiningChannel{}
				if err = msg.Decode(frame.Payload); err != nil {
					client.logErrorf("error decoding OpenExtendedMiningChannel: %s", err)
					return
				}

				if msg.MinExtranonceSize > conf.ExtraNonce2Size {
					client.logErrorf("min extranonce size (%d) is greater than configured size (%d)", msg.MinExtranonceSize, conf.ExtraNonce2Size)
					return
				}
				if !client.validateSv2ChannelOpen(msg.RequestID, msg.MaxTarget, msg.NominalHashRate) {
					return
				}
				if !client.parseIdentity(msg.UserIdentity, msg.RequestID, nil) {
					return
				}
				client.extendedChannel = true

				initialTarget := stratumv2.U256{}
				if client.SuggestedDifficulty > 0 {
					initialTarget = diffToTarget(client.SuggestedDifficulty)
				} else {
					initialTarget = diffToTarget(conf.DefaultDifficulty)
				}
				client.writeSv2Msg(&stratumv2.OpenExtendedMiningChannelSuccess{
					OpenStandardMiningChannelSuccess: stratumv2.OpenStandardMiningChannelSuccess{
						RequestID:        msg.RequestID,
						ChannelID:        uint32(client.ID),
						Target:           initialTarget,
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
		client.processSv2Loop(ctx, client.conn)
	}
}
func (client *StratumClient) startMining() {
	globalLog(fmt.Sprintf(
		/// dig, cause gophers, get it?
		"==<<>>=<<>>=<{green}%s{/green} has joined the dig!>=<<>>=<<>>==\n\tid: {green}%s{/green}\n\taddr: {green}%s{/green}\n\tprotocol: {green}sv%d",
		client.Name(), client.ID, client.Addr(), client.protocol,
	))

	if defaultMiningAddr != nil && client.User.EncodeAddress() == defaultMiningAddr.EncodeAddress() {
		client.log("{yellow}mining to pool address")
	}
	/// the client may have suggested a difficulty before fully initialized
	/// if they haven't, we alert them to our default diff here
	if client.SuggestedDifficulty == 0 {
		if client.UserAgent == "cpuminer" || client.UserAgent == "nerdminer" {
			/// use the hardcoded min
			client.setDifficulty(constants.MIN_DIFFICULTY)
		} else {
			client.setDifficulty(conf.DefaultDifficulty)
		}
	}

	/// i dont think the order matters, but lets send the current template
	/// before adding to the client map, just in case notifyClients gets
	/// called in between (and rapid-fires jobs)
	currTemplateLock.RLock()
	if currTemplate != nil {
		client.TemplateChannel() <- currTemplate
	}
	currTemplateLock.RUnlock()
	clients.Add(client)
	client.stats.startTime = uint64(time.Now().Unix())
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
		globalLog(fmt.Sprintf("==<<>>=<<>>=<{green}%s{/green} has left the dig!>=<<>>=<<>>==", client.Name()))
	}

	client.conn.Close()

	if conf.Benchmarking {
		sharesPS := float64(client.stats.sharesAccepted+client.stats.sharesRejected) / float64(client.stats.Uptime())
		atomic.AddUint64(&totalSharesPerSec, uint64(sharesPS))
		println(fmt.Sprintf("shares/s: %f", sharesPS))
	}
	client = nil
}

func (client *StratumClient) processSv2Loop(ctx context.Context, reader io.Reader) {
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

		/// deadline is a minute + 10x target share interval
		client.conn.SetReadDeadline(time.Now().Add(time.Minute + time.Second*10*time.Duration(conf.TargetShareInterval)))

		frame, err := client.recv.DecryptFrameFromReader(client.conn)
		switch err {
		case io.ErrClosedPipe:
		case io.EOF:
			return
		case nil:
		default:
			client.logf("failed to decrypt frame: %s", err)
			return
		}

		// b, _ := frame.Encode()
		// client.logf("{blue}RX: (%x) %x", frame.MessageType, b)

		switch frame.MessageType {
		case stratumv2.MessageSubmitSharesExtended:
			{
				if !client.extendedChannel {
					client.logError("extended share submitted on standard channel")
					break
				}
				share := stratumv2.SubmitSharesExtended{}
				if err = share.Decode(frame.Payload); err != nil {
					client.logErrorf("error decoding SubmitSharesExtended: %s", err)
					break
				}
				s := &commonShare{
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
					client.logErrorf("error decoding SubmitSharesStandard: %s", err)
					break
				}
				s := &commonShare{
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
					client.logErrorf("error decoding UpdateChannel: %s", err)
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
					client.logErrorf("error decoding CloseChannel: %s", err)
					return
				}
				client.logf("leaving: %s", msg.ReasonCode)
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
			client.logErrorf("unknown method: %x", frame.MessageType)
			continue
		}
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
				client.logErrorf("%s", ne.Err)
			default:
				client.logErrorf("read error: %s", err)
			}
			return
		}

		/// process the message
		m, err := decodeStratumMessage(line)
		if err != nil {
			client.logErrorf("stratum decode error: %s", err)
			return
		}
		// client.logErrorf("%+v", m)

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
					client.logErrorf("error processing %s: %s", m.Method, err)
					client.writeSv1Msg(m.RespondError(constants.ERROR_UNPROCESSABLE))
					break
				}
				jobID, _ := strconv.ParseUint(share.JobID, 16, 64)

				client.currentJobMutex.RLock()

				s := &commonShare{
					JobID:       uint32(jobID),
					Time:        share.Time,
					Version:     uint32(client.CurrentJob.Version),
					Nonce:       share.Nonce,
					Extranonce2: share.Extranonce2,
				}
				/// BIP-310
				if share.VersionMask > -1 {
					s.Version = (s.Version & ^constants.VERSION_ROLLING_MASK) | (uint32(share.VersionMask) & constants.VERSION_ROLLING_MASK)
				}
				client.validateShareSubmission(s, m)
				client.currentJobMutex.RUnlock()
			}
		case stratum.MethodMiningConfigure:
			{
				params := stratum.MiningConfigureParams{}
				if err := params.FromRequest(m); err != nil {
					client.logErrorf("error processing %s: %s", m.Method, err)
					client.writeSv1Msg(m.RespondError(constants.ERROR_UNPROCESSABLE))
					break
				}
				res := stratum.MiningConfigureResult{}
				if params.Supports(stratum.ExtensionVersionRolling) {
					rollingConfig, err := params.GetVersionRolling()
					if err != nil {
						client.logErrorf("couldnt parse version rolling config: %s", err)
						client.writeSv1Msg(m.RespondError(constants.ERROR_UNPROCESSABLE))
						return
					}
					/// bip-310
					/// do we even care about the calced mask? its within our constant,
					// and if the client rolls outside its provided range thats a client issue
					// just tell the client its computed mask
					clientMask := constants.VERSION_ROLLING_MASK & uint32(rollingConfig.Mask)
					err = res.SetVersionRolling(stratum.VersionRollingConfigurationResult{Accepted: true, Mask: clientMask})
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
					client.logErrorf("error processing %s: %s", m.Method, err)
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
					client.logErrorf("error processing %s: %s", m.Method, err)
					client.writeSv1Msg(m.RespondError(constants.ERROR_UNPROCESSABLE))
					break
				}
				client.UserAgent = parseUserAgent(params.UserAgent)
				if client.UserAgent == "luckyminer" {
					/// unsupported
					client.writeSv1Msg(m.RespondError(constants.ERROR_NOT_ACCEPTED))
					return
				}
				if params.Extranonce1 != nil {
					client.ID = *params.Extranonce1
					client.logf("got extranonce %s", client.ID)
				}
				responseParams := stratum.MiningSubscribeResult{
					Subscriptions: []stratum.MiningSubscription{
						{
							Method:    stratum.MethodMiningNotify,
							SessionID: client.ID,
						},
					},
					Extranonce1:     client.ID,
					Extranonce2Size: uint32(conf.ExtraNonce2Size),
				}
				client.writeSv1Msg(responseParams.ToResponse(m.MessageID))
				isSubscribed = true
			}
		case stratum.MethodMiningSuggestDifficulty:
			{
				/// only accept a suggested difficulty if we haven't got one before
				if conf.IgnoreSuggDiff || client.SuggestedDifficulty > 0 {
					client.writeSv1Msg(m.RespondError(constants.ERROR_NOT_ACCEPTED))
					break
				}

				params := stratum.MiningSuggestDifficultyParams{}
				if err := params.FromRequest(m); err != nil {
					client.logErrorf("error processing %s: %s", m.Method, err)
					client.writeSv1Msg(m.RespondError(constants.ERROR_UNPROCESSABLE))
					break
				}
				suggestedDiff := math.Abs(params.Difficulty)
				if suggestedDiff >= constants.MIN_DIFFICULTY {
					/// this comment is just for visual spacing
					client.SuggestedDifficulty = suggestedDiff
					client.writeSv1Msg(stratum.NewBooleanResponse(m.MessageID, true))
					client.logf("accepted suggested difficulty {blue}%g", suggestedDiff)
				} else {
					client.writeSv1Msg(m.RespondError(constants.ERROR_NOT_ACCEPTED))
					client.logError("rejected suggested difficulty")
				}
			}
		case stratum.MethodMiningExtranonceSubscribe:
			{
				/// we dont care
				client.writeSv1Msg(stratum.NewBooleanResponse(m.MessageID, true))
			}
		default:
			{
				client.writeSv1Msg(m.RespondError(constants.ERROR_UNK_METHOD))
				client.logErrorf("unknown stratum message: %+v", m)
			}
		}

		/// we only send work after authed and subbed (and set a flag so we dont do this again)
		if isAuthed && isSubscribed && !stratumInited {
			stratumInited = true

			client.startMining()
		}

		/// deadline is a minute + 10x target share interval
		client.conn.SetReadDeadline(time.Now().Add(time.Minute + time.Second*10*time.Duration(conf.TargetShareInterval)))
	}
}

// aims for the .TargetShareInterval
// TODO: we likely need a different algo for diffs <=16, if it becomes an issue
func (client *StratumClient) calcNextDifficulty() {
	/// ignore diffs below 1, this diff routine is optimized for high-power miners
	if client.TargetDifficulty < 1.0 {
		return
	}
	if client.stats.avgSubmissionDelta == 0 {
		return
	}
	/// negative = running slow, positive = running fast
	/// round it to avoid floating point madness
	difference := math.Round(float64(conf.TargetShareInterval) - client.stats.avgSubmissionDelta/1000)
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

	client.SuggestedDifficulty = max(math.Round(client.TargetDifficulty+delta), constants.MIN_DIFFICULTY)
	/// TODO: figure out how to merge this log with the setdiff one
	client.logf("queued diff adjustment by {blue}%+g{/blue} to {blue}%g", delta, client.SuggestedDifficulty)
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
	client.logf("adjusting share target to {blue}%g", newDiff)
	return nil
}

func (client *StratumClient) TemplateChannel() chan<- *JobTemplate {
	return client.templateChan
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
		if !conf.DisableVarDiff {
			client.calcNextDifficulty()
		}

		/// both stratum specs apply diff changes to next job, so announce diff before announcing job

		if err := client.setDifficulty(client.SuggestedDifficulty); err != nil {
			if errors.Is(err, net.ErrClosed) {
				/// client died and we didnt notice?
				client.Stop()
				return
			}
			client.logErrorf("error adjusting difficulty: %s", err)
		}

		if client.protocol == 1 {
			merkleBranches := make([][]byte, len(template.MerkleBranch))
			for i, branch := range template.MerkleBranch {
				merkleBranches[i] = branch[:]
			}
			params := &stratum.MiningNotifyParams{
				JobID:          strconv.FormatUint(template.ID, 16),
				PrevBlockHash:  newJob.PrevBlock,
				MerkleBranches: merkleBranches,
				Version:        uint32(newJob.Version),
				Bits:           template.Bits[:],
				Timestamp:      newJob.Header.Timestamp,
				CoinbasePart1:  newJob.CoinbasePart1,
				CoinbasePart2:  newJob.CoinbasePart2,
				Clean:          true,
			}
			err := client.writeSv1Msg(params.ToNotification())
			if err != nil {
				client.logErrorf("error sending job: %s", err)
			}
		} else {
			prevhash := &stratumv2.SetNewPrevHash{
				ChannelID: uint32(client.ID),
				JobID:     uint32(currTemplateID),
				PrevHash:  stratumv2.U256(*newJob.PrevBlock),
				MinTime:   uint32(newJob.Header.Timestamp.Unix()), /// should be equiv to Clean=true
				Bits:      newJob.Header.Bits,
			}
			if client.extendedChannel {
				merklePath := make([]stratumv2.U256, len(template.MerkleBranch))
				for i, h := range template.MerkleBranch {
					merklePath[i] = stratumv2.U256(*h)
				}
				job := &stratumv2.NewExtendedMiningJob{
					ChannelID:             uint32(client.ID),
					JobID:                 uint32(currTemplateID),
					MinTime:               []uint32{ /* empty,provided by setprevhash */ },
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
					MinTime:    []uint32{ /* provided by setprevhash */ },
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
func (client *StratumClient) createJob(template *JobTemplate) MiningJob {
	blockHeader := wire.BlockHeader{
		Version:    template.Header.Version,
		PrevBlock:  chainhash.Hash(template.Header.PrevBlock.CloneBytes()),
		MerkleRoot: chainhash.Hash(template.Header.MerkleRoot.CloneBytes()),
		Timestamp:  template.Header.Timestamp,
		Bits:       template.Header.Bits,
		Nonce:      template.Header.Nonce,
	}

	/// copy the coinbase, we don't wanna share it now x3
	coinbaseTx := addCoinbasePayout(client.ID, client.User, template.CoinbaseTx.Copy(), template.Subsidy)

	/// serialized without the witness, we handle that on submission
	serializedCoinbaseTx := serializeCoinbaseTx(coinbaseTx)

	/// split coinbase for clients
	inputScript := coinbaseTx.TxIn[0].SignatureScript

	/// find the split point, right after the input
	partOneIndex := bytes.Index(serializedCoinbaseTx, inputScript)
	if partOneIndex < 0 {
		panic("partOneIndex shoudnt be below 0")
	}
	partOneIndex += len(inputScript)

	return MiningJob{
		ID:                     template.ID,
		Header:                 blockHeader,
		CoinbaseTx:             *coinbaseTx,
		CoinbaseBytes:          serializedCoinbaseTx,
		CoinbaseExtranonce2Idx: partOneIndex - int(conf.ExtraNonce2Size),
		Version:                blockHeader.Version,
		MerkleBranch:           template.MerkleBranch,
		MinTime:                template.MinTime,
		MaxTime:                template.MaxTime,
		NetworkDiff:            template.NetworkDiff,
		PrevBlock:              &blockHeader.PrevBlock,
		CoinbasePart1:          serializedCoinbaseTx[:partOneIndex-int(constants.EXTRANONCE_SIZE+conf.ExtraNonce2Size)],
		CoinbasePart2:          serializedCoinbaseTx[partOneIndex:],
		Bits:                   template.Bits,
	}
}
func (client *StratumClient) submitBlock(block blockSubmission) {
	client.submissionChan <- block
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
		if client.Nickname != "" || defaultMiningAddr == nil {
			client.logErrorf("failed decoding address: %s", err)
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

	/// recreate log prefixes
	client.createLogPrefixes()
	return true
}

func (client *StratumClient) createLogPrefixes() {
	nameLen := len(client.Name())
	sb := strings.Builder{}
	sb.Grow(17 + nameLen)
	sb.WriteString("[{green}")
	sb.WriteString(client.Name())
	sb.WriteString("{/green}] ")
	client.logPrefix = sb.String()

	sb.Reset()
	sb.Grow(29 + nameLen)
	sb.WriteString("{cyan}[{/cyan}")
	sb.WriteString(client.Name())
	sb.WriteString("{cyan}]{/cyan} ")
	client.errLogPrefix = sb.String()
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
		client.logf("guessed initial difficulty {blue}%s", formatDifficulty(client.SuggestedDifficulty))
	}
	return true
}
func (client *StratumClient) validateShareSubmission(share *commonShare, m *stratum.Request) {
	if share.JobID != uint32(client.CurrentJob.ID) {
		client.stats.sharesRejected++
		if share.JobID == uint32(client.CurrentJob.ID-1) {
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

	if share.ChannelID != 0 && share.ChannelID != uint32(client.ID) {
		client.stats.sharesRejected++
		client.writeSv2Msg(&stratumv2.SubmitSharesError{
			ChannelID:      uint32(client.ID),
			SequenceNumber: share.Sequence,
			ErrorCode:      stratumv2.InvalidChannelIDError,
		}, stratumv2.MessageSubmitSharesError)
		client.logError("share rejected: invalid channel ID")
		return
	}

	/// recover the mask from the final version for validation
	shareMask := (^uint32(client.CurrentJob.Version) & share.Version) | (share.Version & constants.VERSION_ROLLING_MASK)
	/// NAND the share mask with the version mask to validate
	masked := (shareMask & ^constants.VERSION_ROLLING_MASK)
	if masked != 0 {
		println(client.CurrentJob.Version, share.Version, masked)
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

	/// verify the difficulty
	/// the backing node will do the full block validation, we only care if the
	/// submission was high enough
	updatedHeader, ok := client.CurrentJob.UpdateHeader(client.ID, share)
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

	shareHash := simdHeaderHash(updatedHeader)
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
		l := diffTooLowLog{
			shareDiff:  shareDiff,
			targetDiff: client.TargetDifficulty,
		}
		client.logError(l.String())
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

	if shareDiff >= client.CurrentJob.NetworkDiff && !conf.Benchmarking {
		/// !!! block! dont say ANYTHING until after submitted
		/// copy header and coinbase to avoid overwrites
		h := wire.BlockHeader{
			Version:    updatedHeader.Version,
			PrevBlock:  chainhash.Hash(updatedHeader.PrevBlock.CloneBytes()),
			MerkleRoot: chainhash.Hash(updatedHeader.MerkleRoot.CloneBytes()),
			Timestamp:  updatedHeader.Timestamp,
			Bits:       updatedHeader.Bits,
			Nonce:      updatedHeader.Nonce,
		}
		submission := blockSubmission{
			ClientID: client.ID,
			Header:   h,
			Coinbase: client.CurrentJob.CoinbaseTx.Copy(),
			Share:    share,
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
	l := shareAcceptLog{
		shareDiff:  shareDiff,
		targetDiff: client.TargetDifficulty,
		bestDiff:   client.stats.bestDiff,
		shareHash:  shareHash.String(),
		version:    updatedHeader.Version,
		nonce:      share.Nonce,
		id:         client.ID,
		en2:        share.Extranonce2,
		hashrate:   client.stats.HashrateMH(),
		delta:      client.stats.avgSubmissionDelta / 1000,
	}
	client.log(l.String())
}

// chatter
func (client *StratumClient) writeSv1Msg(msg stratum.Message) error {
	b, err := msg.Marshal()
	if err != nil {
		client.logErrorf("failed to marshal message: %s", err)
		return err
	}

	client.logErrorf("%s", b)

	return client.writeConn(b)
}
func (client *StratumClient) writeSv2Msg(payload stratumv2.Codable, messageType stratumv2.MessageType) error {
	b, err := payload.Encode()
	if err != nil {
		client.logErrorf("failed to encode payload: %s", err)
		return err
	}
	frame := stratumv2.Frame{
		MessageType:   messageType,
		ExtensionType: stratumv2.ExtensionTypeCore,
		MessageLength: stratumv2.U24(len(b)),
		Payload:       b,
	}
	enc, err := client.send.EncryptFrame(frame)
	if err != nil {
		client.logErrorf("failed to encrypt frame: %s", err)
		return err
	}
	// f, _ := frame.Encode()
	// client.logf("{green}TX: (%x) %x", frame.MessageType, f)
	return client.writeConn(enc)
}
func (client *StratumClient) writeConn(b []byte) error {
	/// if it takes 3 seconds to write somethings fucked
	client.conn.SetWriteDeadline(time.Now().Add(time.Second * 3))
	_, err := client.conn.Write(b)
	return err
}

// logging
func (client *StratumClient) logf(s string, a ...any) {
	s = fmt.Sprintf(s, a...)
	client.log(s)
}
func (client *StratumClient) log(s string) {
	sb := strings.Builder{}
	sb.Grow(len(s) + 32)
	sb.WriteString(client.logPrefix)
	sb.WriteString(s)
	globalLog(sb.String())
}
func (client *StratumClient) logErrorf(s string, a ...any) {
	client.logError(fmt.Sprintf(s, a...))
}
func (client *StratumClient) logError(s string) {
	sb := strings.Builder{}
	sb.Grow(len(s) + 32)
	sb.WriteString(client.errLogPrefix)
	sb.WriteString(s)
	globalLogError(sb.String())
}

// used for hashrate calc
type timeSlot struct {
	startTime uint64
	accDiff   uint64 // accumulated difficulty, used for hashrate calc
}

// stats for the api
type StratumClientStats struct {
	lastTimeSlot,
	currTimeSlot timeSlot
	lastSubmissionTime int64 // used for calcing delta between (valid) `mining.submit`s, in ms
	sharesAccepted,
	sharesRejected uint64
	avgSubmissionDelta, // in ms
	bestDiff,
	hashrate float64
	startTime uint64 // time the client subscribed
}

func (stats *StratumClientStats) update(currTargetDiff float64) {
	now := time.Now().UnixMilli()
	if stats.lastSubmissionTime > 0 {
		/// exponential moving average
		/// wikipedia my beloved
		/// https://en.wikipedia.org/wiki/Exponential_smoothing
		delta := float64(now - stats.lastSubmissionTime)
		/// start the avg calc with the target delta, not 0
		if stats.avgSubmissionDelta == 0 {
			stats.avgSubmissionDelta = float64(conf.TargetShareInterval)
		}
		/// avg = smoothing*delta + (1-smoothing)*avg
		stats.avgSubmissionDelta =
			0.01*delta + 0.99*stats.avgSubmissionDelta
	}

	stats.calcHashrate(uint64(now/1000), currTargetDiff)
	stats.lastSubmissionTime = now
}

// getters
func (stats *StratumClientStats) Uptime() uint64 {
	return uint64(time.Since(time.Unix(int64(stats.startTime), 0)).Seconds())
}
func (stats *StratumClientStats) HashrateMH() float64 {
	return stats.hashrate / 1e6
}
func (stats *StratumClientStats) HashrateH() float64 {
	return stats.hashrate
}

// live hashrate in H/s
func (stats *StratumClientStats) calcHashrate(shareTime uint64, currTargetDiff float64) {
	/// calc copied from public-pool
	windowStart := (shareTime / constants.HASHRATE_WINDOW) * constants.HASHRATE_WINDOW
	/// first call, make the current slot (and set the last as the init time)
	if stats.currTimeSlot.startTime <= 0 {
		stats.currTimeSlot.startTime = windowStart
		stats.lastTimeSlot.startTime = stats.startTime
		/// if we're in the next chunk of time, snapshot the curr* and move over
	} else if stats.currTimeSlot.startTime != windowStart {
		stats.lastTimeSlot = stats.currTimeSlot

		stats.currTimeSlot.accDiff = uint64(currTargetDiff)
		stats.currTimeSlot.startTime = windowStart
		/// otherwise just update stats
	} else {
		/// we wanna use the target difficulty for a stable number
		stats.currTimeSlot.accDiff += uint64(currTargetDiff)
		if stats.currTimeSlot.accDiff > 0 {
			/// "Hashrate = (share difficulty x 2^32) / time" - ben
			/// "2^32 represents the average number of hash attempts needed to find a valid hash at difficulty 1." - skot
			time := shareTime - stats.lastTimeSlot.startTime
			/// sum the two time slots for the total accumulated diff
			stats.hashrate = float64((stats.lastTimeSlot.accDiff+stats.currTimeSlot.accDiff)*4_294_967_296) / float64(time)
		}
	}
}

// clients are given an id, a job, and a channel to submit blocks on
func createClient(conn net.Conn, submissionChannel chan<- blockSubmission) *StratumClient {
	client := &StratumClient{
		ID:             clientIDHash(conn.LocalAddr().String(), conn.RemoteAddr().String()),
		stats:          StratumClientStats{},
		conn:           conn,
		templateChan:   make(chan *JobTemplate, 1),
		submissionChan: submissionChannel,
		// allocate enough space to store the expected number of share hashes before a new job is sent out
		// 15 is a good starter, default config results in 12 shares per job and the map will grow as needed
		shareHashes: make(map[chainhash.Hash]struct{}, 15),
	}
	client.createLogPrefixes()
	return client
}
