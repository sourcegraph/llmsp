package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/pjlast/llmsp/claude"
	"github.com/pjlast/llmsp/sourcegraph/embeddings"
	"github.com/sourcegraph/go-lsp"
	"github.com/sourcegraph/jsonrpc2"
)

type LLMSPSourcegraphSettings struct {
	EmbeddingsClient *embeddings.Client
	ClaudeClient     *claude.Client
	Enabled          bool
	URL              string
	AccessToken      string
	RepoEmbeddings   []string
}

type Server struct {
	Sourcegraph LLMSPSourcegraphSettings
}

func (s *Server) Handle() jsonrpc2.Handler {
	return jsonrpc2.HandlerWithError(func(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) (any, error) {
		switch req.Method {
		case "initialize":
			var params lsp.InitializeParams
			if err := json.Unmarshal(*req.Params, &params); err != nil {
				return nil, err
			}

			root = params.Root()

			opts := lsp.TextDocumentSyncOptionsOrKind{
				Options: &lsp.TextDocumentSyncOptions{
					OpenClose: true,
					WillSave:  true,
					Change:    lsp.TDSKFull,
				},
			}
			completionOptions := lsp.CompletionOptions{}

			return lsp.InitializeResult{
				Capabilities: lsp.ServerCapabilities{
					TextDocumentSync:   &opts,
					CodeActionProvider: true,
					CompletionProvider: &completionOptions,
				},
			}, nil

		case "initialized":
			return nil, nil

		case "textDocument/didChange":
			var params lsp.DidChangeTextDocumentParams
			if err := json.Unmarshal(*req.Params, &params); err != nil {
				return nil, err
			}

			fileMap[params.TextDocument.URI] = params.ContentChanges[0].Text

			return nil, nil

		case "textDocument/didOpen":
			go func() {
				var params lsp.DidOpenTextDocumentParams
				if err := json.Unmarshal(*req.Params, &params); err != nil {
					return
				}

				fileMap[params.TextDocument.URI] = params.TextDocument.Text
			}()

			return nil, nil

		case "textDocument/codeAction":
			var params lsp.CodeActionParams
			if err := json.Unmarshal(*req.Params, &params); err != nil {
				return nil, err
			}

			commands := []lsp.Command{
				{
					Title:     "Provide suggestions",
					Command:   "suggest",
					Arguments: []interface{}{params.TextDocument.URI, params.Range.Start.Line, params.Range.End.Line},
				},
				{
					Title:     "Generate docstring",
					Command:   "docstring",
					Arguments: []interface{}{params.TextDocument.URI, params.Range.Start.Line, params.Range.End.Line},
				},
				{
					Title:     "Implement TODOs",
					Command:   "todos",
					Arguments: []interface{}{params.TextDocument.URI, params.Range.Start.Line, params.Range.End.Line},
				},
			}

			return commands, nil

		case "textDocument/completion":
			var params lsp.CompletionParams
			if err := json.Unmarshal(*req.Params, &params); err != nil {
				return nil, err
			}
			if params.Context.TriggerKind != lsp.CTKInvoked {
				return []lsp.CompletionItem{}, nil
			}
			snippet := getFileSnippet(fileMap[params.TextDocument.URI], 0, params.Position.Line)

			completion := s.getCompletionSuggestion(snippet)

			completions := []lsp.CompletionItem{
				{
					Label:      "LLMSP",
					Kind:       lsp.CIKSnippet,
					InsertText: completion,
					Detail:     strings.Split(snippet, "\n")[params.Position.Line] + completion,
				},
			}

			return completions, nil

		case "workspace/didChangeConfiguration":
			var params DidChangeConfigurationParams
			if err := json.Unmarshal(*req.Params, &params); err != nil {
				return nil, err
			}
			if params.Settings.LLMSP.Sourcegraph != nil {
				s.Sourcegraph.Enabled = true
				s.Sourcegraph.URL = params.Settings.LLMSP.Sourcegraph.URL
				s.Sourcegraph.AccessToken = params.Settings.LLMSP.Sourcegraph.AccessToken
				s.Sourcegraph.RepoEmbeddings = params.Settings.LLMSP.Sourcegraph.RepoEmbeddings
				s.Sourcegraph.EmbeddingsClient = embeddings.NewClient(s.Sourcegraph.URL, s.Sourcegraph.AccessToken, nil)
				s.Sourcegraph.ClaudeClient = claude.NewClient(s.Sourcegraph.URL, s.Sourcegraph.AccessToken, nil)
			}
			return nil, nil

		case "workspace/executeCommand":
			var params lsp.ExecuteCommandParams
			if err := json.Unmarshal(*req.Params, &params); err != nil {
				return nil, err
			}

			switch params.Command {
			case "suggest":
				filename := lsp.DocumentURI(params.Arguments[0].(string))
				startLine := params.Arguments[1].(float64)
				endLine := params.Arguments[2].(float64)
				snippet := getFileSnippet(fileMap[filename], int(startLine), int(endLine))
				snippet = numberLines(snippet, int(startLine))
				return nil, s.sendDiagnostics(ctx, conn, string(filename), snippet)

			case "docstring":
				filename := lsp.DocumentURI(params.Arguments[0].(string))
				startLine := int(params.Arguments[1].(float64))
				endLine := int(params.Arguments[2].(float64))
				funcSnippet := getFileSnippet(fileMap[filename], int(startLine), int(endLine))
				docstring := s.getDocString(funcSnippet)

				edits := []lsp.TextEdit{
					{
						Range: lsp.Range{
							Start: lsp.Position{
								Line:      startLine,
								Character: 0,
							},
							End: lsp.Position{
								Line:      endLine,
								Character: len(strings.Split(fileMap[filename], "\n")[endLine]),
							},
						},
						NewText: docstring + "\n" + funcSnippet,
					},
				}

				editParams := ApplyWorkspaceEditParams{
					Edit: WorkspaceEdit{
						DocumentChanges: []TextDocumentEdit{
							{
								TextDocument: lsp.VersionedTextDocumentIdentifier{
									TextDocumentIdentifier: lsp.TextDocumentIdentifier{
										URI: filename,
									},
									Version: 0,
								},
								Edits: edits,
							},
						},
					},
				}

				conn.Notify(ctx, "workspace/applyEdit", editParams)

				return nil, nil

			case "todos":
				go func() {
					filename := lsp.DocumentURI(params.Arguments[0].(string))
					startLine := int(params.Arguments[1].(float64))
					endLine := int(params.Arguments[2].(float64))
					funcSnippet := getFileSnippet(fileMap[filename], int(startLine), int(endLine))
					implemented := s.implementTODOs(funcSnippet)

					edits := []lsp.TextEdit{
						{
							Range: lsp.Range{
								Start: lsp.Position{
									Line:      startLine,
									Character: 0,
								},
								End: lsp.Position{
									Line:      endLine,
									Character: len(strings.Split(fileMap[filename], "\n")[endLine]),
								},
							},
							NewText: "\n" + implemented,
						},
					}

					editParams := ApplyWorkspaceEditParams{
						Edit: WorkspaceEdit{
							DocumentChanges: []TextDocumentEdit{
								{
									TextDocument: lsp.VersionedTextDocumentIdentifier{
										TextDocumentIdentifier: lsp.TextDocumentIdentifier{
											URI: filename,
										},
										Version: 0,
									},
									Edits: edits,
								},
							},
						},
					}

					conn.Notify(ctx, "workspace/applyEdit", editParams)
				}()

				return nil, nil
			}

			return nil, nil
		}
		return nil, nil
	})
}

var root lsp.DocumentURI

type TextDocumentEdit struct {
	TextDocument lsp.VersionedTextDocumentIdentifier `json:"textDocument"`
	Edits        []lsp.TextEdit                      `json:"edits"`
}

type WorkspaceEdit struct {
	DocumentChanges []TextDocumentEdit `json:"documentChanges"`
}

type ApplyWorkspaceEditParams struct {
	Edit WorkspaceEdit `json:"edit"`
}

type LLMSPSettings struct {
	Sourcegraph *SourcegraphSettings `json:"sourcegraph"`
}

type SourcegraphSettings struct {
	URL            string   `json:"url"`
	AccessToken    string   `json:"accessToken"`
	RepoEmbeddings []string `json:"repos"`
}

type LLMSPConfig struct {
	Settings SourcegraphSettings `json:"sourcegraph"`
}

type ConfigurationSettings struct {
	LLMSP LLMSPSettings `json:"llmsp"`
}

type DidChangeConfigurationParams struct {
	Settings ConfigurationSettings `json:"settings"`
}

var fileMap = map[lsp.DocumentURI]string{}

func (s *Server) getCompletionSuggestion(snippet string) string {
	var embeddings *embeddings.EmbeddingsSearchResult = nil
	var err error
	if len(s.Sourcegraph.RepoEmbeddings) > 0 {
		embeddings, _ = s.Sourcegraph.EmbeddingsClient.GetEmbeddings("UmVwb3NpdG9yeTozOTk=", snippet, 8, 0)
	}
	params := claude.DefaultCompletionParameters(getMessages(embeddings))
	params.Messages = append(params.Messages, claude.Message{
		Speaker: "human",
		Text: fmt.Sprintf(`Suggest a code snippet to complete the following Go code. Provide only the suggestion, nothing else.
%s`, snippet),
	},
		claude.Message{
			Speaker: "assistant",
			Text:    "```go\n" + strings.Split(snippet, "\n")[len(strings.Split(snippet, "\n"))-1],
		})
	retChan, err := s.Sourcegraph.ClaudeClient.GetCompletion(params, false)
	if err != nil {
		return ""
	}
	var completion string
	for resp := range retChan {
		completion = resp
	}
	return strings.TrimSuffix(strings.TrimPrefix(completion, "```go\n"), "\n```")
}

func (s *Server) getDocString(function string) string {
	params := claude.DefaultCompletionParameters(getMessages(nil))
	params.Messages = append(params.Messages, claude.Message{
		Speaker: "human",
		Text: fmt.Sprintf(`Generate a doc string explaining the use of the following Go function:
%s

Don't include the function in your output.`, function),
	},
		claude.Message{
			Speaker: "assistant",
			Text:    "//",
		})
	retChan, err := s.Sourcegraph.ClaudeClient.GetCompletion(params, true)
	if err != nil {
		return ""
	}
	var docstring string
	for resp := range retChan {
		docstring = resp
	}
	return docstring
}

func (s *Server) implementTODOs(function string) string {
	params := claude.DefaultCompletionParameters(getMessages(nil))
	params.Messages = append(params.Messages, claude.Message{
		Speaker: "human",
		Text: fmt.Sprintf(`The following Go code contains TODO instructions. Replace the TODO comments by implementing them. Import any Go libraries that would help complete the task. Only provide the completed code. Don't say anything else.
Keep the original code I provided as part of your answer.
%s`, function),
	},
		claude.Message{
			Speaker: "assistant",
			Text:    "```go",
		})
	retChan, err := s.Sourcegraph.ClaudeClient.GetCompletion(params, true)
	if err != nil {
		return ""
	}
	var implemented string
	for resp := range retChan {
		implemented = resp
	}
	return strings.TrimSuffix(strings.TrimPrefix(implemented, "```go\n"), "\n```")
}

func getFileSnippet(fileContent string, startLine, endLine int) string {
	fileLines := strings.Split(fileContent, "\n")
	return strings.Join(fileLines[startLine:endLine+1], "\n")
}

func numberLines(content string, startLine int) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		lines[i] = fmt.Sprintf("%d. %s", i+startLine, line)
	}
	return strings.Join(lines, "\n")
}

func getSuggestionMessages(filename, content string) []claude.Message {
	return []claude.Message{
		{
			Speaker: "human",
			Text: fmt.Sprintf(`Suggest improvements to following lines of code in the file '%s':
%s

Suggest improvements in the format:
Line {number}: {suggestion}`, filename, content),
		}, {
			Speaker: "assistant",
			Text:    "Line",
		},
	}
}

func getMessages(embeddingResults *embeddings.EmbeddingsSearchResult) []claude.Message {
	messages := []claude.Message{{
		Speaker: "assistant",
		Text: `I am Cody, an AI-powered coding assistant developed by Sourcegraph. I operate inside a Language Server Protocol implementation. My task is to help programmers with programming tasks in the Go programming language.
I am an expert in the Go programming language.
I ignore import statements.
I have access to your currently open files in the editor.
I will generate suggestions as concisely and clearly as possible.
I only suggest something if I am certain about my answer.`,
	}}
	if embeddingResults != nil {
		for _, embedding := range embeddingResults.CodeResults {
			messages = append(messages, claude.Message{
				Speaker:  "human",
				FileName: embedding.FileName,
				Text: fmt.Sprintf(`Here are the contents of the file '%s':
%s`, embedding.FileName, embedding.Content),
			}, claude.Message{Speaker: "assistant", Text: "Ok."})
		}
	}

	return messages
}

// sendDiagnostics sends the provided diagnostics back over the provided connection.
func (s *Server) sendDiagnostics(ctx context.Context, conn jsonrpc2.JSONRPC2, filename, snippet string) error {
	embeddingResults, err := s.Sourcegraph.EmbeddingsClient.GetEmbeddings("UmVwb3NpdG9yeTozOTk=", snippet, 2, 0)
	if err != nil {
		return err
	}

	params := claude.DefaultCompletionParameters(getMessages(embeddingResults))
	params.Messages = append(params.Messages, getSuggestionMessages(strings.TrimPrefix(filename, "file://"), snippet)...)

	retChan, err := s.Sourcegraph.ClaudeClient.GetCompletion(params, true)

	for completionResp := range retChan {
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

		params := lsp.PublishDiagnosticsParams{
			URI:         lsp.DocumentURI(filename),
			Diagnostics: diagnostics,
		}
		if err := conn.Notify(ctx, "textDocument/publishDiagnostics", params); err != nil {
			return err
		}
	}

	return nil
}
