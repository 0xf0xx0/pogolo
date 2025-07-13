package stratumclient

import (
	stratum "github.com/kbnchk/go-Stratum"
)
func DecodeStratumMessage(msg []byte) (*stratum.Request, error) {
	var m stratum.Request
	if err := m.Unmarshal(msg); err != nil {
		return nil, err
	}
	return &m, nil
}
