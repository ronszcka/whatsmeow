package whatsmeow

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"
)

func testReconnectClient() *Client {
	jid := types.NewJID("5511999999999", types.DefaultUserServer)
	return NewClient(&store.Device{ID: &jid}, waLog.Noop)
}

func TestForkHook_AutoReconnectDelayFnNilFallbackPreserved(t *testing.T) {
	client := testReconnectClient()
	client.AutoReconnectErrors = 3

	assert.Equal(t, 6*time.Second, client.nextAutoReconnectDelay())
}

func TestForkHook_PostPairDelayFnNilFallbackPreserved(t *testing.T) {
	client := testReconnectClient()

	assert.Zero(t, client.postPairDelay())
}

func TestReconnect_UsesHookAfterFailure(t *testing.T) {
	client := testReconnectClient()
	client.MessengerConfig = &MessengerConfig{
		UserAgent:    "test-agent",
		BaseURL:      "http://127.0.0.1:1",
		WebsocketURL: "ws://127.0.0.1:1/ws",
	}
	client.AutoReconnectErrors = 3

	called := make(chan int, 1)
	client.AutoReconnectDelayFn = func(failures int) time.Duration {
		called <- failures
		return 50 * time.Millisecond
	}
	client.AutoReconnectHook = func(err error) bool {
		return false
	}

	start := time.Now()
	client.autoReconnect(context.Background())
	elapsed := time.Since(start)

	select {
	case failures := <-called:
		assert.Equal(t, 3, failures)
	default:
		t.Fatal("expected AutoReconnectDelayFn to be invoked")
	}

	require.GreaterOrEqual(t, elapsed, 50*time.Millisecond)
}
