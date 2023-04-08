package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"strconv"
	"strings"

	"github.com/pjlast/llmsp/claude"
	"github.com/pjlast/llmsp/sourcegraph/embeddings"
	"github.com/sourcegraph/go-lsp"
	"github.com/sourcegraph/jsonrpc2"
)

var root lsp.DocumentURI

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

			return lsp.InitializeResult{
				Capabilities: lsp.ServerCapabilities{
					CodeActionProvider: true,
				},
			}, nil

		case "initialized":
			return nil, nil

		case "textDocument/codeAction":
			var params lsp.CodeActionParams
			if err := json.Unmarshal(*req.Params, &params); err != nil {
				return nil, err
			}

			filename := strings.TrimPrefix(string(params.TextDocument.URI), "file://")

			commands := []lsp.Command{
				{
					Title:     "Provide suggestions",
					Command:   "suggest",
					Arguments: []interface{}{filename, params.Range.Start.Line, params.Range.End.Line},
				},
				{
					Title:     "Generate docstring",
					Command:   "docstring",
					Arguments: []interface{}{filename, params.Range.Start.Line, params.Range.End.Line},
				},
			}

			return commands, nil

		case "workspace/executeCommand":
			var params lsp.ExecuteCommandParams
			if err := json.Unmarshal(*req.Params, &params); err != nil {
				return nil, err
			}

			switch params.Command {
			case "suggest":
				filename := params.Arguments[0].(string)
				startLine := params.Arguments[1].(float64)
				endLine := params.Arguments[2].(float64)
				buf, err := ioutil.ReadFile(filename)
				if err != nil {
					return nil, err
				}
				snippet := getFileSnippet(string(buf), int(startLine), int(endLine))
				return nil, sendDiagnostics(ctx, conn, filename, snippet, int(startLine))
			}

			return nil, nil
		}
		return nil, nil
	})
}

func getFileSnippet(fileContent string, startLine, endLine int) string {
	fileLines := strings.Split(fileContent, "\n")
	numberedSnippet := make([]string, 0, endLine-startLine+1)
	for i, line := range fileLines[startLine:endLine] {
		numberedSnippet = append(numberedSnippet, fmt.Sprintf("%d. %s", i, line))
	}
	return strings.Join(numberedSnippet, "\n")
}

func getMessages(fileName string, numberedFile string, embeddingResults *embeddings.EmbeddingsSearchResult) []claude.Message {
	messages := []claude.Message{{
		Speaker: "assistant",
		Text: `I am Cody, an AI-powered coding assistant developed by Sourcegraph. I operate inside a Language Server Protocol implementation. My task is to help programmers with programming tasks in the Go programming language.
I am an expert in the Go programming language.
I ignore import statements.
I have access to your currently open files in the editor.
I will generate suggestions as concisely and clearly as possible.
I only suggest something if I am certain about my answer.
I suggest improvements in the following format:
Line {number}: {suggestion}`,
	}}
	for _, embedding := range embeddingResults.CodeResults {
		messages = append(messages, claude.Message{
			Speaker:  "human",
			FileName: embedding.FileName,
			Text: fmt.Sprintf(`Here are the contents of the file '%s':
%s`, embedding.FileName, embedding.Content),
		}, claude.Message{Speaker: "assistant", Text: "Ok."})
	}
	messages = append(messages, claude.Message{
		Speaker: "human",
		Text: fmt.Sprintf(`Suggest improvements to the file '%s'. Here are the file contents:

%s`, fileName, numberedFile),
	},
		claude.Message{
			Speaker: "assistant",
			Text:    "Line",
		})

	return messages
}

// sendDiagnostics sends the provided diagnostics back over the provided connection.
func sendDiagnostics(ctx context.Context, conn jsonrpc2.JSONRPC2, filename, snippet string, lineOffset int) error {
	srcURL := os.Getenv("SRC_URL")
	srcToken := os.Getenv("SRC_TOKEN")
	claudeCLI := claude.NewClient(srcURL, srcToken, nil)
	srcCLI := embeddings.NewClient(srcURL, srcToken, nil)

	embeddingResults, err := srcCLI.GetEmbeddings("UmVwb3NpdG9yeTozOTk=", snippet, 2, 0)
	if err != nil {
		return err
	}

	params := claude.DefaultCompletionParameters(getMessages(filename, snippet, embeddingResults))

	retChan, err := claudeCLI.GetCompletion(params)

	for completionResp := range retChan {
		fmt.Println(completionResp)
		diagnostics := []lsp.Diagnostic{}
		for _, line := range strings.Split(completionResp, "\n") {
			parts := strings.Split(line, ": ")
			if len(parts) < 2 {
				continue
			}
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
						Line:      lineStart + lineOffset,
						Character: 0,
					},
					End: lsp.Position{
						Line:      lineEnd + lineOffset,
						Character: 0,
					},
				},
				Severity: lsp.Log,
				Message:  parts[1],
			})
		}

		params := lsp.PublishDiagnosticsParams{
			URI:         lsp.DocumentURI("file://" + filename),
			Diagnostics: diagnostics,
		}
		if err := conn.Notify(ctx, "textDocument/publishDiagnostics", params); err != nil {
			return err
		}
	}

	return nil
}
