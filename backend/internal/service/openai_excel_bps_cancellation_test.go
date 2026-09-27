package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type bpsCancelBody struct{ cancel context.CancelFunc }

func (b *bpsCancelBody) Read([]byte) (int, error) { b.cancel(); return 0, context.Canceled }
func (b *bpsCancelBody) Close() error             { return nil }

func TestExcelBPSClientCancellationBeforeHeadersAndDuringStream(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, stage := range []string{"headers", "stream"} {
			t.Run(fmt.Sprintf("%s/stream_%t", stage, stream), func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				body, err := json.Marshal(gin.H{"model": "gpt-6-astra", "stream": stream, "input": "test"})
				require.NoError(t, err)
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(body))).WithContext(ctx)
				if stage == "stream" && stream {
					_, err = c.Writer.WriteString(": keepalive\n\n")
					require.NoError(t, err)
					c.Writer.Flush()
				}
				calls := 0
				upstream := &bpsTestUpstream{send: func(req *http.Request, _ string) (*http.Response, error) {
					calls++
					require.NoError(t, req.Body.Close())
					if stage == "headers" {
						cancel()
						return nil, &url.Error{Op: "Post", URL: "https://invalid.test", Err: context.Canceled}
					}
					return &http.Response{StatusCode: 200, Header: http.Header{}, Body: &bpsCancelBody{cancel: cancel}}, nil
				}}
				svc := openAIClientToolsTestService(nil)
				svc.httpUpstream = upstream
				result, err := svc.Forward(ctx, c, excelAccount(), body)
				require.ErrorIs(t, err, context.Canceled)
				require.Equal(t, 1, calls, "a canceled request must not be replayed")
				if stage == "headers" {
					require.Nil(t, result, "no synthetic billable result before headers")
				} else {
					require.NotNil(t, result)
					require.True(t, result.ClientDisconnect)
				}
				marked, ok := GetOpsStreamError(c)
				require.True(t, ok)
				require.Equal(t, OpsClientCanceledCode, marked.Code)
				require.Equal(t, 499, marked.IntendedStatus)
				require.True(t, marked.RequestScoped)
				require.Equal(t, !stream, marked.NonStream)
				require.NotContains(t, recorder.Body.String(), "basispoints_transport_error")
				require.NotContains(t, recorder.Body.String(), "response.failed")
				require.Less(t, recorder.Code, 400)
				_, providerFailure := c.Get(OpsUpstreamErrorMessageKey)
				require.False(t, providerFailure)
				_, attempts := c.Get(OpsUpstreamErrorsKey)
				require.False(t, attempts)
			})
		}
	}
}

func TestExcelBPSChildCancellationAndTimeoutRemainProviderFailures(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, io.ErrUnexpectedEOF} {
		t.Run(cause.Error(), func(t *testing.T) {
			body, err := json.Marshal(gin.H{"model": "gpt-6-astra", "stream": true, "input": "test"})
			require.NoError(t, err)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(body)))
			svc := openAIClientToolsTestService(&httpUpstreamRecorder{err: cause})
			_, err = svc.Forward(c.Request.Context(), c, excelAccount(), body)
			require.Error(t, err)
			require.Equal(t, 502, c.Writer.Status())
			_, canceled := GetOpsStreamError(c)
			require.False(t, canceled)
			_, failure := c.Get(OpsUpstreamErrorMessageKey)
			require.True(t, failure)
		})
	}
}

func TestExcelBPSClientCancellationRequiresMatchingIncomingCause(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
	require.True(t, isExcelBPSClientCancellation(c, fmt.Errorf("wrapped: %w", context.Canceled)))
	for _, err := range []error{nil, io.ErrUnexpectedEOF, context.DeadlineExceeded, errors.New("context canceled")} {
		require.False(t, isExcelBPSClientCancellation(c, err))
	}
}
