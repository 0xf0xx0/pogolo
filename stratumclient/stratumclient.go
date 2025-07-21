package stratumclient

import (
	"bufio"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"pogolo/config"
	"slices"
	"strings"
	"time"

	"github.com/btcsuite/btcd/btcutil"
	stratum "github.com/kbnchk/go-Stratum"
)

type StratumClient struct {
	ID                  stratum.ID
	Difficulty          float64
	VersionRolling      bool
	VersionRollingMask  uint32
	SuggestedDifficulty float64
	UserAgent           string
	User                *btcutil.Address
	Worker              string
	//Password            string
	config map[string]any
	conn   net.Conn
	// messages to client
	messageChan chan any
	// winning block submissions
	submissionChan chan *btcutil.Block
	CurrentJob     MiningJob
	activeJobs     []stratum.NotifyParams
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
			/// TODO?
			stratumInited = true
			/// the initial difficulty was set in CreateClient,
			/// but the client may also suggest a difficulty before
			/// fully initialized
			/// if they haven't, alert them to our default diff
			if client.SuggestedDifficulty == 0 {
				//client.AdjustDifficulty(client.Difficulty)
			}
			client.messageChan <- "ready"
		}
		line, err := reader.ReadBytes([]byte("\n")[0])
		if err != nil {
			if err != io.ErrClosedPipe {
				println(fmt.Sprintf("client read error: %s", err))
			}
			break readloop
		}
		/// TODO/FIXME: 1 min deadline? 30s? 5 min?
		client.conn.SetDeadline(time.Now().Add(time.Minute * 25))
		// println(fmt.Sprintf("data from %s (%s):\n\"\"\"\n%s\n\"\"\"", client.ID, client.conn.RemoteAddr(), strings.TrimSpace(string(line))))
		m, err := DecodeStratumMessage(line)
		if err != nil {
			println(fmt.Sprintf("client stratum decode error: %s", err))
			// println(fmt.Sprintf("%+v", client))
			break readloop
		}

		switch m.GetMethod() {
		case stratum.MiningConfigure:
			{
				/// assert the stupid type cause i cant just
				/// m.Params[0].([]string)
				supported := make([]string, 0)
				for _, param := range m.Params[0].([]interface{}) {
					supported = append(supported, param.(string))
				}
				params := stratum.ConfigureParams{
					Supported:  supported,
					Parameters: m.Params[1].(map[string]interface{}),
				}
				res := stratum.ConfigureResult{}
				if slices.Contains(params.Supported, "version-rolling") {
					if _, ok := params.Parameters["version-rolling.mask"]; ok {
						client.VersionRolling = true
						client.VersionRollingMask = config.VERSION_ROLLING_MASK

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
				if isAuthed {
					break
				}
				params := stratum.AuthorizeParams{}
				params.Read(m)
				split := strings.Split(params.Username, ".")
				decoded, err := btcutil.DecodeAddress(split[0], config.CHAIN)
				if err != nil {
					println(err.Error())
				}
				client.User = &decoded
				if len(split) > 1 {
					client.Worker = split[1]
				}
				//client.password = params.Password
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
				/// MAYBE: should we accept client extranonce1?
				params := stratum.SubscribeResult{
					Subscriptions: []stratum.Subscription{
						{
							Method:    stratum.MiningNotify,
							SessionID: client.ID,
						},
					},
					ExtraNonce1:     client.ID,
					ExtraNonce2Size: config.EXTRANONCE_SIZE,
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
				println(fmt.Sprintf("unhandled stratum message: %+v", m))
				client.writeRes(stratum.NewErrorResponse(m.MessageID, stratum.Error{Code: 501, Message: "unknown method"}))
			}
		}
	}
}

func (client *StratumClient) validateShareSubmission(s stratum.Share, m *stratum.Request) {
	updatedBlock := client.CurrentJob.Template.UpdateBlock(client, s, client.CurrentJob.CoinbaseTx)
	shareDiff, _ := CalcDifficulty(updatedBlock.Header)
	println(fmt.Sprintf("difficulty: %g (%f): %s %s", shareDiff, shareDiff, updatedBlock.Header.BlockHash(), updatedBlock.BlockHash()))
	if shareDiff >= float64(client.Difficulty) {
		if shareDiff >= client.CurrentJob.Template.NetworkDiff {
			client.submitBlock(&client.CurrentJob.Template.Block)
			println("===== BLOCK FOUND ===== BLOCK FOUND ===== BLOCK FOUND =====")
			println(fmt.Sprintf("client: %s\ndifficulty: %g\nhash: %s", client.ID, shareDiff, updatedBlock.BlockHash().String()))
		}
		client.writeRes(stratum.BooleanResponse(m.MessageID, true))
	} else {
		client.writeRes(stratum.NewErrorResponse(m.MessageID, stratum.Error{Code: 1, Message: "difficulty too low"}))
	}
}
func (client *StratumClient) Stop() {
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
func (client *StratumClient) submitBlock(block *btcutil.Block) {
	client.submissionChan <- block
}
func (client *StratumClient) writeChan(msg any) {
	client.messageChan <- msg
}
func (client *StratumClient) readChanRoutine() {
	for {
		switch m := (<-client.messageChan).(type) {
		case string:
			{
				println(m)
			}
		case *JobTemplate:
			{
				job := client.createJob(DeepCopyTemplate(m))
				client.CurrentJob = job
				client.writeNotif(job.Notification)
			}
		default:
			{
				println("closing chan")
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
	coinbaseTx := CreateCoinbaseTx(*client.User, *template, config.CHAIN)
	/// this is serialized without the witness because its given to the client
	/// we finish up the tx on a block-winning share submission
	serializedCoinbaseTx, err := SerializeTx(coinbaseTx.MsgTx(), false)
	if err != nil {
		panic(err)
	}
	inputScript := coinbaseTx.MsgTx().TxIn[0].SignatureScript
	partOneIndex := strings.Index(hex.EncodeToString(serializedCoinbaseTx), hex.EncodeToString(inputScript)) + len(inputScript)
	if (partOneIndex < 0) {
		panic("partOneIndex shoudnt be below 0")
	}
	println("bits:",hex.EncodeToString(template.Bits))
	job := MiningJob{
		Template:   template,
		CoinbaseTx: coinbaseTx,
		Notification: stratum.Notify(stratum.NotifyParams{
			JobID:          template.ID,
			PrevBlockHash:  blockHeader.PrevBlock[:],
			MerkleBranches: merkleBranches,
			Version:        uint32(blockHeader.Version),
			Bits:           template.Bits,
			Timestamp:      blockHeader.Timestamp,
			CoinbasePart1:  serializedCoinbaseTx[:partOneIndex-16],
			CoinbasePart2:  serializedCoinbaseTx[partOneIndex:],
			Clean:          template.Clear,
		}),
	}

	return job
}

// 32-bit (4-byte) hash used for client id and extranonce1
func ClientIDHash() stratum.ID {
	randomBytes := make([]byte, config.EXTRANONCE_SIZE)
	rand.Read(randomBytes)
	return stratum.ID(binary.BigEndian.Uint32(randomBytes))
}
func CreateClient(conn net.Conn, submissionChannel chan *btcutil.Block) StratumClient {
	client := StratumClient{
		conn:           conn,
		ID:             ClientIDHash(),
		messageChan:    make(chan any),
		submissionChan: submissionChannel,
		Difficulty:     config.DEFAULT_DIFFICULTY,
	}
	return client
}
func (client *StratumClient) writeRes(res stratum.Response) error {
	bytes, err := res.Marshal()
	if err != nil {
		println(fmt.Sprintf("failed to marshal response: %s", err))
		return err
	}

	return client.writeConn(bytes)
}
func (client *StratumClient) writeNotif(n stratum.Notification) error {
	bytes, err := n.Marshal()
	if err != nil {
		println(fmt.Sprintf("failed to marshal notification: %s", err))
		return err
	}

	return client.writeConn(bytes)
}
func (client *StratumClient) writeConn(b []byte) error {
	_, err := client.conn.Write(b)
	return err
}
