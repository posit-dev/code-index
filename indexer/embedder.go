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

// Embedder generates vector embeddings from text.
type Embedder interface {
	// EmbedDocument generates an embedding for a document (for indexing).
	EmbedDocument(ctx context.Context, text string) ([]float32, error)
	// EmbedQuery generates an embedding for a search query.
	// Some models optimize differently for queries vs documents.
	EmbedQuery(ctx context.Context, text string) ([]float32, error)
	// Name returns a human-readable name for this embedder.
	Name() string
}

// --- OpenAI-compatible embedder ---

// OpenAIEmbedder uses any OpenAI-compatible embeddings API.
// Works with OpenAI, Ollama, Together AI, LM Studio, vLLM, etc.
type OpenAIEmbedder struct {
	client *openaiClient
	model  string
}

// NewOpenAIEmbedder creates an embedder using the OpenAI embeddings API.
func NewOpenAIEmbedder(model, baseURL, apiKeyEnv string) (*OpenAIEmbedder, error) {
	if model == "" {
		return nil, fmt.Errorf("embeddings.model must be configured in .code-index.json")
	}
	client, err := newOpenAIClient(baseURL, apiKeyEnv)
	if err != nil {
		return nil, err
	}
	return &OpenAIEmbedder{client: client, model: model}, nil
}

func (e *OpenAIEmbedder) Name() string {
	return fmt.Sprintf("OpenAI (%s, %s)", e.client.baseURL, e.model)
}

// EmbedDocument and EmbedQuery produce identical embeddings — the OpenAI
// embeddings API has no document/query type distinction.
func (e *OpenAIEmbedder) EmbedDocument(ctx context.Context, text string) ([]float32, error) {
	return e.embed(ctx, text)
}

func (e *OpenAIEmbedder) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	return e.embed(ctx, text)
}

func (e *OpenAIEmbedder) embed(ctx context.Context, text string) ([]float32, error) {
	reqBody := map[string]interface{}{
		"model": e.model,
		"input": text,
	}

	respBody, err := e.client.post(ctx, "/embeddings", reqBody)
	if err != nil {
		return nil, fmt.Errorf("OpenAI embeddings: %w", err)
	}

	var result struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parsing embeddings response: %w", err)
	}

	if len(result.Data) == 0 || len(result.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("empty embeddings response")
	}

	return result.Data[0].Embedding, nil
}

// --- Bedrock Cohere embedder ---

// BedrockCohereEmbedder uses Cohere embedding models via AWS Bedrock.
type BedrockCohereEmbedder struct {
	client  *bedrockruntime.Client
	modelID string
}

type cohereEmbedRequest struct {
	Texts          []string `json:"texts"`
	InputType      string   `json:"input_type"`
	EmbeddingTypes []string `json:"embedding_types"`
}

type cohereEmbedResponse struct {
	Embeddings struct {
		Float [][]float32 `json:"float"`
	} `json:"embeddings"`
}

// NewBedrockEmbedder creates an embedder using a Bedrock embedding model.
func NewBedrockEmbedder(ctx context.Context, model, region string) (*BedrockCohereEmbedder, error) {
	if region == "" {
		region = "us-east-1"
	}
	if model == "" {
		return nil, fmt.Errorf("embeddings.model must be configured in .code-index.json")
	}

	opts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(region),
	}

	cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}

	client := bedrockruntime.NewFromConfig(cfg)

	return &BedrockCohereEmbedder{
		client:  client,
		modelID: model,
	}, nil
}

func (e *BedrockCohereEmbedder) Name() string {
	return fmt.Sprintf("Bedrock (%s)", e.modelID)
}

func (e *BedrockCohereEmbedder) EmbedDocument(ctx context.Context, text string) ([]float32, error) {
	return e.embed(ctx, text, "search_document")
}

func (e *BedrockCohereEmbedder) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	return e.embed(ctx, text, "search_query")
}

func (e *BedrockCohereEmbedder) embed(ctx context.Context, text, inputType string) ([]float32, error) {
	reqBody := cohereEmbedRequest{
		Texts:          []string{text},
		InputType:      inputType,
		EmbeddingTypes: []string{"float"},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	input := &bedrockruntime.InvokeModelInput{
		ModelId:     aws.String(e.modelID),
		ContentType: aws.String("application/json"),
		Accept:      aws.String("application/json"),
		Body:        body,
	}

	// Retry with exponential backoff on transient errors.
	var resp *bedrockruntime.InvokeModelOutput
	backoff := 500 * time.Millisecond
	for attempt := 0; attempt < 8; attempt++ {
		resp, err = e.client.InvokeModel(ctx, input)
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
		return nil, fmt.Errorf("invoking Bedrock model: %w", err)
	}
	if err != nil {
		return nil, fmt.Errorf("invoking Bedrock model after retries: %w", err)
	}

	var result cohereEmbedResponse
	if err := json.Unmarshal(resp.Body, &result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	if len(result.Embeddings.Float) == 0 {
		return nil, fmt.Errorf("empty embeddings response")
	}

	return result.Embeddings.Float[0], nil
}

// --- Factory ---

// NewEmbedder creates an embedder based on the provider configuration.
func NewEmbedder(ctx context.Context, cfg EmbeddingsConfig, awsRegion string) (Embedder, error) {
	switch cfg.Provider {
	case "openai":
		return NewOpenAIEmbedder(cfg.Model, cfg.BaseURL, cfg.APIKeyEnv)
	case "bedrock", "":
		return NewBedrockEmbedder(ctx, cfg.Model, awsRegion)
	default:
		return nil, fmt.Errorf("unknown embedding provider %q (supported: \"bedrock\", \"openai\")", cfg.Provider)
	}
}

// BuildEmbeddingText constructs the text to embed for an entry.
func BuildEmbeddingText(name, signature, summary, doc, file string) string {
	var parts []string
	parts = append(parts, name)
	parts = append(parts, signature)
	if summary != "" {
		parts = append(parts, summary)
	}
	if doc != "" {
		parts = append(parts, doc)
	}
	parts = append(parts, file)
	return strings.Join(parts, "\n")
}
