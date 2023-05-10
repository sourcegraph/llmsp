package claude

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

type Speaker string

const (
	Human     Speaker = "HUMAN"
	Assistant Speaker = "ASSISTANT"
)

type Message struct {
	Speaker Speaker `json:"speaker"`
	Text    string  `json:"text"`
}

type CompletionParameters struct {
	Messages          []Message `json:"messages"`
	Temperature       float32   `json:"temperature"`
	MaxTokensToSample int       `json:"maxTokensToSample"`
	TopK              int       `json:"topK"`
	TopP              int       `json:"topP"`
}

type Client struct {
	URL        string
	authToken  string
	httpClient *http.Client
}

func NewClient(url string, authToken string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	return &Client{
		URL:        url,
		httpClient: httpClient,
		authToken:  authToken,
	}
}

func DefaultCompletionParameters(messages []Message) *CompletionParameters {
	return &CompletionParameters{
		Messages:          messages,
		Temperature:       0.2,
		MaxTokensToSample: 1000,
		TopK:              -1,
		TopP:              -1,
	}
}

type message struct {
	Speaker string `json:"speaker"`
	Text    string `json:"text"`
}

type completionInput struct {
	Messages []message `json:"messages"`
}

type GraphQLQuery[T any] struct {
	Query     string `json:"query"`
	Variables T      `json:"variables"`
}

const getCompletionsQuery = `query GetCompletions($messages: [Message!]!, $temperature: Float!, $maxTokensToSample: Int!, $topK: Int!, $topP: Int!) {
  completions(input: {
    messages: $messages,
    temperature: $temperature,
    maxTokensToSample: $maxTokensToSample,
    topK: $topK,
    topP: $topP
  })
}`

type completions struct {
	Data struct {
		Completions string
	}
	Errors []struct {
		Message   string
		Locations []struct {
			Line   int
			Column int
		}
	}
}

func (c *Client) GetCompletion(ctx context.Context, params *CompletionParameters, includePromptText bool) (string, error) {
	completionsPath, err := url.JoinPath(c.URL, "/.api/graphql")
	if err != nil {
		return "", err
	}

	q := GraphQLQuery[CompletionParameters]{
		Query:     getCompletionsQuery,
		Variables: *params,
	}

	body, err := json.Marshal(q)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", completionsPath, bytes.NewBuffer(body))
	if err != nil {
		return "", err
	}
	req.Header.Add("Content-Type", "application/json; charset=utf-8")
	req.Header.Add("Authorization", "token "+c.authToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}

	var completion completions
	if err := json.NewDecoder(resp.Body).Decode(&completion); err != nil {
		return "", err
	}
	defer resp.Body.Close()

	completionText := completion.Data.Completions
	if includePromptText {
		completionText = params.Messages[len(params.Messages)-1].Text + completionText
	}

	return completionText, nil
}

func (c *Client) StreamCompletion(ctx context.Context, params *CompletionParameters, includePromptText bool) (chan string, error) {
	retChan := make(chan string)
	completionsPath, err := url.JoinPath(c.URL, "/.api/completions/stream")
	if err != nil {
		return nil, err
	}

	for i, m := range params.Messages {
		params.Messages[i].Speaker = Speaker(strings.ToLower(string(m.Speaker)))
	}
	reqBody, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", completionsPath, bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Add("Content-Type", "application/json; charset=utf-8")
	req.Header.Add("Authorization", "token "+c.authToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	go func() {
		var completion struct {
			Completion string
		}

		reader := bufio.NewReader(resp.Body)
		defer resp.Body.Close()

		for {
			line, err := reader.ReadSlice('\n')
			if err != nil {
				close(retChan)
				break
			}

			if strings.HasPrefix(string(line), "event") {
				if strings.Contains(string(line), "done") {
					close(retChan)
					break
				}
			} else {
				if strings.HasPrefix(string(line), "data: ") {
					json.Unmarshal([]byte(strings.TrimPrefix(string(line), "data: ")), &completion)
					if includePromptText {
						retChan <- strings.TrimSuffix(params.Messages[len(params.Messages)-1].Text+completion.Completion, "\n```")
					} else {
						retChan <- strings.TrimSuffix(completion.Completion, "\n```")
					}
				}
			}
		}
	}()

	return retChan, nil
}
