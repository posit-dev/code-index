// Copyright (C) 2026 by Posit Software, PBC
package indexer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// IndexConfig defines what to index and how.
type IndexConfig struct {
	// Project name (used in descriptions and logging).
	Project string `json:"project,omitempty"`
	// Sources to index.
	Sources []SourceConfig `json:"sources"`
	// LLM configuration for doc generation.
	LLM LLMConfig `json:"llm"`
	// Embeddings configuration.
	Embeddings EmbeddingsConfig `json:"embeddings"`
	// Storage configuration for vector distribution.
	Storage StorageConfig `json:"storage"`
	// AWS configuration.
	AWS AWSConfig `json:"aws"`
	// R configuration for the native R parser.
	R RConfig `json:"r,omitempty"`
}

// RConfig defines R-specific settings for native parsing.
type RConfig struct {
	// Executable is the path to the Rscript binary. If empty, Rscript is looked up in PATH.
	Executable string `json:"executable,omitempty"`
}

// SourceConfig defines a single source to index.
type SourceConfig struct {
	// Path is the directory to scan, relative to the repo root.
	Path string `json:"path"`
	// Language overrides auto-detection. Values: "go", "typescript", "javascript".
	Language string `json:"language,omitempty"`
	// ImportPrefix is the Go module import prefix.
	// Only used for Go sources. Auto-detected from go.mod if empty.
	ImportPrefix string `json:"import_prefix,omitempty"`
	// VendorInclude lists vendored Go module paths to include.
	VendorInclude []string `json:"vendor_include,omitempty"`
	// Exclude lists glob patterns of files/dirs to skip.
	Exclude []string `json:"exclude,omitempty"`
}

// LLMConfig defines the LLM provider and model settings for doc generation.
type LLMConfig struct {
	// Provider: "bedrock" or "openai". Default: "bedrock".
	Provider string `json:"provider,omitempty"`
	// BaseURL is the API base URL (openai provider only). Default: "https://api.openai.com/v1".
	BaseURL string `json:"base_url,omitempty"`
	// APIKeyEnv is the env var name containing the API key (openai provider only). Default: "OPENAI_API_KEY".
	APIKeyEnv string `json:"api_key_env,omitempty"`
	// FunctionModel is the model for function-level summaries (high volume, fast).
	FunctionModel string `json:"function_model,omitempty"`
	// SummaryModel is the model for file and package summaries (higher quality).
	SummaryModel string `json:"summary_model,omitempty"`
}

// EmbeddingsConfig defines the embedding provider and model.
type EmbeddingsConfig struct {
	// Provider: "bedrock" or "openai". Default: "bedrock".
	Provider string `json:"provider,omitempty"`
	// BaseURL is the API base URL (openai provider only). Default: "https://api.openai.com/v1".
	BaseURL string `json:"base_url,omitempty"`
	// APIKeyEnv is the env var name containing the API key (openai provider only). Default: "OPENAI_API_KEY".
	APIKeyEnv string `json:"api_key_env,omitempty"`
	// Model is the embedding model ID.
	Model string `json:"model,omitempty"`
}

// StorageConfig defines where vectors are stored for distribution.
type StorageConfig struct {
	// S3Bucket is the S3 bucket name for vector storage.
	S3Bucket string `json:"s3_bucket,omitempty"`
	// S3Prefix is the key prefix within the bucket.
	S3Prefix string `json:"s3_prefix,omitempty"`
}

// AWSConfig defines AWS-specific settings.
type AWSConfig struct {
	// Region is the AWS region for Bedrock and S3.
	Region string `json:"region,omitempty"`
	// Account is the AWS account ID (used for profile auto-detection).
	Account string `json:"account,omitempty"`
	// Profiles is a list of AWS profile names to try when auto-detecting credentials.
	Profiles []string `json:"profiles,omitempty"`
}

// LoadConfig reads the config from .code-index.json.
// Returns an error if the config file is not found — every project must have one.
func LoadConfig(repoRoot string) (*IndexConfig, error) {
	configPath := filepath.Join(repoRoot, ".code-index.json")
	data, err := os.ReadFile(configPath)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf(".code-index.json not found in %s — create one to configure the code index", repoRoot)
	}
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	var config IndexConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("parsing .code-index.json: %w", err)
	}

	// Apply defaults.
	config.applyDefaults()

	return &config, nil
}

// applyDefaults fills in default values for empty fields.
func (c *IndexConfig) applyDefaults() {
	if c.LLM.Provider == "" {
		c.LLM.Provider = "bedrock"
	}
	if c.Embeddings.Provider == "" {
		c.Embeddings.Provider = "bedrock"
	}
	if c.AWS.Region == "" {
		c.AWS.Region = "us-east-1"
	}
	if c.Storage.S3Prefix == "" {
		c.Storage.S3Prefix = "vectors"
	}
}

// FunctionModel returns the model ID for function-level doc generation.
// Must be configured in .code-index.json under llm.function_model.
func (c *IndexConfig) FunctionModel() string {
	return c.LLM.FunctionModel
}

// SummaryModel returns the model ID for file/package doc generation.
// Must be configured in .code-index.json under llm.summary_model.
func (c *IndexConfig) SummaryModel() string {
	return c.LLM.SummaryModel
}
