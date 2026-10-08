// A synthetic upstream for isolated integration checks. It never contacts a model.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
)

type task struct {
	Prompt string `json:"prompt"`
	Grader struct {
		Kind     string          `json:"kind"`
		Expected json.RawMessage `json:"expected"`
		Required []string        `json:"required_record_ids"`
	} `json:"grader"`
}

func main() {
	var suite struct {
		Tasks []task `json:"tasks"`
	}
	raw, err := os.ReadFile(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	if err = json.Unmarshal(raw, &suite); err != nil {
		log.Fatal(err)
	}
	var count atomic.Int32
	http.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]int32{"submissions": count.Load()})
	})
	http.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"object":"list","data":[{"id":"gpt-5.4","object":"model"}]}`)
	})
	http.HandleFunc("/v1/responses", func(w http.ResponseWriter, r *http.Request) {
		sequence := count.Add(1)
		var request struct {
			Model     string            `json:"model"`
			Reasoning map[string]string `json:"reasoning"`
			Input     []struct {
				Type    string `json:"type"`
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"input"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&request) != nil || len(request.Input) == 0 || len(request.Input[0].Content) == 0 {
			http.Error(w, "invalid fixture request", 400)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Request-Id", fmt.Sprintf("fixture-%d", sequence))
		if r.Header.Get("Authorization") == "Bearer fixture-incomplete" {
			_, _ = fmt.Fprint(w, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_incomplete\"}}\n\n")
			return
		}
		prompt := request.Input[0].Content[0].Text
		answer := strings.TrimPrefix(prompt, "Reply with exactly this string and nothing else: ")
		output := make([]any, 0)
		if answer == prompt {
			answer = "{}"
			for _, candidate := range suite.Tasks {
				if candidate.Prompt != prompt {
					continue
				}
				answer = string(candidate.Grader.Expected)
				if candidate.Grader.Kind == "tool_json" && len(request.Input) == 1 {
					output = append(output, map[string]any{"type": "reasoning", "encrypted_content": "fixture-opaque-reasoning"})
					for index, id := range candidate.Grader.Required {
						args, _ := json.Marshal(map[string]string{"record_id": id})
						output = append(output, map[string]any{"type": "function_call", "name": "fixture.read_record", "call_id": fmt.Sprintf("fixture-call-%d", index), "arguments": string(args)})
					}
				}
				break
			}
		}
		if len(output) == 0 {
			output = append(output, map[string]any{"type": "message", "role": "assistant", "id": "msg_fixture", "content": []any{map[string]string{"type": "output_text", "text": answer}}})
		}
		response := map[string]any{"id": fmt.Sprintf("resp_fixture_%d", sequence), "status": "completed", "model": request.Model, "reasoning": request.Reasoning, "output": output, "usage": map[string]any{"input_tokens": 100, "output_tokens": 20, "input_tokens_details": map[string]int{"cached_tokens": 50}}}
		encoded, _ := json.Marshal(map[string]any{"type": "response.completed", "response": response})
		_, _ = fmt.Fprintf(w, "data: %s\n\n", encoded)
	})
	log.Fatal(http.ListenAndServe(":8087", nil))
}
