package embeddings

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
)

type EmbeddingsSearchResult struct {
	CodeResults []struct {
		FileName  string
		StartLine int
		EndLine   int
		Content   string
	}
	TextResults []struct {
		FileName  string
		StartLine int
		EndLine   int
		Content   string
	}
}

type EmbeddingsResponse struct {
	Data struct {
		EmbeddingsSearch EmbeddingsSearchResult
	}
}

type RepoIDResponse struct {
	Data struct {
		Repository struct {
			ID string
		}
	}
}

type Client struct {
	URL         string
	httpClient  *http.Client
	accessToken string
}

func NewClient(sgURL string, accessToken string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	sgURL = strings.TrimSuffix(sgURL, "/") + "/.api/graphql"

	return &Client{
		URL:         sgURL,
		httpClient:  httpClient,
		accessToken: accessToken,
	}
}

type searchEmbeddingsQuery struct {
	Query     string              `json:"query"`
	Variables embeddingsVariables `json:"variables"`
}

type getRepoIDQuery struct {
	Query     string            `json:"query"`
	Variables repoNameVariables `json:"variables"`
}

type repoNameVariables struct {
	Name string `json:"name"`
}

type embeddingsVariables struct {
	Repo             string `json:"repo"`
	Query            string `json:"query"`
	CodeResultsCount int    `json:"codeResultsCount"`
	TextResultsCount int    `json:"textResultsCount"`
}

func (c *Client) GetEmbeddings(repoID string, query string, codeResults int, textResults int) (*EmbeddingsSearchResult, error) {
	q := searchEmbeddingsQuery{
		Query: `query EmbeddingsSearch($repo: ID!, $query: String!, $codeResultsCount: Int!, $textResultsCount: Int!) {
  embeddingsSearch(repo: $repo, query: $query, codeResultsCount: $codeResultsCount, textResultsCount: $textResultsCount) {
    codeResults {
      fileName
      startLine
      endLine
      content
    }
    textResults {
      fileName
      startLine
      endLine
      content
    }
  }
}`,
		Variables: embeddingsVariables{
			Repo:             repoID,
			Query:            query,
			CodeResultsCount: codeResults,
			TextResultsCount: textResults,
		},
	}

	var embeddings EmbeddingsResponse
	if err := c.sendGraphQLRequest(q, &embeddings); err != nil {
		return nil, err
	}

	return &embeddings.Data.EmbeddingsSearch, nil
}

func (c *Client) GetRepoID(repoName string) (string, error) {
	q := getRepoIDQuery{
		Query: `query RepoID($name: String!) {
      repository(name: $name) {
        id
      }
    }`,
		Variables: repoNameVariables{
			Name: repoName,
		},
	}

	var repoIDResponse RepoIDResponse
	if err := c.sendGraphQLRequest(q, &repoIDResponse); err != nil {
		return "", err
	}

	return repoIDResponse.Data.Repository.ID, nil
}

// sendGraphQLRequest sends a GraphQL request and parses the response.
func (c *Client) sendGraphQLRequest(request interface{}, response interface{}) error {
	reqBody, err := json.Marshal(request)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", c.URL, bytes.NewBuffer(reqBody))
	if err != nil {
		return err
	}
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("Authorization", "token "+c.accessToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return json.NewDecoder(resp.Body).Decode(response)
}
