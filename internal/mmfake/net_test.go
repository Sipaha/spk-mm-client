package mmfake

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetDownFailsEveryRequest(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	a := loginAs(t, s, "alice")
	s.SetDown(true)
	assert.Equal(t, http.StatusServiceUnavailable, a.call("GET", "/api/v4/users/me", nil, nil))
	s.SetDown(false)
	assert.Equal(t, http.StatusOK, a.call("GET", "/api/v4/users/me", nil, nil))
}

func TestSetLatencyDelaysMatchingPaths(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	a := loginAs(t, s, "alice")
	s.SetLatency("/users/me", 150*time.Millisecond)
	start := time.Now()
	a.call("GET", "/api/v4/users/me", nil, nil)
	assert.GreaterOrEqual(t, time.Since(start), 150*time.Millisecond)
	s.SetLatency("/users/me", 0)
	start = time.Now()
	a.call("GET", "/api/v4/users/me", nil, nil)
	assert.Less(t, time.Since(start), 150*time.Millisecond)
}

func TestRejectResumesClosesResumedSockets(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	a := loginAs(t, s, "alice")
	c := dialWS(t, s, a.tok, "")
	id := read(t, c).Data["connection_id"].(string)
	s.DropConnections(false)
	s.RejectResumes(true)
	c2 := dialWS(t, s, a.tok, "?connection_id="+id+"&sequence_number=1")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, err := c2.Read(ctx)
	require.Error(t, err, "closed without a hello")
	fresh := dialWS(t, s, a.tok, "")
	assert.Equal(t, "hello", read(t, fresh).Event, "fresh connections still work")
}

func TestSetFailureFailsMatchingPaths(t *testing.T) {
	s := Start(Options{})
	defer s.Close()
	a := loginAs(t, s, "alice")
	s.SetFailure("/users/me", http.StatusInternalServerError)
	assert.Equal(t, http.StatusInternalServerError, a.call("GET", "/api/v4/users/me", nil, nil))
	s.SetFailure("/users/me", 0)
	assert.Equal(t, http.StatusOK, a.call("GET", "/api/v4/users/me", nil, nil))
}
