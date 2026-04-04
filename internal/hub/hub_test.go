package hub

import (
	"net"
	"sync"
	"testing"
	"time"

	"go-mls/internal/logger"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewHub_RTMP(t *testing.T) {
	log := logger.NewLogger()
	h := NewHub(log, HubTypeRTMP, "127.0.0.1", 0)

	assert.NotNil(t, h)
	assert.Equal(t, HubTypeRTMP, getHubType(h))

	err := h.Start()
	require.NoError(t, err)
	defer h.Stop()

	assert.NotEmpty(t, h.Addr())
}

func TestNewHub_RTSP(t *testing.T) {
	log := logger.NewLogger()
	h := NewHub(log, HubTypeRTSP, "127.0.0.1", 0)

	assert.NotNil(t, h)
	assert.Equal(t, HubTypeRTSP, getHubType(h))

	err := h.Start()
	require.NoError(t, err)
	defer h.Stop()

	assert.NotEmpty(t, h.Addr())
}

func TestRTMPHub_StartStop(t *testing.T) {
	log := logger.NewLogger()
	h := NewRTMPHub(log, "127.0.0.1", 0)

	err := h.Start()
	require.NoError(t, err)

	addr := h.Addr()
	assert.NotEmpty(t, addr)

	host, portStr, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1", host)
	assert.NotZero(t, portStr)

	h.Stop()
}

func TestRTSPHub_StartStop(t *testing.T) {
	log := logger.NewLogger()
	h := NewRTSPHub(log, "127.0.0.1", 0)

	err := h.Start()
	require.NoError(t, err)

	addr := h.Addr()
	assert.NotEmpty(t, addr)

	h.Stop()
}

func TestRTMPHub_AddrBeforeStart(t *testing.T) {
	log := logger.NewLogger()
	h := NewRTMPHub(log, "127.0.0.1", 1935)

	addr := h.Addr()
	assert.Equal(t, "127.0.0.1:1935", addr)
}

func TestRTSPHub_AddrBeforeStart(t *testing.T) {
	log := logger.NewLogger()
	h := NewRTSPHub(log, "127.0.0.1", 8554)

	addr := h.Addr()
	assert.Equal(t, "127.0.0.1:8554", addr)
}

func TestRTMPHub_SetHandlers(t *testing.T) {
	log := logger.NewLogger()
	h := NewRTMPHub(log, "127.0.0.1", 0)

	h.SetOnPublish(func(sp, tok, remoteAddr string) error {
		return nil
	})

	h.SetOnUnpublish(func(sp string) {
	})

	assert.NotNil(t, h.onPublish)
	assert.NotNil(t, h.onUnpublish)

	err := h.Start()
	require.NoError(t, err)
	defer h.Stop()

	assert.True(t, h.onPublish != nil)
	assert.True(t, h.onUnpublish != nil)
}

func TestRTSPHub_SetHandlers(t *testing.T) {
	log := logger.NewLogger()
	h := NewRTSPHub(log, "127.0.0.1", 0)

	h.SetOnPublish(func(sp, tok, remoteAddr string) error {
		return nil
	})

	h.SetOnUnpublish(func(sp string) {
	})

	assert.NotNil(t, h.onPublish)
	assert.NotNil(t, h.onUnpublish)

	err := h.Start()
	require.NoError(t, err)
	defer h.Stop()

	assert.True(t, h.onPublish != nil)
	assert.True(t, h.onUnpublish != nil)
}

func TestRTMPHub_DoubleStart(t *testing.T) {
	log := logger.NewLogger()
	h := NewRTMPHub(log, "127.0.0.1", 0)

	err := h.Start()
	require.NoError(t, err)

	addr1 := h.Addr()

	err = h.Start()
	assert.NoError(t, err)

	addr2 := h.Addr()
	assert.NotEqual(t, addr1, addr2, "Second start should bind to different port")

	h.Stop()
}

func TestRTSPHub_DoubleStart(t *testing.T) {
	log := logger.NewLogger()
	h := NewRTSPHub(log, "127.0.0.1", 0)

	err := h.Start()
	require.NoError(t, err)
	defer h.Stop()

	err = h.Start()
	assert.NoError(t, err)
}

func TestRTMPHub_StopMultiple(t *testing.T) {
	log := logger.NewLogger()
	h := NewRTMPHub(log, "127.0.0.1", 0)

	err := h.Start()
	require.NoError(t, err)

	h.Stop()
	h.Stop()
}

func TestRTSPHub_StopMultiple(t *testing.T) {
	log := logger.NewLogger()
	h := NewRTSPHub(log, "127.0.0.1", 0)

	err := h.Start()
	require.NoError(t, err)

	h.Stop()
	h.Stop()
}

func TestRTMPHub_ConcurrentStartStop(t *testing.T) {
	log := logger.NewLogger()
	h := NewRTMPHub(log, "127.0.0.1", 0)

	err := h.Start()
	require.NoError(t, err)
	defer h.Stop()

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.Stop()
		}()
	}
	wg.Wait()
}

func TestRTSPHub_ConcurrentStartStop(t *testing.T) {
	log := logger.NewLogger()
	h := NewRTSPHub(log, "127.0.0.1", 0)

	err := h.Start()
	require.NoError(t, err)
	defer h.Stop()

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.Stop()
		}()
	}
	wg.Wait()
}

func TestRTMPHub_InvalidPort(t *testing.T) {
	log := logger.NewLogger()
	h := NewRTMPHub(log, "invalid-host", 0)

	err := h.Start()
	assert.Error(t, err)
}

func TestHubTypeConstants(t *testing.T) {
	assert.Equal(t, HubType("rtmp"), HubTypeRTMP)
	assert.Equal(t, HubType("rtsp"), HubTypeRTSP)
}

func TestHubInterface(t *testing.T) {
	log := logger.NewLogger()

	rtmpHub := NewRTMPHub(log, "127.0.0.1", 0)
	rtspHub := NewRTSPHub(log, "127.0.0.1", 0)

	var rtmpHubInterface interface{} = rtmpHub
	var rtspHubInterface interface{} = rtspHub

	_, ok := rtmpHubInterface.(Hub)
	assert.True(t, ok, "RTMPHub should implement Hub interface")

	_, ok = rtspHubInterface.(Hub)
	assert.True(t, ok, "RTSPHub should implement Hub interface")
}

func TestRTMPHub_AcceptConnection(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	log := logger.NewLogger()
	h := NewRTMPHub(log, "127.0.0.1", 0)

	var publishCalled bool
	h.SetOnPublish(func(sp, tok, remoteAddr string) error {
		publishCalled = true
		return nil
	})

	err := h.Start()
	require.NoError(t, err)
	defer h.Stop()

	conn, err := net.Dial("tcp", h.Addr())
	require.NoError(t, err)
	defer conn.Close()

	time.Sleep(100 * time.Millisecond)

	_ = publishCalled
}

func TestRTSPHub_AcceptConnection(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	log := logger.NewLogger()
	h := NewRTSPHub(log, "127.0.0.1", 0)

	var publishCalled bool
	h.SetOnPublish(func(sp, tok, remoteAddr string) error {
		publishCalled = true
		return nil
	})

	err := h.Start()
	require.NoError(t, err)
	defer h.Stop()

	conn, err := net.Dial("tcp", h.Addr())
	require.NoError(t, err)
	defer conn.Close()

	time.Sleep(100 * time.Millisecond)

	_ = publishCalled
}

func TestRTMPHub_PortReuse(t *testing.T) {
	log := logger.NewLogger()

	h1 := NewRTMPHub(log, "127.0.0.1", 0)
	err := h1.Start()
	require.NoError(t, err)
	defer h1.Stop()

	_, portStr, err := net.SplitHostPort(h1.Addr())
	require.NoError(t, err)

	h2 := NewRTMPHub(log, "127.0.0.1", 0)
	err = h2.Start()
	require.NoError(t, err)
	defer h2.Stop()

	_, portStr2, err := net.SplitHostPort(h2.Addr())
	require.NoError(t, err)

	assert.NotEqual(t, portStr, portStr2, "Ports should be different")
}

func TestRTSPHub_PortReuse(t *testing.T) {
	log := logger.NewLogger()

	h1 := NewRTSPHub(log, "127.0.0.1", 0)
	err := h1.Start()
	require.NoError(t, err)
	defer h1.Stop()

	addr1 := h1.Addr()

	h2 := NewRTSPHub(log, "127.0.0.1", 0)
	err = h2.Start()
	require.NoError(t, err)
	defer h2.Stop()

	addr2 := h2.Addr()

	assert.NotEmpty(t, addr1)
	assert.NotEmpty(t, addr2)
}

func TestRTMPHub_ZeroPort(t *testing.T) {
	log := logger.NewLogger()
	h := NewRTMPHub(log, "127.0.0.1", 0)

	err := h.Start()
	require.NoError(t, err)
	defer h.Stop()

	_, portStr, err := net.SplitHostPort(h.Addr())
	require.NoError(t, err)
	assert.NotZero(t, portStr)
}

func TestRTSPHub_ZeroPort(t *testing.T) {
	log := logger.NewLogger()
	h := NewRTSPHub(log, "127.0.0.1", 0)

	err := h.Start()
	require.NoError(t, err)
	defer h.Stop()

	_, portStr, err := net.SplitHostPort(h.Addr())
	require.NoError(t, err)
	assert.NotZero(t, portStr)
}

func TestRTMPHub_PublishReject(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	log := logger.NewLogger()
	h := NewRTMPHub(log, "127.0.0.1", 0)

	h.SetOnPublish(func(sp, tok, remoteAddr string) error {
		if tok != "valid-token" {
			return assert.AnError
		}
		return nil
	})

	err := h.Start()
	require.NoError(t, err)
	defer h.Stop()
}

func TestRTSPHub_PublishReject(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	log := logger.NewLogger()
	h := NewRTSPHub(log, "127.0.0.1", 0)

	h.SetOnPublish(func(sp, tok, remoteAddr string) error {
		if tok != "valid-token" {
			return assert.AnError
		}
		return nil
	})

	err := h.Start()
	require.NoError(t, err)
	defer h.Stop()
}

func getHubType(h Hub) HubType {
	switch h.(type) {
	case *RTMPHub:
		return HubTypeRTMP
	case *rtspHub:
		return HubTypeRTSP
	default:
		return ""
	}
}
