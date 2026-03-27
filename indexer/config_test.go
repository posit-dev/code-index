// Copyright (C) 2026 by Posit Software, PBC
package indexer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	t.Run("missing config file errors", func(t *testing.T) {
		_, err := LoadConfig("/nonexistent/path")
		if err == nil {
			t.Fatal("expected error for missing config")
		}
	})

	t.Run("valid config loads", func(t *testing.T) {
		dir := t.TempDir()
		configPath := filepath.Join(dir, ".code-index.json")
		err := os.WriteFile(configPath, []byte(`{
			"project": "test",
			"sources": [{"path": "src", "language": "go"}],
			"llm": {"provider": "bedrock"},
			"embeddings": {"model": "cohere.embed-v4:0"},
			"aws": {"region": "us-west-2"}
		}`), 0o644)
		if err != nil {
			t.Fatal(err)
		}

		config, err := LoadConfig(dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if config.Project != "test" {
			t.Errorf("project = %q, want %q", config.Project, "test")
		}
		if len(config.Sources) != 1 {
			t.Fatalf("sources = %d, want 1", len(config.Sources))
		}
		if config.Sources[0].Language != "go" {
			t.Errorf("language = %q, want %q", config.Sources[0].Language, "go")
		}
		if config.AWS.Region != "us-west-2" {
			t.Errorf("region = %q, want %q", config.AWS.Region, "us-west-2")
		}
	})

	t.Run("defaults applied", func(t *testing.T) {
		dir := t.TempDir()
		configPath := filepath.Join(dir, ".code-index.json")
		err := os.WriteFile(configPath, []byte(`{
			"sources": [{"path": "src", "language": "go"}]
		}`), 0o644)
		if err != nil {
			t.Fatal(err)
		}

		config, err := LoadConfig(dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if config.LLM.Provider != "bedrock" {
			t.Errorf("llm.provider default = %q, want %q", config.LLM.Provider, "bedrock")
		}
		if config.Embeddings.Provider != "bedrock" {
			t.Errorf("embeddings.provider default = %q, want %q", config.Embeddings.Provider, "bedrock")
		}
		if config.AWS.Region != "us-east-1" {
			t.Errorf("aws.region default = %q, want %q", config.AWS.Region, "us-east-1")
		}
		if config.Storage.S3Prefix != "vectors" {
			t.Errorf("storage.s3_prefix default = %q, want %q", config.Storage.S3Prefix, "vectors")
		}
	})

	t.Run("FunctionModel and SummaryModel", func(t *testing.T) {
		dir := t.TempDir()
		configPath := filepath.Join(dir, ".code-index.json")

		// With explicit models
		err := os.WriteFile(configPath, []byte(`{
			"sources": [],
			"llm": {"function_model": "my-haiku", "summary_model": "my-sonnet"}
		}`), 0o644)
		if err != nil {
			t.Fatal(err)
		}

		config, err := LoadConfig(dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if config.FunctionModel() != "my-haiku" {
			t.Errorf("FunctionModel() = %q, want %q", config.FunctionModel(), "my-haiku")
		}
		if config.SummaryModel() != "my-sonnet" {
			t.Errorf("SummaryModel() = %q, want %q", config.SummaryModel(), "my-sonnet")
		}

		// Without explicit models (returns empty — config must provide them)
		err = os.WriteFile(configPath, []byte(`{"sources": []}`), 0o644)
		if err != nil {
			t.Fatal(err)
		}

		config, err = LoadConfig(dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if config.FunctionModel() != "" {
			t.Errorf("FunctionModel() default = %q, want empty", config.FunctionModel())
		}
		if config.SummaryModel() != "" {
			t.Errorf("SummaryModel() default = %q, want empty", config.SummaryModel())
		}
	})
}
