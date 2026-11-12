// Code generated from ../codec/dto.go by go generate. DO NOT EDIT.

package w7

import "encoding/json"

// The request and response types below follow new-api's relaykit/dto
// declarations. Their own MarshalJSON and UnmarshalJSON methods call the
// engine under test through Use, as new-api's call its host codec.

type ResponseFormat struct {
	Type       string          `json:"type,omitempty"`
	JsonSchema json.RawMessage `json:"json_schema,omitempty"`
}

type GeneralOpenAIRequest struct {
	Model                  string            `json:"model,omitempty"`
	Messages               []Message         `json:"messages,omitempty"`
	Prompt                 any               `json:"prompt,omitempty"`
	Prefix                 any               `json:"prefix,omitempty"`
	Suffix                 any               `json:"suffix,omitempty"`
	Stream                 *bool             `json:"stream,omitempty"`
	StreamOptions          *StreamOptions    `json:"stream_options,omitempty"`
	MaxTokens              *uint             `json:"max_tokens,omitempty"`
	MaxCompletionTokens    *uint             `json:"max_completion_tokens,omitempty"`
	ReasoningEffort        string            `json:"reasoning_effort,omitempty"`
	Verbosity              json.RawMessage   `json:"verbosity,omitempty"`
	Temperature            *float64          `json:"temperature,omitempty"`
	TopP                   *float64          `json:"top_p,omitempty"`
	TopK                   *int              `json:"top_k,omitempty"`
	Stop                   any               `json:"stop,omitempty"`
	N                      *int              `json:"n,omitempty"`
	Input                  any               `json:"input,omitempty"`
	Instruction            string            `json:"instruction,omitempty"`
	Size                   string            `json:"size,omitempty"`
	Functions              json.RawMessage   `json:"functions,omitempty"`
	FrequencyPenalty       *float64          `json:"frequency_penalty,omitempty"`
	PresencePenalty        *float64          `json:"presence_penalty,omitempty"`
	ResponseFormat         *ResponseFormat   `json:"response_format,omitempty"`
	EncodingFormat         json.RawMessage   `json:"encoding_format,omitempty"`
	Seed                   *float64          `json:"seed,omitempty"`
	ParallelTooCalls       *bool             `json:"parallel_tool_calls,omitempty"`
	Tools                  []ToolCallRequest `json:"tools,omitempty"`
	ToolChoice             any               `json:"tool_choice,omitempty"`
	FunctionCall           json.RawMessage   `json:"function_call,omitempty"`
	User                   json.RawMessage   `json:"user,omitempty"`
	ServiceTier            json.RawMessage   `json:"service_tier,omitempty"`
	LogProbs               *bool             `json:"logprobs,omitempty"`
	TopLogProbs            *int              `json:"top_logprobs,omitempty"`
	Dimensions             *int              `json:"dimensions,omitempty"`
	Modalities             json.RawMessage   `json:"modalities,omitempty"`
	Audio                  json.RawMessage   `json:"audio,omitempty"`
	SafetyIdentifier       json.RawMessage   `json:"safety_identifier,omitempty"`
	Store                  json.RawMessage   `json:"store,omitempty"`
	PromptCacheKey         string            `json:"prompt_cache_key,omitempty"`
	PromptCacheOptions     json.RawMessage   `json:"prompt_cache_options,omitempty"`
	PromptCacheRetention   json.RawMessage   `json:"prompt_cache_retention,omitempty"`
	LogitBias              json.RawMessage   `json:"logit_bias,omitempty"`
	Metadata               json.RawMessage   `json:"metadata,omitempty"`
	Prediction             json.RawMessage   `json:"prediction,omitempty"`
	ExtraBody              json.RawMessage   `json:"extra_body,omitempty"`
	ImageConfig            json.RawMessage   `json:"imageConfig,omitempty"`
	SearchParameters       json.RawMessage   `json:"search_parameters,omitempty"`
	WebSearchOptions       *WebSearchOptions `json:"web_search_options,omitempty"`
	Usage                  json.RawMessage   `json:"usage,omitempty"`
	Reasoning              json.RawMessage   `json:"reasoning,omitempty"`
	VlHighResolutionImages json.RawMessage   `json:"vl_high_resolution_images,omitempty"`
	EnableThinking         json.RawMessage   `json:"enable_thinking,omitempty"`
	ThinkingBudget         json.RawMessage   `json:"thinking_budget,omitempty"`
	ChatTemplateKwargs     json.RawMessage   `json:"chat_template_kwargs,omitempty"`
	EnableSearch           json.RawMessage   `json:"enable_search,omitempty"`
	SearchOptions          json.RawMessage   `json:"search_options,omitempty"`
	Think                  json.RawMessage   `json:"think,omitempty"`
	WebSearch              json.RawMessage   `json:"web_search,omitempty"`
	THINKING               json.RawMessage   `json:"thinking,omitempty"`
	SearchDomainFilter     json.RawMessage   `json:"search_domain_filter,omitempty"`
	SearchRecencyFilter    json.RawMessage   `json:"search_recency_filter,omitempty"`
	ReturnImages           *bool             `json:"return_images,omitempty"`
	ReturnRelatedQuestions *bool             `json:"return_related_questions,omitempty"`
	SearchMode             json.RawMessage   `json:"search_mode,omitempty"`
	ReasoningSplit         json.RawMessage   `json:"reasoning_split,omitempty"`
	ThinkingTokenBudget    json.RawMessage   `json:"thinking_token_budget,omitempty"`
	IncludeReasoning       json.RawMessage   `json:"include_reasoning,omitempty"`
	MinP                   json.RawMessage   `json:"min_p,omitempty"`
	RepetitionPenalty      json.RawMessage   `json:"repetition_penalty,omitempty"`
	StructuredOutputs      json.RawMessage   `json:"structured_outputs,omitempty"`
	ReturnTokenIds         json.RawMessage   `json:"return_token_ids,omitempty"`
	MinTokens              *uint             `json:"min_tokens,omitempty"`
	SeparateReasoning      json.RawMessage   `json:"separate_reasoning,omitempty"`
	StreamReasoning        json.RawMessage   `json:"stream_reasoning,omitempty"`
	Regex                  json.RawMessage   `json:"regex,omitempty"`
	EBNF                   json.RawMessage   `json:"ebnf,omitempty"`
	StopTokenIDs           json.RawMessage   `json:"stop_token_ids,omitempty"`
	StopRegex              json.RawMessage   `json:"stop_regex,omitempty"`
	NoStopTrim             json.RawMessage   `json:"no_stop_trim,omitempty"`
	IgnoreEOS              json.RawMessage   `json:"ignore_eos,omitempty"`
	SkipSpecialTokens      json.RawMessage   `json:"skip_special_tokens,omitempty"`
	ContinueFinalMessage   json.RawMessage   `json:"continue_final_message,omitempty"`
	CacheSalt              json.RawMessage   `json:"cache_salt,omitempty"`
}

// MarshalJSON writes the request through the engine, as new-api's does for
// a request without tool-loading messages.
func (r GeneralOpenAIRequest) MarshalJSON() ([]byte, error) {
	type Alias GeneralOpenAIRequest
	r.ThinkingBudget = nil
	return Use.Marshal((*Alias)(&r))
}

type ToolCallRequest struct {
	ID       string          `json:"id,omitempty"`
	Type     string          `json:"type"`
	Function FunctionRequest `json:"function"`
	Custom   json.RawMessage `json:"custom,omitempty"`
	Native   json.RawMessage `json:"-"`
}

func (t ToolCallRequest) MarshalJSON() ([]byte, error) {
	if len(t.Native) > 0 {
		return t.Native, nil
	}
	type Alias ToolCallRequest
	return Use.Marshal((*Alias)(&t))
}

type FunctionRequest struct {
	Description string          `json:"description,omitempty"`
	Name        string          `json:"name"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	Arguments   string          `json:"arguments,omitempty"`
	Strict      *bool           `json:"strict,omitempty"`
}

// UnmarshalJSON leaves a null parameters value out.
func (f *FunctionRequest) UnmarshalJSON(data []byte) error {
	type functionFields FunctionRequest
	var fields functionFields
	if err := Use.Unmarshal(data, &fields); err != nil {
		return err
	}
	if string(fields.Parameters) == "null" {
		fields.Parameters = nil
	}
	*f = FunctionRequest(fields)
	return nil
}

type StreamOptions struct {
	IncludeUsage       bool `json:"include_usage,omitempty"`
	IncludeObfuscation bool `json:"include_obfuscation,omitempty"`
}

type WebSearchOptions struct {
	SearchContextSize string          `json:"search_context_size,omitempty"`
	UserLocation      json.RawMessage `json:"user_location,omitempty"`
}

type Message struct {
	Role             string          `json:"role"`
	Content          any             `json:"content"`
	Name             *string         `json:"name,omitempty"`
	Prefix           *bool           `json:"prefix,omitempty"`
	ReasoningContent *string         `json:"reasoning_content,omitempty"`
	Reasoning        *string         `json:"reasoning,omitempty"`
	ToolCalls        json.RawMessage `json:"tool_calls,omitempty"`
	ToolCallId       string          `json:"tool_call_id,omitempty"`
	Tools            json.RawMessage `json:"tools,omitempty"`
	Annotations      json.RawMessage `json:"annotations,omitempty"`
	parsedContent    []MediaContent
}

type MediaContent struct {
	Type         string          `json:"type"`
	Text         string          `json:"text,omitempty"`
	ImageUrl     any             `json:"image_url,omitempty"`
	InputAudio   any             `json:"input_audio,omitempty"`
	File         any             `json:"file,omitempty"`
	VideoUrl     any             `json:"video_url,omitempty"`
	CacheControl json.RawMessage `json:"cache_control,omitempty"`
}

type MessageImageUrl struct {
	Url      string `json:"url"`
	Detail   string `json:"detail,omitempty"`
	MimeType string `json:"-"`
}

type ChatCompletionsStreamResponseChoice struct {
	Delta        ChatCompletionsStreamResponseChoiceDelta `json:"delta"`
	Logprobs     *any                                     `json:"logprobs"`
	FinishReason *string                                  `json:"finish_reason"`
	Index        int                                      `json:"index"`
}

type ChatCompletionsStreamResponseChoiceDelta struct {
	Content          *string            `json:"content,omitempty"`
	ReasoningContent *string            `json:"reasoning_content,omitempty"`
	Reasoning        *string            `json:"reasoning,omitempty"`
	Role             string             `json:"role,omitempty"`
	ToolCalls        []ToolCallResponse `json:"tool_calls,omitempty"`
	Annotations      json.RawMessage    `json:"annotations,omitempty"`
}

type ToolCallResponse struct {
	Index    *int             `json:"index,omitempty"`
	ID       string           `json:"id,omitempty"`
	Type     any              `json:"type"`
	Function FunctionResponse `json:"function"`
}

type FunctionResponse struct {
	Description string `json:"description,omitempty"`
	Name        string `json:"name,omitempty"`
	Parameters  any    `json:"parameters,omitempty"`
	Arguments   string `json:"arguments"`
}

type ChatCompletionsStreamResponse struct {
	Id                string                                `json:"id"`
	Object            string                                `json:"object"`
	Created           int64                                 `json:"created"`
	Model             string                                `json:"model"`
	SystemFingerprint *string                               `json:"system_fingerprint"`
	Choices           []ChatCompletionsStreamResponseChoice `json:"choices"`
	Usage             *Usage                                `json:"usage"`
}

type Usage struct {
	PromptTokens         int           `json:"prompt_tokens"`
	CompletionTokens     int           `json:"completion_tokens"`
	TotalTokens          int           `json:"total_tokens"`
	PromptCacheHitTokens int           `json:"prompt_cache_hit_tokens,omitempty"`
	UsageSemantic        string        `json:"usage_semantic,omitempty"`
	UsageSource          string        `json:"usage_source,omitempty"`
	BillingUsage         *BillingUsage `json:"billing_usage,omitempty"`

	PromptTokensDetails    InputTokenDetails   `json:"prompt_tokens_details"`
	CompletionTokenDetails OutputTokenDetails  `json:"completion_tokens_details"`
	InputTokens            int                 `json:"input_tokens"`
	OutputTokens           int                 `json:"output_tokens"`
	InputTokensDetails     *InputTokenDetails  `json:"input_tokens_details"`
	OutputTokensDetails    *OutputTokenDetails `json:"output_tokens_details,omitempty"`

	ClaudeCacheCreation5mTokens int `json:"claude_cache_creation_5_m_tokens"`
	ClaudeCacheCreation1hTokens int `json:"claude_cache_creation_1_h_tokens"`

	Cost any `json:"cost,omitempty"`
}

type BillingUsage struct {
	Source      string `json:"source,omitempty"`
	Semantic    string `json:"semantic,omitempty"`
	Estimated   bool   `json:"estimated,omitempty"`
	OpenAIUsage *Usage `json:"openai_usage,omitempty"`
}

type InputTokenDetails struct {
	CachedTokens             int                     `json:"cached_tokens"`
	CachedTokensDetails      *CachedTokenDetails     `json:"cached_tokens_details,omitempty"`
	CachedCreationTokens     int                     `json:"cached_creation_tokens,omitempty"`
	CacheWriteTokens         int                     `json:"cache_write_tokens,omitempty"`
	CacheWriteTokensDetails  *CacheWriteTokenDetails `json:"cache_write_tokens_details,omitempty"`
	TextTokens               int                     `json:"text_tokens"`
	AudioTokens              int                     `json:"audio_tokens"`
	ImageTokens              int                     `json:"image_tokens"`
	CacheCreationInputTokens int                     `json:"cache_creation_input_tokens,omitempty"`
}

type CacheWriteTokenDetails struct {
	TTL30mTokens int `json:"ttl_30m_tokens"`
	TTL12hTokens int `json:"ttl_12h_tokens"`
}

type CachedTokenDetails struct {
	TextTokens  *int `json:"text_tokens,omitempty"`
	ImageTokens *int `json:"image_tokens,omitempty"`
	AudioTokens *int `json:"audio_tokens,omitempty"`
}

type OutputTokenDetails struct {
	TextTokens      int `json:"text_tokens"`
	AudioTokens     int `json:"audio_tokens"`
	ImageTokens     int `json:"image_tokens"`
	ReasoningTokens int `json:"reasoning_tokens"`
}
