package service

import (
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
)

// openAIWebSearchCallCounter counts completed OpenAI Responses built-in
// web_search_call output items. Custom function calls named web_search are
// intentionally excluded because they are client tools, not OpenAI hosted
// search invocations.
type openAIWebSearchCallCounter struct {
	seen  map[string]struct{}
	count int
}

func newOpenAIWebSearchCallCounter() *openAIWebSearchCallCounter {
	return &openAIWebSearchCallCounter{seen: make(map[string]struct{})}
}

func (c *openAIWebSearchCallCounter) ObserveSSEData(data []byte) {
	if c == nil || len(data) == 0 || !gjson.ValidBytes(data) {
		return
	}
	eventType := strings.ToLower(strings.TrimSpace(gjson.GetBytes(data, "type").String()))
	switch eventType {
	case "response.output_item.done":
		item := gjson.GetBytes(data, "item")
		if !item.Exists() {
			return
		}
		c.observeItem(item, int(gjson.GetBytes(data, "output_index").Int()), gjson.GetBytes(data, "output_index").Exists(), true)
	case "response.completed", "response.done":
		output := gjson.GetBytes(data, "response.output")
		if !output.IsArray() {
			output = gjson.GetBytes(data, "output")
		}
		c.observeOutput(output, true)
	}
}

func (c *openAIWebSearchCallCounter) observeOutput(output gjson.Result, implicitlyCompleted bool) {
	if c == nil || !output.IsArray() {
		return
	}
	index := 0
	output.ForEach(func(_, item gjson.Result) bool {
		c.observeItem(item, index, true, implicitlyCompleted)
		index++
		return true
	})
}

func (c *openAIWebSearchCallCounter) observeItem(item gjson.Result, outputIndex int, hasOutputIndex, implicitlyCompleted bool) {
	if c == nil || !isCompletedOpenAIWebSearchOutputItem(item, implicitlyCompleted) {
		return
	}
	aliases := make([]string, 0, 3)
	if callID := strings.TrimSpace(item.Get("call_id").String()); callID != "" {
		aliases = append(aliases, "call:"+callID)
	}
	if id := strings.TrimSpace(item.Get("id").String()); id != "" {
		aliases = append(aliases, "id:"+id)
	}
	if hasOutputIndex && outputIndex >= 0 {
		aliases = append(aliases, "index:"+strconv.Itoa(outputIndex))
	}
	// A completed item without a stable identity cannot be safely deduplicated
	// against the terminal response. Fail closed instead of double charging.
	if len(aliases) == 0 {
		return
	}
	if c.seen == nil {
		c.seen = make(map[string]struct{})
	}
	for _, alias := range aliases {
		if _, ok := c.seen[alias]; ok {
			return
		}
	}
	for _, alias := range aliases {
		c.seen[alias] = struct{}{}
	}
	c.count++
}

func (c *openAIWebSearchCallCounter) Count() int {
	if c == nil {
		return 0
	}
	return c.count
}

func (c *openAIWebSearchCallCounter) Reset() {
	if c == nil {
		return
	}
	c.seen = make(map[string]struct{})
	c.count = 0
}

func countOpenAIWebSearchCallsFromJSONBytes(body []byte) int {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return 0
	}
	counter := newOpenAIWebSearchCallCounter()
	output := gjson.GetBytes(body, "response.output")
	if !output.IsArray() {
		output = gjson.GetBytes(body, "output")
	}
	status := strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, "response.status").String()))
	if status == "" {
		status = strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, "status").String()))
	}
	counter.observeOutput(output, status == "completed" || status == "done")
	return counter.Count()
}

func countOpenAIWebSearchCallsFromSSEBody(body string) int {
	if strings.TrimSpace(body) == "" {
		return 0
	}
	counter := newOpenAIWebSearchCallCounter()
	forEachOpenAISSEDataPayload(body, counter.ObserveSSEData)
	return counter.Count()
}

func isCompletedOpenAIWebSearchOutputItem(item gjson.Result, implicitlyCompleted bool) bool {
	if !item.Exists() || strings.ToLower(strings.TrimSpace(item.Get("type").String())) != "web_search_call" {
		return false
	}
	status := strings.ToLower(strings.TrimSpace(item.Get("status").String()))
	if status == "" {
		return implicitlyCompleted
	}
	return status == "completed"
}
