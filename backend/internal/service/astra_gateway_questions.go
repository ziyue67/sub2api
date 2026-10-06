package service

import (
	"context"
	"errors"
	"strings"
)

type astraQuestion struct {
	Prompt, Expected string
	HTML             bool
}

func evaluateAstraAnswer(question astraQuestion, answer string) (string, error) {
	answer = strings.TrimSpace(answer)
	if len(answer) > 128<<10 {
		return "", errors.New("test_output_too_large")
	}
	if question.HTML {
		if !pelicanHTMLPattern.MatchString(answer) {
			return answer, errors.New("html_missing")
		}
		return answer, nil
	}
	if answer == "" || (question.Expected != "" && answer != question.Expected) {
		return answer, errors.New("answer_mismatch")
	}
	return answer, nil
}

// Only controlled lightweight source acquisition may collect without candy scoring.
type astraSourceAcquisitionKey struct{}

func WithAstraSourceAcquisition(ctx context.Context) context.Context {
	return context.WithValue(ctx, astraSourceAcquisitionKey{}, true)
}
func IsAstraSourceAcquisition(ctx context.Context) bool {
	v, _ := ctx.Value(astraSourceAcquisitionKey{}).(bool)
	return v
}
