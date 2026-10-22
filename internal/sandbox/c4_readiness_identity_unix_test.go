//go:build !windows

package sandbox

import (
	"context"
	"io"
	"testing"
)

func TestC4ReadinessPinsTrustIdentityAcrossSequence(t *testing.T) {
	for _, path := range []string{"/trusted/docker", "/trusted/socket"} {
		t.Run(path, func(t *testing.T) {
			p, c, steps := c4ReadinessFixture()
			tr, fs := c4TrustFixture()
			p.DockerPath = tr.trust.DockerPath
			p.Endpoint = tr.trust.Endpoint
			c.Trust = tr.trust
			calls := 0
			process := c4ReadinessProcessFunc(func(_ context.Context, _ C4Command, out, _ io.Writer) (int, error) {
				io.WriteString(out, steps[calls].out)
				calls++
				node := fs.nodes[path]
				node.stat.Ino++
				fs.nodes[path] = node
				return 0, nil
			})
			if _, err := checkC4DockerReadiness(context.Background(), p, c, process, tr); err == nil || calls != 1 {
				t.Fatal("replaced identity accepted", err, calls)
			}
		})
	}
}
