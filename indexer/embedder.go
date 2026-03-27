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

// EmbeddingDimensions is the number of dimensions for Cohere Embed v4 embeddings.
const EmbeddingDimensions = 1536

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

// BedrockCohereEmbedder uses an embedding model via AWS Bedrock.
type BedrockCohereEmbedder struct {
	client  *bedrockruntime.Client
	modelID string
}

// cohereEmbedRequest is the request body for Cohere embeddings via Bedrock.
type cohereEmbedRequest struct {
	Texts          []string `json:"texts"`
	InputType      string   `json:"input_type"`
	EmbeddingTypes []string `json:"embedding_types"`
}

// cohereEmbedResponse is the response body from Cohere embeddings.
type cohereEmbedResponse struct {
	Embeddings struct {
		Float [][]float32 `json:"float"`
	} `json:"embeddings"`
}

// NewBedrockEmbedder creates an embedder using a Bedrock embedding model.
// model is the Bedrock model ID (must be configured in .code-index.json).
// region defaults to "us-east-1" if empty.
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

// NewEmbedder creates an embedder based on the provider string.
// model is the embedding model ID. region is the AWS region for Bedrock.
func NewEmbedder(ctx context.Context, provider, model, region string) (Embedder, error) {
	switch provider {
	case "bedrock", "":
		return NewBedrockEmbedder(ctx, model, region)
	default:
		return nil, fmt.Errorf("unknown embedding provider %q (supported: \"bedrock\")", provider)
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
