// Copyright (C) 2026 by Posit Software, PBC
package indexer

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
)

// LLMBackend is the interface for calling an LLM to generate summaries.
type LLMBackend interface {
	// Call sends a prompt to the LLM and returns the text response.
	// model is the full model ID as specified in .code-index.json config.
	Call(model, prompt string) (string, error)
	// Name returns a human-readable name for this backend.
	Name() string
}

// OpenAILLMBackend uses any OpenAI-compatible API for LLM calls.
// Works with OpenAI, Ollama, Together AI, Groq, Fireworks, LM Studio, vLLM, etc.
type OpenAILLMBackend struct {
	client *openaiClient
}

// NewOpenAILLMBackend creates an LLM backend using the OpenAI chat/completions API.
func NewOpenAILLMBackend(baseURL, apiKeyEnv string) (*OpenAILLMBackend, error) {
	client, err := newOpenAIClient(baseURL, apiKeyEnv)
	if err != nil {
		return nil, err
	}
	return &OpenAILLMBackend{client: client}, nil
}

func (b *OpenAILLMBackend) Name() string {
	return fmt.Sprintf("OpenAI (%s)", b.client.baseURL)
}

func (b *OpenAILLMBackend) Call(model, prompt string) (string, error) {
	reqBody := map[string]interface{}{
		"model": model,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"max_tokens": 16384,
	}

	respBody, err := b.client.post(context.Background(), "/chat/completions", reqBody)
	if err != nil {
		return "", fmt.Errorf("OpenAI chat/completions: %w", err)
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("parsing response: %w", err)
	}

	if len(result.Choices) == 0 {
		return "", fmt.Errorf("empty response (no choices)")
	}

	return strings.TrimSpace(result.Choices[0].Message.Content), nil
}

// BedrockLLMBackend uses Claude models on AWS Bedrock for LLM calls.
type BedrockLLMBackend struct {
	client *bedrockruntime.Client
}

// NewBedrockLLMBackend creates a Bedrock LLM backend.
func NewBedrockLLMBackend(region string) (*BedrockLLMBackend, error) {
	if region == "" {
		region = "us-east-1"
	}

	opts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(region),
	}

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(), opts...)
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}

	return &BedrockLLMBackend{
		client: bedrockruntime.NewFromConfig(cfg),
	}, nil
}

func (b *BedrockLLMBackend) Name() string { return "Bedrock" }

func (b *BedrockLLMBackend) Call(model, prompt string) (string, error) {
	body := map[string]interface{}{
		"anthropic_version": "bedrock-2023-05-31",
		"max_tokens":        16384,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshaling request: %w", err)
	}

	// Retry with backoff on transient errors.
	var resp *bedrockruntime.InvokeModelOutput
	backoff := 500 * time.Millisecond
	for attempt := 0; attempt < 8; attempt++ {
		resp, err = b.client.InvokeModel(context.Background(), &bedrockruntime.InvokeModelInput{
			ModelId:     aws.String(model),
			ContentType: aws.String("application/json"),
			Accept:      aws.String("application/json"),
			Body:        jsonBody,
		})
		if err == nil {
			break
		}
		errStr := err.Error()
		if strings.Contains(errStr, "ThrottlingException") ||
			strings.Contains(errStr, "TooManyRequestsException") ||
			strings.Contains(errStr, "429") ||
			strings.Contains(errStr, "424") ||
			strings.Contains(errStr, "ModelErrorException") ||
			strings.Contains(errStr, "ModelTimeoutException") ||
			strings.Contains(errStr, "StatusCode: 5") {
			jitter := time.Duration(float64(backoff) * (0.8 + 0.4*float64(attempt%3)/3.0))
			time.Sleep(jitter)
			backoff *= 2
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
			continue
		}
		return "", fmt.Errorf("invoking Bedrock model: %w", err)
	}
	if err != nil {
		return "", fmt.Errorf("invoking Bedrock model after retries: %w", err)
	}

	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(resp.Body, &result); err != nil {
		return "", fmt.Errorf("parsing Bedrock response: %w", err)
	}

	if len(result.Content) == 0 {
		return "", fmt.Errorf("empty Bedrock response")
	}

	return strings.TrimSpace(result.Content[0].Text), nil
}
