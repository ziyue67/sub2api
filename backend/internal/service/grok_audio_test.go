package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

func TestBuildGrokVoiceURL_UsesAPIDefaultForCLIProxyBase(t *testing.T) {
	account := &Account{
		Platform: PlatformGrok,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"base_url": xai.DefaultCLIBaseURL,
		},
	}
	url, err := buildGrokVoiceURL(account, nil, "tts")
	require.NoError(t, err)
	require.Equal(t, xai.DefaultBaseURL+"/tts", url)

	url, err = buildGrokVoiceURL(account, nil, "realtime")
	require.NoError(t, err)
	require.Equal(t, xai.DefaultBaseURL+"/realtime", url)
}

func TestBuildGrokVoiceURL_EmptyBaseFallsBackToAPI(t *testing.T) {
	account := &Account{
		Platform:    PlatformGrok,
		Type:        AccountTypeOAuth,
		Credentials: map[string]any{},
	}
	url, err := buildGrokVoiceURL(account, nil, "stt")
	require.NoError(t, err)
	require.Equal(t, xai.DefaultBaseURL+"/stt", url)
}

func TestBuildGrokVoiceURL_RequiresEndpoint(t *testing.T) {
	account := &Account{Platform: PlatformGrok, Type: AccountTypeOAuth}
	_, err := buildGrokVoiceURL(account, nil, "  ")
	require.Error(t, err)
}

func TestBuildGrokVoiceURL_EncodesCustomVoicePathSegments(t *testing.T) {
	account := &Account{Platform: PlatformGrok, Type: AccountTypeOAuth}
	got, err := buildGrokVoiceURL(account, nil, "custom-voices/nlbqfwie/audio")
	require.NoError(t, err)
	require.Equal(t, xai.DefaultBaseURL+"/custom-voices/nlbqfwie/audio", got)

	_, err = buildGrokVoiceURL(account, nil, "custom-voices/../audio")
	require.Error(t, err)
}

func TestForwardGrokVoice_RejectsNonGrok(t *testing.T) {
	svc := &OpenAIGatewayService{}
	_, err := svc.ForwardGrokVoice(context.Background(), nil, &Account{Platform: PlatformOpenAI}, "tts", []byte(`{}`), "application/json")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not supported")
}

func TestAwaitGrokRealtimeAudioObservedReadsFlagAfterRelayExits(t *testing.T) {
	errCh := make(chan error, 1)
	var observed atomic.Bool
	go func() {
		observed.Store(true)
		errCh <- io.EOF
	}()
	got, err := awaitGrokRealtimeAudioObserved(errCh, &observed)
	require.ErrorIs(t, err, io.EOF)
	require.True(t, got, "audioObserved must be read after the relay returns, not before <-errCh")
}

func TestGrokRealtimeEventHasAudio(t *testing.T) {
	require.False(t, grokRealtimeEventHasAudio([]byte(`{"type":"session.created"}`)))
	require.False(t, grokRealtimeEventHasAudio([]byte(`{"type":"response.audio_transcript.delta","delta":"hi"}`)))
	require.False(t, grokRealtimeEventHasAudio([]byte(`{"type":"response.audio.delta","delta":""}`)))
	require.True(t, grokRealtimeEventHasAudio([]byte(`{"type":"response.audio.delta","delta":"abc"}`)))
	require.True(t, grokRealtimeEventHasAudio([]byte(`{"type":"response.output_audio.delta","audio":"abc"}`)))
}

func TestGrokRealtimeFrameIsAudio(t *testing.T) {
	cases := []struct {
		name string
		kind coderws.MessageType
		msg  []byte
		want bool
	}{
		{"raw pcm binary frame", coderws.MessageBinary, []byte{0x00, 0x01, 0xfe, 0xff}, true},
		{"binary frame that is a bare json number", coderws.MessageBinary, []byte("12"), true},
		{"json event sent as binary frame", coderws.MessageBinary, []byte(`{"type":"input_audio_buffer.commit"}`), false},
		{"empty binary frame", coderws.MessageBinary, nil, false},
		{"text event", coderws.MessageText, []byte(`{"type":"response.output_audio.delta","delta":"abc"}`), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, grokRealtimeFrameIsAudio(tc.kind, tc.msg))
		})
	}
}

type grokRealtimeFrame struct {
	kind coderws.MessageType
	data []byte
}

// grokRealtimeFrameUpstreamStub records frames written upstream with their type
// and replays queued upstream frames.
type grokRealtimeFrameUpstreamStub struct {
	mu       sync.Mutex
	written  []grokRealtimeFrame
	toClient chan grokRealtimeFrame
}

func (u *grokRealtimeFrameUpstreamStub) record(kind coderws.MessageType, data []byte) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.written = append(u.written, grokRealtimeFrame{kind, append([]byte(nil), data...)})
}

func (u *grokRealtimeFrameUpstreamStub) WriteJSON(_ context.Context, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	u.record(coderws.MessageText, data)
	return nil
}

func (u *grokRealtimeFrameUpstreamStub) WriteFrame(_ context.Context, kind coderws.MessageType, data []byte) error {
	u.record(kind, data)
	return nil
}

func (u *grokRealtimeFrameUpstreamStub) ReadFrame(ctx context.Context) (coderws.MessageType, []byte, error) {
	select {
	case frame := <-u.toClient:
		return frame.kind, frame.data, nil
	case <-ctx.Done():
		return coderws.MessageText, nil, ctx.Err()
	}
}

func (u *grokRealtimeFrameUpstreamStub) ReadMessage(ctx context.Context) ([]byte, error) {
	_, data, err := u.ReadFrame(ctx)
	return data, err
}

func (u *grokRealtimeFrameUpstreamStub) Ping(context.Context) error { return nil }
func (u *grokRealtimeFrameUpstreamStub) Close() error               { return nil }

func TestProxyGrokRealtimeConnPreservesBinaryAudioFrames(t *testing.T) {
	upstreamAudio := []byte{0x10, 0x00, 0xf0, 0xff, 0x7b}
	clientAudio := []byte{0x7b, 0x01, 0x02, 0x03}
	stub := &grokRealtimeFrameUpstreamStub{toClient: make(chan grokRealtimeFrame, 2)}
	stub.toClient <- grokRealtimeFrame{coderws.MessageBinary, upstreamAudio}
	stub.toClient <- grokRealtimeFrame{coderws.MessageText, []byte(`{"type":"response.done"}`)}
	svc := &OpenAIGatewayService{}
	type proxyResult struct {
		observed bool
		err      error
	}
	results := make(chan proxyResult, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		observed, proxyErr := svc.ProxyGrokRealtimeConn(r.Context(), nil, conn, &GrokRealtimeUpstream{conn: stub})
		results <- proxyResult{observed, proxyErr}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	defer func() { _ = client.CloseNow() }()

	kind, msg, err := client.Read(ctx)
	require.NoError(t, err)
	require.Equal(t, coderws.MessageBinary, kind)
	require.Equal(t, upstreamAudio, msg)
	kind, msg, err = client.Read(ctx)
	require.NoError(t, err)
	require.Equal(t, coderws.MessageText, kind)
	require.JSONEq(t, `{"type":"response.done"}`, string(msg))

	sessionUpdate := `{"type":"session.update","session":{"audio":{"input":{"transport":"binary"},"output":{"transport":"binary"}}}}`
	require.NoError(t, client.Write(ctx, coderws.MessageText, []byte(sessionUpdate)))
	require.NoError(t, client.Write(ctx, coderws.MessageBinary, clientAudio))
	require.NoError(t, client.Write(ctx, coderws.MessageBinary, []byte(`{"type":"input_audio_buffer.commit"}`)))
	require.Eventually(t, func() bool {
		stub.mu.Lock()
		defer stub.mu.Unlock()
		return len(stub.written) == 3
	}, 5*time.Second, 10*time.Millisecond)
	require.NoError(t, client.Close(coderws.StatusNormalClosure, ""))

	result := <-results
	require.Equal(t, coderws.StatusNormalClosure, coderws.CloseStatus(result.err))
	require.True(t, result.observed, "binary audio frames must count as observed audio for billing")
	stub.mu.Lock()
	defer stub.mu.Unlock()
	require.Equal(t, coderws.MessageText, stub.written[0].kind)
	require.JSONEq(t, sessionUpdate, string(stub.written[0].data))
	require.Equal(t, grokRealtimeFrame{coderws.MessageBinary, clientAudio}, stub.written[1])
	// A JSON event sent as a binary frame is still relayed as a text event.
	require.Equal(t, coderws.MessageText, stub.written[2].kind)
	require.JSONEq(t, `{"type":"input_audio_buffer.commit"}`, string(stub.written[2].data))
}

func TestForwardGrokVoice_RejectsUnknownEndpoint(t *testing.T) {
	svc := &OpenAIGatewayService{}
	_, err := svc.ForwardGrokVoice(context.Background(), nil, &Account{Platform: PlatformGrok}, "unknown", []byte(`{}`), "application/json")
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported")
}

type grokRealtimeDialerStub struct {
	status  int
	headers http.Header
	err     error
}

func (d grokRealtimeDialerStub) Dial(context.Context, string, http.Header, string) (openAIWSClientConn, int, http.Header, error) {
	return nil, d.status, d.headers, d.err
}

func TestOpenGrokRealtimePreservesTypedHandshakeBody(t *testing.T) {
	body := []byte(`{"code":"entitlement_required","error":"subscription required"}`)
	headers := http.Header{"X-Request-Id": []string{"req-realtime"}}
	svc := &OpenAIGatewayService{openaiWSPassthroughDialer: grokRealtimeDialerStub{
		status:  http.StatusForbidden,
		headers: headers,
		err:     &openAIWSHandshakeError{Body: body, Err: errors.New("handshake forbidden")},
	}}
	account := &Account{ID: 880201, Platform: PlatformGrok, Type: AccountTypeOAuth}

	_, err := svc.OpenGrokRealtime(context.Background(), account, "token", "grok-voice-latest")

	var dialErr *GrokRealtimeDialError
	require.ErrorAs(t, err, &dialErr)
	require.Equal(t, http.StatusForbidden, dialErr.StatusCode)
	require.Equal(t, body, dialErr.ResponseBody)
	require.Equal(t, "req-realtime", dialErr.ResponseHeaders.Get("X-Request-Id"))
	require.False(t, isGrokUnknownForbidden(dialErr.StatusCode, dialErr.ResponseBody))
}

type grokVoiceURLCaptureDialer struct {
	url *string
}

func (d grokVoiceURLCaptureDialer) Dial(_ context.Context, wsURL string, _ http.Header, _ string) (openAIWSClientConn, int, http.Header, error) {
	*d.url = wsURL
	return nil, http.StatusNotFound, nil, errors.New("not found")
}

func TestOpenGrokTTSStreamForwardsOnlySynthesisParams(t *testing.T) {
	var dialed string
	svc := &OpenAIGatewayService{openaiWSPassthroughDialer: grokVoiceURLCaptureDialer{url: &dialed}}
	account := &Account{ID: 880202, Platform: PlatformGrok, Type: AccountTypeOAuth}
	query := url.Values{
		"voice":       {"ara"},
		"codec":       {"pcm"},
		"sample_rate": {"16000"},
		"model":       {"grok-tts"},
		"api_key":     {"sk-client-secret"},
	}

	_, err := svc.OpenGrokTTSStream(context.Background(), account, "token", query)

	var dialErr *GrokRealtimeDialError
	require.ErrorAs(t, err, &dialErr)
	require.Equal(t, http.StatusNotFound, dialErr.StatusCode)
	require.Equal(t, "wss://api.x.ai/v1/tts?codec=pcm&language=auto&sample_rate=16000&voice=ara", dialed)
}

// grokTTSStreamUpstreamStub records events written upstream and replays queued upstream events.
type grokTTSStreamUpstreamStub struct {
	mu       sync.Mutex
	written  []string
	toClient chan []byte
}

func (u *grokTTSStreamUpstreamStub) WriteJSON(_ context.Context, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.written = append(u.written, string(data))
	return nil
}

func (u *grokTTSStreamUpstreamStub) ReadMessage(ctx context.Context) ([]byte, error) {
	select {
	case msg := <-u.toClient:
		return msg, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (u *grokTTSStreamUpstreamStub) Ping(context.Context) error { return nil }
func (u *grokTTSStreamUpstreamStub) Close() error               { return nil }

func TestProxyGrokTTSStreamConnCountsDeliveredTextDeltaCharacters(t *testing.T) {
	stub := &grokTTSStreamUpstreamStub{toClient: make(chan []byte, 1)}
	stub.toClient <- []byte(`{"type":"audio.delta","delta":"AAAA"}`)
	svc := &OpenAIGatewayService{}
	type proxyResult struct {
		chars int64
		err   error
	}
	results := make(chan proxyResult, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		chars, proxyErr := svc.ProxyGrokTTSStreamConn(r.Context(), conn, &GrokRealtimeUpstream{conn: stub})
		results <- proxyResult{chars, proxyErr}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	defer func() { _ = client.CloseNow() }()

	delta := `{"type":"text.delta","delta":"你好，world"}`
	require.NoError(t, client.Write(ctx, coderws.MessageText, []byte(delta)))
	require.NoError(t, client.Write(ctx, coderws.MessageText, []byte(`{"type":"text.done"}`)))
	kind, msg, err := client.Read(ctx)
	require.NoError(t, err)
	require.Equal(t, coderws.MessageText, kind)
	require.JSONEq(t, `{"type":"audio.delta","delta":"AAAA"}`, string(msg))
	require.Eventually(t, func() bool {
		stub.mu.Lock()
		defer stub.mu.Unlock()
		return len(stub.written) == 2
	}, 5*time.Second, 10*time.Millisecond)
	require.NoError(t, client.Close(coderws.StatusNormalClosure, ""))

	result := <-results
	require.Equal(t, coderws.StatusNormalClosure, coderws.CloseStatus(result.err))
	// "你好，world" is 8 characters; text.done carries no billable text.
	require.Equal(t, int64(8), result.chars)
	stub.mu.Lock()
	defer stub.mu.Unlock()
	require.JSONEq(t, delta, stub.written[0])
	require.JSONEq(t, `{"type":"text.done"}`, stub.written[1])
}
