// Copyright (C) 2026 by Posit Software, PBC
package indexer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
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

// CLIBackend uses the Claude Code CLI (`claude -p`) for LLM calls.
// This uses the developer's existing Claude Code authentication.
type CLIBackend struct {
	claudePath string
	verbose    bool
}

// NewCLIBackend creates a CLI backend, returning an error if claude is not found.
func NewCLIBackend(verbose bool) (*CLIBackend, error) {
	path, err := exec.LookPath("claude")
	if err != nil {
		return nil, fmt.Errorf("claude CLI not found in PATH: %w", err)
	}
	return &CLIBackend{claudePath: path, verbose: verbose}, nil
}

func (b *CLIBackend) Name() string { return "Claude Code CLI" }

func (b *CLIBackend) Call(model, prompt string) (string, error) {
	args := []string{
		"-p",
		"--model", model,
		"--no-session-persistence",
		"--system-prompt", "You are a code documentation assistant. You receive function signatures and doc comments as input. Generate summaries based ONLY on the information provided in the prompt. Never ask for files, tools, or additional context. Always respond directly with the requested output.",
	}

	if b.verbose {
		fmt.Fprintf(os.Stderr, "[claude-cli] Running: %s %s\n", b.claudePath, strings.Join(args, " "))
		fmt.Fprintf(os.Stderr, "[claude-cli] Prompt length: %d bytes\n", len(prompt))
	}

	cmd := exec.Command(b.claudePath, args...)
	cmd.Stdin = strings.NewReader(prompt)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		stderrStr := strings.TrimSpace(stderr.String())
		stdoutStr := strings.TrimSpace(stdout.String())
		if strings.Contains(stderrStr, "Not logged in") || strings.Contains(stderrStr, "/login") ||
			strings.Contains(stdoutStr, "Not logged in") || strings.Contains(stdoutStr, "/login") {
			return "", fmt.Errorf("claude CLI: not logged in. Run 'claude /login' first")
		}
		return "", fmt.Errorf("claude CLI (%s) error: %w\nstdout: %s\nstderr: %s", b.claudePath, err, stdoutStr, stderrStr)
	}

	result := strings.TrimSpace(stdout.String())
	if b.verbose {
		fmt.Fprintf(os.Stderr, "[claude-cli] Response length: %d bytes\n", len(result))
	}

	return result, nil
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
