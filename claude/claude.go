package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

type Message struct {
	Speaker  string `json:"speaker"`
	Text     string `json:"text"`
	FileName string `json:"fileName,omitempty"`
}

type CompletionParameters struct {
	Messages          []Message
	Temperature       float32
	MaxTokensToSample int
	TopK              int
	TopP              int
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

func (c *Client) GetCompletion(params *CompletionParameters, includePromptText bool) (chan string, error) {
	retChan := make(chan string)
	completionsPath, err := url.JoinPath(c.URL, "/.api/completions/stream")
	if err != nil {
		return nil, err
	}

	reqBody, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", completionsPath, bytes.NewBuffer(reqBody))
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
						retChan <- params.Messages[len(params.Messages)-1].Text + completion.Completion
					} else {
						retChan <- completion.Completion
					}
				}
			}
		}
	}()

	return retChan, nil
}
