package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"strconv"
	"strings"

	"github.com/sourcegraph/go-lsp"
	"github.com/sourcegraph/jsonrpc2"
)

var root lsp.DocumentURI

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

type CodyResponse struct {
	Completion string
}

type EmbeddingsResponse struct {
	Data struct {
		EmbeddingsSearch struct {
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
	}
}

const (
	numCodeResults = 8
	numTextResults = 2
)

const (
	instanceURL   = "INSTANCE_URL"
	sgAccessToken = "SG_ACCESS_TOKEN"
)

var graphqlEndpoint = fmt.Sprintf("%s/.api/graphql", instanceURL)

const searchEmbeddingsQuery = `
{"query": "query EmbeddingsSearch($query: String!) { embeddingsSearch(repo: \"%s\", query: $query, codeResultsCount: 8, textResultsCount: 2) { codeResults { fileName startLine endLine content } textResults { fileName startLine endLine content } } }", "variables": {"query": "%s"}}`

func DoRequest(input string) string {
	// TODO: Add GraphQL query to fetch the relevant repo ID instead of hardcoding it
	sourcegraphRepoID := "UmVwb3NpdG9yeTozOTk="

	// TODO: Clean up this horrible code
	type toMarshal struct {
		Query string `json:"query"`
	}
	tm := toMarshal{Query: input}
	jsonStr, _ := json.Marshal(tm)
	req, _ := http.NewRequest("POST", graphqlEndpoint, bytes.NewBuffer([]byte(fmt.Sprintf(searchEmbeddingsQuery, sourcegraphRepoID, strings.TrimSuffix(strings.TrimPrefix(string(jsonStr), "{\"query\":\""), "\"}")))))

	req.Header.Add("Content-Type", "application/graphql")
	req.Header.Add("Authorization", "token "+sgAccessToken)

	resp, _ := http.DefaultClient.Do(req)
	dec := json.NewDecoder(resp.Body)
	var embedResp EmbeddingsResponse
	dec.Decode(&embedResp)

	x := []Message{}
	for _, msg := range embedResp.Data.EmbeddingsSearch.CodeResults {
		x = append(x, Message{
			Speaker:  "human",
			Text:     msg.Content,
			FileName: msg.FileName,
		}, Message{Speaker: "assistant", Text: "Ok."})
	}
	numberedLines := []string{}
	for i, line := range strings.Split(input, "\n") {
		numberedLines = append(numberedLines, fmt.Sprintf("%d. %s", i, line))
	}
	numberedFile := strings.Join(numberedLines, "\n")
	params := CompletionParameters{
		Messages: []Message{
			{
				Speaker: "human",
				Text: `You are Cody, an AI-powered coding assistant developed by Sourcegraph. You live inside a Language Server Protocol implementation. You have access to my currently open files. You perform the following actions:
- Provide suggestions on how to improve lines of code in the Go programming language.

In your responses, obey the following rules:
- Be as brief and concise as possible without losing clarity.
- Make suggestions only if you are sure about your answer. Otherwise, don't make any suggestion at all.
- Only reference functions if you are sure they exist.
- Do not make suggestions that are already present in the provided code.
- Do not make any suggestions about improving readability
- Your suggestions will be in the following format (always include the word "Line"):
Line number: suggestion

You have access to the "sourcegraph" repository. You are able to answer questions about the "sourcegraph" repository. I will provide the relevant code snippets from the "sourcegraph" repository when necessary to answer my questions.`,
			},
			{
				Speaker: "assistant",
				Text: `Understood. I am Cody, an AI-powered coding assistant developed by Sourcegraph. I operate inside a Language Server Protocol implementation. My task is to help programmers with programming tasks in the Go programming language.
I have access to your currently open files in the editor.
I will generate suggestions as concisely and clearly as possible.
I will not suggest anything if I'm not sure about my answer.
I will not make suggestions that are already present in the provided code.
I will not make any suggestions regarding readability.
I will suggest improvements in the format:
Line number: suggestion

I have access to the "sourcegraph" repository and can answer questions about its files.`,
			},
		},
		Temperature:       0.2,
		MaxTokensToSample: 1000,
		TopK:              -1,
		TopP:              -1,
	}
	for _, s := range x {
		params.Messages = append(params.Messages, s)
	}
	params.Messages = append(params.Messages, Message{
		Speaker: "human",
		Text: fmt.Sprintf(`Suggest improvements to this code:
	%s`, numberedFile),
	},
		Message{
			Speaker: "assistant",
			Text:    "",
		})
	reqBody, err := json.Marshal(params)
	if err != nil {
		return ""
	}
	req, err = http.NewRequest("POST", "https://sourcegraph.sourcegraph.com/.api/completions/stream", io.NopCloser(bytes.NewReader(reqBody)))
	req.Header.Add("Content-Type", "application/json; charset=utf-8")
	req.Header.Add("Authorization", "token sgp_70f3584b35d7bbfcbd749ce6e092f54c784d66f0")
	if err != nil {
		return ""
	}

	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	body, err := io.ReadAll(resp.Body)
	defer resp.Body.Close()

	nextLineData := false
	data := ""
	for _, line := range strings.Split(string(body), "\n") {
		if nextLineData {
			data = line[6:]
			nextLineData = false
		}
		if line == "event: completion" {
			nextLineData = true
		}
	}
	var codyResponse CodyResponse
	if err := json.Unmarshal([]byte(data), &codyResponse); err != nil {
		return string(body)
	}

	return codyResponse.Completion
}

// Handle looks at the provided request and calls functions depending on the request method.
// The response, if any, is sent back over the connection.
func Handle() jsonrpc2.Handler {
	return jsonrpc2.HandlerWithError(func(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) (any, error) {
		switch req.Method {
		case "initialize":
			var params lsp.InitializeParams
			if err := json.Unmarshal(*req.Params, &params); err != nil {
				return nil, err
			}

			root = params.Root()

			return lsp.InitializeResult{}, nil

		case "initialized":
			return nil, nil

		case "textDocument/didSave":
			var params lsp.DidSaveTextDocumentParams
			if err := json.Unmarshal(*req.Params, &params); err != nil {
				return nil, err
			}

			return nil, sendDiagnostics(ctx, conn, "diagnostics", "go", []string{string(params.TextDocument.URI)})
		}
		return nil, nil
	})
}

// sendDiagnostics sends the provided diagnostics back over the provided connection.
func sendDiagnostics(ctx context.Context, conn jsonrpc2.JSONRPC2, diags string, source string, files []string) error {
	filename := strings.TrimPrefix(string(files[0]), "file://")
	buf, err := ioutil.ReadFile(filename)
	if err != nil {
		return err
	}
	numberedLines := []string{}
	for i, line := range strings.Split(string(buf), "\n") {
		numberedLines = append(numberedLines, fmt.Sprintf("%d. %s", i, line))
	}
	numberedFile := strings.Join(numberedLines, "\n")
	resp := DoRequest(numberedFile)

	diagnostics := []lsp.Diagnostic{}
	for _, line := range strings.Split(resp, "\n") {
		if len(line) > 4 && line[:4] == "Line" {
			parts := strings.Split(line, ": ")
			lineNumberRange := parts[0][5:]
			var lineStart, lineEnd int
			if strings.Contains(lineNumberRange, "-") {
				sp := strings.Split(lineNumberRange, "-")
				lineStart, err = strconv.Atoi(sp[0])
				if err != nil {
					continue
				}
				lineEnd, err = strconv.Atoi(sp[1])
				if err != nil {
					continue
				}
			} else {
				lineStart, err = strconv.Atoi(parts[0][5:])
				if err != nil {
					continue
				}
				lineEnd = lineStart
			}

			diagnostics = append(diagnostics, lsp.Diagnostic{
				Range: lsp.Range{
					Start: lsp.Position{
						Line:      lineStart,
						Character: 0,
					},
					End: lsp.Position{
						Line:      lineEnd,
						Character: 0,
					},
				},
				Severity: lsp.Log,
				Message:  parts[1],
			})
		}
	}

	params := lsp.PublishDiagnosticsParams{
		URI:         lsp.DocumentURI(files[0]),
		Diagnostics: diagnostics,
	}
	if err := conn.Notify(ctx, "textDocument/publishDiagnostics", params); err != nil {
		return err
	}

	return nil
}
