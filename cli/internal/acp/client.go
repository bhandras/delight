package acp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/bhandras/delight/protocol/logger"
)

type Client struct {
	baseURL   string
	agentName string
	debug     bool
	http      *http.Client
}

type RunResult struct {
	RunID        string
	Status       string
	AwaitRequest map[string]interface{}
	OutputText   string
}

type runRequest struct {
	AgentName string    `json:"agent_name"`
	SessionID string    `json:"session_id,omitempty"`
	Input     []Message `json:"input"`
	Mode      string    `json:"mode,omitempty"`
}

type runResumeRequest struct {
	RunID       string `json:"run_id"`
	AwaitResume any    `json:"await_resume"`
	Mode        string `json:"mode"`
}

type RunResponse struct {
	RunID        string                 `json:"run_id"`
	Status       string                 `json:"status"`
	AwaitRequest map[string]interface{} `json:"await_request"`
	Output       []Message              `json:"output"`
}

type Message struct {
	Role  string        `json:"role"`
	Parts []MessagePart `json:"parts"`
}

type MessagePart struct {
	ContentType     string `json:"content_type"`
	Content         string `json:"content"`
	ContentEncoding string `json:"content_encoding"`
}

func NewClient(baseURL, agentName string, debug bool) *Client {
	return &Client{
		baseURL:   strings.TrimRight(baseURL, "/"),
		agentName: agentName,
		debug:     debug,
		http:      &http.Client{Timeout: 60 * time.Second},
	}
}

func (c *Client) Run(ctx context.Context, sessionID, inputText string) (*RunResult, error) {
	req := runRequest{
		AgentName: c.agentName,
		SessionID: sessionID,
		Input: []Message{
			{
				Role: "user",
				Parts: []MessagePart{
					{
						ContentType:     "text/plain",
						Content:         inputText,
						ContentEncoding: "plain",
					},
				},
			},
		},
		Mode: "sync",
	}
	return c.doRun(ctx, "/runs", req)
}

func (c *Client) Resume(ctx context.Context, runID string, awaitResume any) (*RunResult, error) {
	req := runResumeRequest{
		RunID:       runID,
		AwaitResume: awaitResume,
		Mode:        "sync",
	}
	return c.doRun(ctx, "/runs/"+runID, req)
}

func (c *Client) doRun(ctx context.Context, path string, payload interface{}) (*RunResult, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Close = true

	if c.debug {
		logger.Debugf("ACP request: POST %s", c.baseURL+path)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if c.debug {
		logger.Debugf("ACP response: %s", resp.Status)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return nil, fmt.Errorf("acp request failed: %s", resp.Status)
	}

	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "text/event-stream") {
		return c.parseEventStream(resp)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var run RunResponse
	if err := json.Unmarshal(data, &run); err != nil {
		return nil, err
	}

	outputText := extractOutputText(run.Output)
	if outputText == "" {
		var raw map[string]interface{}
		if err := json.Unmarshal(data, &raw); err == nil {
			if out, ok := raw["output"].([]interface{}); ok {
				outputText = extractOutputTextFromAny(out)
			}
		}
	}

	return &RunResult{
		RunID:        run.RunID,
		Status:       run.Status,
		AwaitRequest: run.AwaitRequest,
		OutputText:   outputText,
	}, nil
}

func (c *Client) parseEventStream(resp *http.Response) (*RunResult, error) {
	var (
		runID        string
		status       string
		awaitRequest map[string]interface{}
		output       strings.Builder
	)

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}

		var event map[string]interface{}
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			continue
		}

		eventType, _ := event["type"].(string)
		switch eventType {
		case "message.part":
			part, _ := event["part"].(map[string]interface{})
			if partText := extractPartText(part); partText != "" {
				output.WriteString(partText)
			}
		case "message.completed":
			msg, _ := event["message"].(map[string]interface{})
			if msgText := extractMessageText(msg); msgText != "" {
				output.WriteString(msgText)
			}
		case "run.awaiting":
			if run, ok := event["run"].(map[string]interface{}); ok {
				if id, ok := run["run_id"].(string); ok {
					runID = id
				}
				if st, ok := run["status"].(string); ok {
					status = st
				}
				if ar, ok := run["await_request"].(map[string]interface{}); ok {
					awaitRequest = ar
				}
			}
		case "run.completed", "run.failed", "run.cancelled":
			if run, ok := event["run"].(map[string]interface{}); ok {
				if id, ok := run["run_id"].(string); ok {
					runID = id
				}
				if st, ok := run["status"].(string); ok {
					status = st
				}
				if out, ok := run["output"].([]interface{}); ok {
					if outText := extractOutputTextFromAny(out); outText != "" {
						output.WriteString(outText)
					}
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return &RunResult{
		RunID:        runID,
		Status:       status,
		AwaitRequest: awaitRequest,
		OutputText:   output.String(),
	}, nil
}

func extractOutputText(messages []Message) string {
	var out strings.Builder
	for _, msg := range messages {
		for _, part := range msg.Parts {
			out.WriteString(extractMessagePartText(part))
		}
	}
	return out.String()
}

func extractOutputTextFromAny(messages []interface{}) string {
	var out strings.Builder
	for _, msg := range messages {
		msgMap, ok := msg.(map[string]interface{})
		if !ok {
			continue
		}
		out.WriteString(extractMessageText(msgMap))
	}
	return out.String()
}

func extractMessageText(msg map[string]interface{}) string {
	parts, _ := msg["parts"].([]interface{})
	var out strings.Builder
	for _, part := range parts {
		partMap, ok := part.(map[string]interface{})
		if !ok {
			continue
		}
		out.WriteString(extractPartText(partMap))
	}
	return out.String()
}

func extractPartText(part map[string]interface{}) string {
	ct, _ := part["content_type"].(string)
	content, _ := part["content"].(string)
	encoding, _ := part["content_encoding"].(string)
	if content == "" || !strings.HasPrefix(ct, "text/") {
		return ""
	}
	if encoding == "base64" {
		decoded, err := base64.StdEncoding.DecodeString(content)
		if err != nil {
			return ""
		}
		return string(decoded)
	}
	return content
}

func extractMessagePartText(part MessagePart) string {
	if part.Content == "" || !strings.HasPrefix(part.ContentType, "text/") {
		return ""
	}
	if part.ContentEncoding == "base64" {
		decoded, err := base64.StdEncoding.DecodeString(part.Content)
		if err != nil {
			return ""
		}
		return string(decoded)
	}
	return part.Content
}
