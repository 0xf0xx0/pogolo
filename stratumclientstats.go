package main

import (
	"time"

	"git.0xf0xx0.eth.limo/0xf0xx0/pogolo/constants"
)

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

		/// avg = smoothing*delta + (1-smoothing)*avg
		stats.avgSubmissionDelta =
			0.01*delta + 0.99*stats.avgSubmissionDelta
	} else {
		/// start the avg calc with the target delta, not 0
		stats.avgSubmissionDelta = float64(conf.TargetShareInterval * 1000)
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
