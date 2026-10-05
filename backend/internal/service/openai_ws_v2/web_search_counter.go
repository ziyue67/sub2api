package openai_ws_v2

import (
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
)

type webSearchCallCounter struct {
	seen  map[string]struct{}
	count int
}

func (c *webSearchCallCounter) Observe(message []byte, eventType string) {
	if c == nil || len(message) == 0 || !gjson.ValidBytes(message) {
		return
	}
	eventType = strings.ToLower(strings.TrimSpace(eventType))
	switch eventType {
	case "response.output_item.done":
		item := gjson.GetBytes(message, "item")
		index := gjson.GetBytes(message, "output_index")
		c.observeItem(item, int(index.Int()), index.Exists())
	case "response.completed", "response.done":
		output := gjson.GetBytes(message, "response.output")
		if !output.IsArray() {
			output = gjson.GetBytes(message, "output")
		}
		index := 0
		output.ForEach(func(_, item gjson.Result) bool {
			c.observeItem(item, index, true)
			index++
			return true
		})
	}
}

func (c *webSearchCallCounter) observeItem(item gjson.Result, outputIndex int, hasOutputIndex bool) {
	if c == nil || strings.ToLower(strings.TrimSpace(item.Get("type").String())) != "web_search_call" {
		return
	}
	status := strings.ToLower(strings.TrimSpace(item.Get("status").String()))
	if status != "" && status != "completed" {
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
	if len(aliases) == 0 {
		return
	}
	if c.seen == nil {
		c.seen = make(map[string]struct{})
	}
	for _, alias := range aliases {
		if _, exists := c.seen[alias]; exists {
			return
		}
	}
	for _, alias := range aliases {
		c.seen[alias] = struct{}{}
	}
	c.count++
}

func (c *webSearchCallCounter) Take() int {
	if c == nil {
		return 0
	}
	count := c.count
	c.Reset()
	return count
}

func (c *webSearchCallCounter) Count() int {
	if c == nil {
		return 0
	}
	return c.count
}

func (c *webSearchCallCounter) Reset() {
	if c == nil {
		return
	}
	c.seen = make(map[string]struct{})
	c.count = 0
}
