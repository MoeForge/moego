package codec

import (
	"encoding/json"
	"maps"
)

// The types below follow new-api's relaykit/dto declarations for Claude
// messages, OpenAI Responses requests and embedding responses.

type ClaudeRequest struct {
	Model              string          `json:"model"`
	Prompt             string          `json:"prompt,omitempty"`
	System             any             `json:"system,omitempty"`
	Messages           []ClaudeMessage `json:"messages,omitempty"`
	CacheControl       json.RawMessage `json:"cache_control,omitempty"`
	InferenceGeo       string          `json:"inference_geo,omitempty"`
	MaxTokens          *uint           `json:"max_tokens,omitempty"`
	MaxTokensToSample  *uint           `json:"max_tokens_to_sample,omitempty"`
	StopSequences      []string        `json:"stop_sequences,omitempty"`
	Temperature        *float64        `json:"temperature,omitempty"`
	TopP               *float64        `json:"top_p,omitempty"`
	TopK               *int            `json:"top_k,omitempty"`
	Stream             *bool           `json:"stream,omitempty"`
	Tools              any             `json:"tools,omitempty"`
	Safeguards         json.RawMessage `json:"safeguards,omitempty"`
	ContextManagement  json.RawMessage `json:"context_management,omitempty"`
	OutputConfig       json.RawMessage `json:"output_config,omitempty"`
	OutputFormat       json.RawMessage `json:"output_format,omitempty"`
	Container          json.RawMessage `json:"container,omitempty"`
	ToolChoice         any             `json:"tool_choice,omitempty"`
	Thinking           *Thinking       `json:"thinking,omitempty"`
	McpServers         json.RawMessage `json:"mcp_servers,omitempty"`
	Metadata           json.RawMessage `json:"metadata,omitempty"`
	ChatTemplateKwargs json.RawMessage `json:"chat_template_kwargs,omitempty"`
	Speed              json.RawMessage `json:"speed,omitempty"`
	ServiceTier        string          `json:"service_tier,omitempty"`
}

// UnmarshalJSON keeps the tools as raw JSON, as new-api's does.
func (c *ClaudeRequest) UnmarshalJSON(data []byte) error {
	type requestFields ClaudeRequest
	var fields struct {
		requestFields
		Tools json.RawMessage `json:"tools,omitempty"`
	}
	if err := Use.Unmarshal(data, &fields); err != nil {
		return err
	}
	*c = ClaudeRequest(fields.requestFields)
	if len(fields.Tools) > 0 && string(fields.Tools) != "null" {
		c.Tools = fields.Tools
	}
	return nil
}

type ClaudeMessage struct {
	Role         string          `json:"role"`
	Content      any             `json:"content"`
	OutputConfig json.RawMessage `json:"output_config,omitempty"`
}

type Thinking struct {
	Type         string                     `json:"type,omitempty"`
	BudgetTokens *int                       `json:"budget_tokens,omitempty"`
	Display      string                     `json:"display,omitempty"`
	BlockBinding json.RawMessage            `json:"block_binding,omitempty"`
	Extra        map[string]json.RawMessage `json:"-"`
}

// UnmarshalJSON keeps the members it does not model in Extra.
func (c *Thinking) UnmarshalJSON(data []byte) error {
	type thinkingFields Thinking
	var fields thinkingFields
	if err := Use.Unmarshal(data, &fields); err != nil {
		return err
	}
	var all map[string]json.RawMessage
	if err := Use.Unmarshal(data, &all); err != nil {
		return err
	}
	for _, known := range []string{"type", "budget_tokens", "display", "block_binding"} {
		delete(all, known)
	}
	*c = Thinking(fields)
	if len(all) > 0 {
		c.Extra = all
	}
	return nil
}

// MarshalJSON writes Extra back.
func (c Thinking) MarshalJSON() ([]byte, error) {
	type thinkingFields Thinking
	data, err := Use.Marshal(thinkingFields(c))
	if err != nil || len(c.Extra) == 0 {
		return data, err
	}
	merged := make(map[string]json.RawMessage, len(c.Extra)+4)
	maps.Copy(merged, c.Extra)
	if err := Use.Unmarshal(data, &merged); err != nil {
		return nil, err
	}
	return Use.Marshal(merged)
}

type OpenAIResponsesRequest struct {
	Model                string          `json:"model"`
	Input                json.RawMessage `json:"input,omitempty"`
	Include              json.RawMessage `json:"include,omitempty"`
	Conversation         json.RawMessage `json:"conversation,omitempty"`
	ContextManagement    json.RawMessage `json:"context_management,omitempty"`
	Instructions         json.RawMessage `json:"instructions,omitempty"`
	MaxOutputTokens      *uint           `json:"max_output_tokens,omitempty"`
	TopLogProbs          *int            `json:"top_logprobs,omitempty"`
	Metadata             json.RawMessage `json:"metadata,omitempty"`
	Moderation           json.RawMessage `json:"moderation,omitempty"`
	ParallelToolCalls    json.RawMessage `json:"parallel_tool_calls,omitempty"`
	FrequencyPenalty     json.RawMessage `json:"frequency_penalty,omitempty"`
	PresencePenalty      json.RawMessage `json:"presence_penalty,omitempty"`
	PreviousResponseID   string          `json:"previous_response_id,omitempty"`
	Reasoning            *Reasoning      `json:"reasoning,omitempty"`
	ServiceTier          string          `json:"service_tier,omitempty"`
	Store                json.RawMessage `json:"store,omitempty"`
	PromptCacheKey       json.RawMessage `json:"prompt_cache_key,omitempty"`
	PromptCacheOptions   json.RawMessage `json:"prompt_cache_options,omitempty"`
	PromptCacheRetention json.RawMessage `json:"prompt_cache_retention,omitempty"`
	SafetyIdentifier     json.RawMessage `json:"safety_identifier,omitempty"`
	Stream               *bool           `json:"stream,omitempty"`
	StreamOptions        *StreamOptions  `json:"stream_options,omitempty"`
	Temperature          *float64        `json:"temperature,omitempty"`
	Text                 json.RawMessage `json:"text,omitempty"`
	ToolChoice           json.RawMessage `json:"tool_choice,omitempty"`
	Tools                json.RawMessage `json:"tools,omitempty"`
	TopP                 *float64        `json:"top_p,omitempty"`
	Truncation           json.RawMessage `json:"truncation,omitempty"`
	User                 json.RawMessage `json:"user,omitempty"`
	MaxToolCalls         *uint           `json:"max_tool_calls,omitempty"`
	Prompt               json.RawMessage `json:"prompt,omitempty"`
	ClientMetadata       json.RawMessage `json:"client_metadata,omitempty"`
	EnableThinking       json.RawMessage `json:"enable_thinking,omitempty"`
	ThinkingBudget       json.RawMessage `json:"thinking_budget,omitempty"`
	ChatTemplateKwargs   json.RawMessage `json:"chat_template_kwargs,omitempty"`
	TopK                 json.RawMessage `json:"top_k,omitempty"`
	MinP                 json.RawMessage `json:"min_p,omitempty"`
	RepetitionPenalty    json.RawMessage `json:"repetition_penalty,omitempty"`
	Stop                 json.RawMessage `json:"stop,omitempty"`
	CacheSalt            json.RawMessage `json:"cache_salt,omitempty"`
	Preset               json.RawMessage `json:"preset,omitempty"`
}

// MarshalJSON leaves out the thinking budget, as new-api's does for models
// other than Qwen's.
func (r OpenAIResponsesRequest) MarshalJSON() ([]byte, error) {
	type Alias OpenAIResponsesRequest
	r.ThinkingBudget = nil
	return Use.Marshal((*Alias)(&r))
}

type Reasoning struct {
	Effort  string          `json:"effort,omitempty"`
	Summary string          `json:"summary,omitempty"`
	Mode    json.RawMessage `json:"mode,omitempty"`
	Context json.RawMessage `json:"context,omitempty"`
}

type OpenAIEmbeddingResponseItem struct {
	Object    string    `json:"object"`
	Index     int       `json:"index"`
	Embedding []float64 `json:"embedding"`
}

type OpenAIEmbeddingResponse struct {
	Object string                        `json:"object"`
	Data   []OpenAIEmbeddingResponseItem `json:"data"`
	Model  string                        `json:"model"`
	Usage  `json:"usage"`
}
