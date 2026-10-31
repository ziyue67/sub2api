package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/requesttiming"
)

const (
	maxOpenAIConcatenatedJSONDocuments = 16
	maxOpenAIConcatenatedJSONBytes     = 16 * 1024 * 1024
)

// splitOpenAIConcatenatedJSONDocuments recognizes the narrow corruption shape
// produced when multiple complete Responses events arrive in one transport
// message. Other malformed payloads are left untouched for normal error paths.
func splitOpenAIConcatenatedJSONDocuments(payload []byte) ([][]byte, bool) {
	payload = bytes.TrimSpace(payload)
	if len(payload) == 0 || len(payload) > maxOpenAIConcatenatedJSONBytes || json.Valid(payload) {
		return nil, false
	}

	decoder := json.NewDecoder(bytes.NewReader(payload))
	documents := make([][]byte, 0, 2)
	for {
		var raw json.RawMessage
		err := decoder.Decode(&raw)
		if err != nil {
			if err == io.EOF && len(documents) > 1 {
				return documents, true
			}
			return nil, false
		}
		raw = bytes.TrimSpace(raw)
		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return nil, false
		}
		eventType := strings.TrimSpace(envelope.Type)
		if eventType == "" || strings.ContainsAny(eventType, "\r\n") {
			return nil, false
		}
		if len(documents) == maxOpenAIConcatenatedJSONDocuments {
			return nil, false
		}
		documents = append(documents, raw)
	}
}

type openAISSEJSONDocumentScanner struct {
	ctx     context.Context
	scanner *bufio.Scanner
	pending []string
	current string

	// Diagnostic state only: emitted lines and the repair queue are unchanged.
	timingEventType string
	timingData      []byte
	timingDiscard   bool
}

func newOpenAISSEJSONDocumentScanner(scanner *bufio.Scanner) *openAISSEJSONDocumentScanner {
	return &openAISSEJSONDocumentScanner{scanner: scanner}
}

func (s *openAISSEJSONDocumentScanner) Scan() (ok bool) {
	defer func() {
		if s.ctx == nil {
			return
		}
		if !ok {
			s.resetTimingEvent()
			return
		}
		s.observeTimingLine(s.current)
	}()

	if len(s.pending) > 0 {
		s.current = s.pending[0]
		s.pending = s.pending[1:]
		return true
	}
	if s.scanner == nil || !s.scanner.Scan() {
		return false
	}

	line := s.scanner.Text()
	data, ok := extractOpenAISSEDataLine(line)
	if !ok {
		s.current = line
		return true
	}
	if len(data) > maxOpenAIConcatenatedJSONBytes {
		s.current = line
		return true
	}
	documents, repaired := splitOpenAIConcatenatedJSONDocuments([]byte(data))
	if !repaired {
		s.current = line
		return true
	}

	expanded := make([]string, 0, len(documents)*3)
	for i, document := range documents {
		if i > 0 {
			var envelope struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal(document, &envelope)
			expanded = append(expanded, "event: "+strings.TrimSpace(envelope.Type))
		}
		expanded = append(expanded, "data: "+string(document), "")
	}
	s.current = expanded[0]
	s.pending = expanded[1:]
	return true
}

func (s *openAISSEJSONDocumentScanner) resetTimingEvent() {
	s.timingEventType = ""
	s.timingData = nil
	s.timingDiscard = false
}

func (s *openAISSEJSONDocumentScanner) observeTimingLine(line string) {
	if line == "" || line == "event" {
		s.resetTimingEvent()
		return
	}
	if eventType, ok := extractOpenAISSEEventLine(line); ok {
		// Some upstreams omit the blank separator before a new event. Never
		// combine an unfinished payload or a previous type with that event.
		s.resetTimingEvent()
		s.timingEventType = eventType
		return
	}
	data, ok := extractOpenAISSEDataLine(line)
	if !ok || s.timingDiscard {
		return
	}
	if len(s.timingData) > 0 {
		if len(data) >= maxOpenAIConcatenatedJSONBytes-len(s.timingData) {
			s.timingData = nil
			s.timingDiscard = true
			return
		}
		s.timingData = append(s.timingData, '\n')
	} else if len(data) > maxOpenAIConcatenatedJSONBytes {
		s.timingDiscard = true
		return
	}
	s.timingData = append(s.timingData, data...)
	if !json.Valid(s.timingData) {
		return
	}
	// Observe complete data immediately, as before, rather than waiting for a
	// separator/EOF. Incomplete multiline JSON is joined only for diagnostics.
	requesttiming.Stage(s.ctx, "sse_parsed_first")
	eventType := effectiveOpenAISSEEventType(s.timingData, s.timingEventType)
	requesttiming.Content(s.ctx, s.timingData, eventType)
	if openAIStreamDataStartsVisibleOutput(string(s.timingData), eventType) {
		requesttiming.Stage(s.ctx, "sse_visible_first")
	}
	s.timingData = nil
}

func (s *openAISSEJSONDocumentScanner) Text() string {
	return s.current
}

func (s *openAISSEJSONDocumentScanner) Err() error {
	if s.scanner == nil {
		return nil
	}
	return s.scanner.Err()
}
