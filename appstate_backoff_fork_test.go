package whatsmeow

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.mau.fi/whatsmeow/appstate"
	waBinary "go.mau.fi/whatsmeow/binary"
)

// BiaZap fork: recoverAppState (patch 1, LTHash recovery without MAC
// validation) must honour the upstream app state backoff (5403881). Without
// this, a 429 on the validated full resync is immediately followed by a second
// full fetch in recovery, hammering the server that just asked us to wait.
func TestForkHook_RecoverAppStateRespectsRateLimitBackoff(t *testing.T) {
	client := testReconnectClient()
	name := appstate.WAPatchCriticalUnblockLow
	client.appStateBackoff[name] = time.Now().Add(time.Minute)

	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("recoverAppState ignored the backoff and touched the store: %v", r)
			}
		}()
		err = client.recoverAppState(context.Background(), name)
	}()
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAppStateRateLimited), "want ErrAppStateRateLimited, got %v", err)
}

// The 429 backoff must also be recorded when it is hit during recovery, so the
// next notification-triggered sync waits instead of retrying right away.
func TestForkHook_RecordAppStateBackoffFrom429(t *testing.T) {
	client := testReconnectClient()
	name := appstate.WAPatchRegular
	errNode := &waBinary.Node{Tag: "error", Attrs: waBinary.Attrs{"code": "429", "text": "rate-overlimit", "backoff": "120"}}
	err := fmt.Errorf("wrapped: %w", &IQError{Code: 429, Text: "rate-overlimit", ErrorNode: errNode})

	client.recordAppStateBackoff(name, err)

	until := client.appStateBackoff[name]
	assert.WithinDuration(t, time.Now().Add(120*time.Second), until, 5*time.Second)

	other := appstate.WAPatchRegularHigh
	client.recordAppStateBackoff(other, &IQError{Code: 500, Text: "internal-server-error", ErrorNode: errNode})
	_, set := client.appStateBackoff[other]
	assert.False(t, set, "non-429 errors must not set a backoff")
}
