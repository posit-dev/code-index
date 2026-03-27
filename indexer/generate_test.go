// Copyright (C) 2026 by Posit Software, PBC
package indexer

import (
	"reflect"
	"testing"
)

func TestStripCodeFences(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "no fences",
			input: `{"summaries": {"key": "value"}}`,
			want:  `{"summaries": {"key": "value"}}`,
		},
		{
			name:  "json fence",
			input: "```json\n{\"summaries\": {\"key\": \"value\"}}\n```",
			want:  `{"summaries": {"key": "value"}}`,
		},
		{
			name:  "plain fence",
			input: "```\n{\"summaries\": {\"key\": \"value\"}}\n```",
			want:  `{"summaries": {"key": "value"}}`,
		},
		{
			name:  "fence with whitespace",
			input: "  ```json\n{\"key\": \"val\"}\n```  ",
			want:  `{"key": "val"}`,
		},
		{
			name:  "no trailing fence",
			input: "```json\n{\"key\": \"val\"}",
			want:  `{"key": "val"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripCodeFences(tt.input)
			if got != tt.want {
				t.Errorf("stripCodeFences() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractFirstJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "clean JSON",
			input: `{"summaries": {"key": "value"}}`,
			want:  `{"summaries": {"key": "value"}}`,
		},
		{
			name:  "JSON with leading text",
			input: `Here is the result: {"summaries": {"key": "value"}}`,
			want:  `{"summaries": {"key": "value"}}`,
		},
		{
			name:  "JSON with trailing text",
			input: `{"summaries": {"key": "value"}} Let me know if you need more.`,
			want:  `{"summaries": {"key": "value"}}`,
		},
		{
			name:  "two JSON blocks takes first",
			input: `{"first": true} some text {"second": true}`,
			want:  `{"first": true}`,
		},
		{
			name:  "nested braces",
			input: `{"outer": {"inner": {"deep": "val"}}}`,
			want:  `{"outer": {"inner": {"deep": "val"}}}`,
		},
		{
			name:  "braces inside strings",
			input: `{"key": "value with {braces} inside"}`,
			want:  `{"key": "value with {braces} inside"}`,
		},
		{
			name:  "escaped quotes in strings",
			input: `{"key": "value with \"escaped\" quotes"}`,
			want:  `{"key": "value with \"escaped\" quotes"}`,
		},
		{
			name:  "haiku correction pattern",
			input: "```json\n{\"summaries\": {\"a\": \"b\", \"summaries\": {}}}\n```\nWait, let me correct:\n```json\n{\"summaries\": {\"a\": \"b\"}}\n```",
			want:  `{"summaries": {"a": "b", "summaries": {}}}`,
		},
		{
			name:  "no JSON",
			input: "This has no JSON at all.",
			want:  "",
		},
		{
			name:  "unclosed brace repaired",
			input: `{"key": "value"`,
			want:  `{"key": "value"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractFirstJSON(tt.input)
			if got != tt.want {
				t.Errorf("extractFirstJSON() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseSummariesResponse(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    map[string]string
		wantErr bool
	}{
		{
			name:  "clean response",
			input: `{"summaries": {"key1": "summary one", "key2": "summary two"}}`,
			want:  map[string]string{"key1": "summary one", "key2": "summary two"},
		},
		{
			name:  "code fences",
			input: "```json\n{\"summaries\": {\"key1\": \"value1\"}}\n```",
			want:  map[string]string{"key1": "value1"},
		},
		{
			name:  "nested summaries object ignored",
			input: `{"summaries": {"key1": "good summary", "key2": "another summary", "summaries": {}}}`,
			want:  map[string]string{"key1": "good summary", "key2": "another summary"},
		},
		{
			name: "real haiku nested summaries bug",
			input: `{
  "summaries": {
    "file.go::FuncA": "Does thing A.",
    "file.go::FuncB": "Does thing B.",
    "summaries": {}
  }
}`,
			want: map[string]string{"file.go::FuncA": "Does thing A.", "file.go::FuncB": "Does thing B."},
		},
		{
			name:  "leading text before JSON",
			input: `Here is the result: {"summaries": {"key": "val"}}`,
			want:  map[string]string{"key": "val"},
		},
		{
			name:  "code fence with correction pattern",
			input: "```json\n{\"summaries\": {\"a\": \"b\", \"summaries\": {}}}\n```\nWait, let me correct:\n```json\n{\"summaries\": {\"a\": \"correct\"}}\n```",
			want:  map[string]string{"a": "b"},
		},
		{
			name:    "no JSON",
			input:   "This has no JSON at all.",
			wantErr: true,
		},
		{
			name:  "empty summaries",
			input: `{"summaries": {}}`,
			want:  map[string]string{},
		},
		{
			name:    "all non-string values",
			input:   `{"summaries": {"a": 123, "b": true}}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSummariesResponse(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("parseSummariesResponse() expected error, got %v", got)
				}
				return
			}
			if err != nil {
				t.Errorf("parseSummariesResponse() unexpected error: %v", err)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseSummariesResponse() = %v, want %v", got, tt.want)
			}
		})
	}
}
