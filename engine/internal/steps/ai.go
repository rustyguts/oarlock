package steps

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// AIPrompt sends one prompt to an LLM using a BYO API key
// (anthropic | openai | openrouter) and returns the text response.
type AIPrompt struct {
	svc *Services
}

func (e *AIPrompt) Execute(ctx context.Context, in TaskInput) (TaskOutput, error) {
	keyName, _ := in.Config["api_key"].(string)
	model, _ := in.Config["model"].(string)
	prompt, _ := in.Config["prompt"].(string)
	system, _ := in.Config["system"].(string)
	if keyName == "" || model == "" || prompt == "" {
		return TaskOutput{}, fmt.Errorf("ai.prompt: api_key, model, and prompt are required")
	}
	maxTokens := int(toFloat(in.Config["max_tokens"]))
	if maxTokens <= 0 {
		maxTokens = 1024
	}

	provider, key, err := e.svc.Secrets.APIKey(ctx, in.WorkspaceID, keyName)
	if err != nil {
		return TaskOutput{}, fmt.Errorf("ai.prompt: %w", err)
	}

	in.Log.Info("ai prompt", "provider", provider, "model", model)
	var text string
	switch provider {
	case "anthropic":
		text, err = anthropicMessage(ctx, key, model, system, prompt, maxTokens)
	case "openai":
		text, err = openAIChat(ctx, "https://api.openai.com/v1/chat/completions", key, model, system, prompt, maxTokens)
	case "openrouter":
		text, err = openAIChat(ctx, "https://openrouter.ai/api/v1/chat/completions", key, model, system, prompt, maxTokens)
	default:
		return TaskOutput{}, fmt.Errorf("ai.prompt: api key %q has unsupported provider %q", keyName, provider)
	}
	if err != nil {
		return TaskOutput{}, fmt.Errorf("ai.prompt: %w", err)
	}
	in.Log.Info("ai prompt done", "provider", provider, "chars", len(text))
	return TaskOutput{Data: map[string]any{"text": text, "model": model, "provider": provider}}, nil
}

func anthropicMessage(ctx context.Context, key, model, system, prompt string, maxTokens int) (string, error) {
	body := map[string]any{
		"model":      model,
		"max_tokens": maxTokens,
		"messages":   []map[string]any{{"role": "user", "content": prompt}},
	}
	if system != "" {
		body["system"] = system
	}
	raw, err := postJSON(ctx, "https://api.anthropic.com/v1/messages", body, map[string]string{
		"x-api-key":         key,
		"anthropic-version": "2023-06-01",
	})
	if err != nil {
		return "", err
	}
	var resp struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", err
	}
	for _, c := range resp.Content {
		if c.Type == "text" {
			return c.Text, nil
		}
	}
	return "", fmt.Errorf("anthropic: no text content in response")
}

func openAIChat(ctx context.Context, url, key, model, system, prompt string, maxTokens int) (string, error) {
	messages := []map[string]any{}
	if system != "" {
		messages = append(messages, map[string]any{"role": "system", "content": system})
	}
	messages = append(messages, map[string]any{"role": "user", "content": prompt})
	raw, err := postJSON(ctx, url, map[string]any{
		"model":                 model,
		"max_completion_tokens": maxTokens,
		"messages":              messages,
	}, map[string]string{"Authorization": "Bearer " + key})
	if err != nil {
		return "", err
	}
	var resp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}
	return resp.Choices[0].Message.Content, nil
}

func postJSON(ctx context.Context, url string, body any, headers map[string]string) ([]byte, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		msg := string(raw)
		if len(msg) > 500 {
			msg = msg[:500]
		}
		return nil, fmt.Errorf("%s returned %d: %s", url, resp.StatusCode, msg)
	}
	return raw, nil
}
