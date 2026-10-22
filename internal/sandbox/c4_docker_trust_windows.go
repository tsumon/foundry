package sandbox

import "errors"

type c4DockerTrustCheck interface{ Check() error }
type c4DockerOSFS struct{}
type c4DockerTrust struct {
	trust C4Trust
	fs    c4DockerOSFS
	uid   uint32
}

var errC4DockerTrust = errors.New("real Docker is unsupported on Windows")

func (*c4DockerTrust) Check() error { return errC4DockerTrust }
