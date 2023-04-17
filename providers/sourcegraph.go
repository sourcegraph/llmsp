package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pjlast/llmsp/claude"
	"github.com/pjlast/llmsp/sourcegraph/embeddings"
	"github.com/pjlast/llmsp/types"
	"github.com/sourcegraph/go-lsp"
	"github.com/sourcegraph/jsonrpc2"
)

type SourcegraphLLM struct {
	FileMap          types.MemoryFileMap
	EmbeddingsClient *embeddings.Client
	ClaudeClient     *claude.Client
	URL              string
	AccessToken      string
	RepoID           string
	RepoName         string
	Mu               sync.Mutex
	Context          *struct {
		context.Context
		CancelFunc context.CancelFunc
	}
}

func getGitURL() string {
	out, err := exec.Command("git", "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func commentPrefix(language string) string {
	switch language {
	case "Go":
		return "//"
	case "Python":
		return "#"
	case "JavaScript":
		return "//"
	case "TypeScript":
		return "//"
	case "TypeScript React":
		return "//"
	case "Java":
		return "//"
	case "C":
		return "//"
	case "C++":
		return "//"
	case "Lua":
		return "--"
	case "Ruby":
		return "#"
	case "PHP":
		return "#"
	case "C#":
		return "//"
	default:
		return ""
	}
}

func determineLanguage(filename string) string {
	ext := filepath.Ext(filename)
	switch ext {
	case ".go":
		return "Go"
	case ".py":
		return "Python"
	case ".js":
		return "JavaScript"
	case ".ts":
		return "TypeScript"
	case ".tsx":
		return "TypeScript React"
	case ".java":
		return "Java"
	case ".c":
		return "C"
	case ".cpp":
		return "C++"
	case ".lua":
		return "Lua"
	case ".rb":
		return "Ruby"
	case ".php":
		return "PHP"
	case ".cs":
		return "C#"
	default:
		return "Unknown"
	}
}

func truncateText(text string, maxTokens int) string {
	maxLength := maxTokens * 4
	if len(text) < maxLength {
		return text
	}
	return text[:maxLength]
}

func truncateTextStart(text string, maxTokens int) string {
	maxLength := maxTokens * 4
	if len(text) < maxLength {
		return text
	}
	return text[len(text)-maxLength:]
}

func (l *SourcegraphLLM) Initialize(settings types.LLMSPSettings) error {
	if settings.Sourcegraph == nil {
		return fmt.Errorf("Sourcegraph settings not present")
	}

	l.URL = settings.Sourcegraph.URL
	l.AccessToken = settings.Sourcegraph.AccessToken
	l.EmbeddingsClient = embeddings.NewClient(l.URL, l.AccessToken, nil)
	l.ClaudeClient = claude.NewClient(l.URL, l.AccessToken, nil)

	gitURL := getGitURL()
	if gitURL != "" {
		repoName := getRepoName(gitURL)

		for _, rn := range settings.Sourcegraph.RepoEmbeddings {
			if rn == repoName {
				repoID, err := l.EmbeddingsClient.GetRepoID(repoName)
				if err != nil {
				} else {
					l.RepoID = repoID
					l.RepoName = rn
				}
			}
		}
	}

	return nil
}

func getRepoName(gitURL string) string {
	// Check if URL contains @ but not :// (SSH URL)
	if strings.Contains(gitURL, "@") && !strings.Contains(gitURL, "://") {
		// Split on @ and : to get base URL and repo name
		urlAndRepo := strings.Split(strings.Split(gitURL, "@")[1], ":")
		baseURL := urlAndRepo[0]
		repoName := baseURL + "/" + strings.TrimSuffix(urlAndRepo[1], ".git")
		return repoName
	} else if strings.Contains(gitURL, "://") { // Check if URL contains :// (HTTPS URL)
		// Split on :// to get protocol and rest of URL
		urlParts := strings.Split(gitURL, "://")
		// Check if rest of URL contains @ (for GitHub URLs)
		if strings.Contains(urlParts[1], "@") {
			// Split on @ to get base URL
			urlParts = strings.Split(urlParts[1], "@")
			baseURL := urlParts[1]
			repoName := strings.TrimSuffix(baseURL, ".git")
			return repoName
		}
		// No @, so base URL is entire part after ://
		baseURL := urlParts[1]
		repoName := strings.TrimSuffix(baseURL, ".git")
		return repoName
	} else { // Otherwise assume URL is just repo path
		repoName := gitURL + "/" + strings.TrimSuffix(strings.Split(gitURL, "/")[1], ".git")
		return repoName
	}
}

func (l *SourcegraphLLM) GetCompletions(ctx context.Context, params types.CompletionParams) ([]types.CompletionItem, error) {
	currentLine := strings.Split(l.FileMap[params.TextDocument.URI], "\n")[params.Position.Line]
	indentation := currentLine[:len(currentLine)-len(strings.TrimLeft(currentLine, " \t"))]
	l.Mu.Lock()
	if l.Context != nil {
		l.Context.CancelFunc()
	}
	ctx, cancel := context.WithCancel(ctx)

	l.Context = &struct {
		context.Context
		CancelFunc context.CancelFunc
	}{ctx, cancel}
	l.Mu.Unlock()
	time.Sleep(100 * time.Millisecond) // To not spam the server when rapidly typing
	if ctx.Err() != nil {
		return nil, fmt.Errorf("context canceled")
	}

	// startLine := params.Position.Line - 20
	// if params.Position.Line < 20 {
	// 	startLine = 0
	// }
	snippet := getFileSnippet(l.FileMap[params.TextDocument.URI], params.Position.Line, params.Position.Line)

	var embeddings *embeddings.EmbeddingsSearchResult = nil
	var err error
	if l.RepoID != "" {
		embeddings, _ = l.EmbeddingsClient.GetEmbeddings(l.RepoID, snippet, 8, 0)
	}
	claudeParams := claude.DefaultCompletionParameters(l.getMessages(string(params.TextDocument.URI), embeddings))
	claudeParams.Messages = append(claudeParams.Messages,
		claude.Message{
			Speaker: claude.Human,
			Text: fmt.Sprintf(`Here are the contents of the file you are working in:
%s`, truncateText(l.FileMap[params.TextDocument.URI], 1000)),
		},
		claude.Message{
			Speaker: claude.Assistant,
			Text:    "Ok.",
		},
		claude.Message{
			Speaker: claude.Human,
			Text: fmt.Sprintf(`Suggest a %s code snippet to complete the following code. Continue from where I left off:
%s`, determineLanguage(string(params.TextDocument.URI)), snippet),
		},
		claude.Message{
			Speaker: claude.Assistant,
			Text:    "```go\n",
		})
	completion, err := l.ClaudeClient.GetCompletion(ctx, claudeParams, false)
	if err != nil {
		return nil, err
	}
	completion = completion[:strings.Index(completion, "\n```")]
	completionLines := strings.Split(completion, "\n")
	for i, line := range completionLines {
		completionLines[i] = indentation + line
	}
	textCompletion := strings.Join(completionLines, "\n")

	textEdit := &lsp.TextEdit{
		Range: lsp.Range{
			Start: lsp.Position{
				Line: params.Position.Line,
			},
			End: params.Position,
		},
		NewText: textCompletion,
	}
	return []types.CompletionItem{
		{
			Label:    completion,
			Kind:     lsp.CIKSnippet,
			TextEdit: textEdit,
			Detail:   completion,
		},
	}, nil
}

func (l *SourcegraphLLM) GetCodeActions(doc lsp.DocumentURI, selection lsp.Range) []lsp.Command {
	cp := commentPrefix(determineLanguage(string(doc)))
	commands := []lsp.Command{
		{
			Title:     "Provide suggestions",
			Command:   "suggest",
			Arguments: []interface{}{doc, selection.Start.Line, selection.End.Line},
		},
		{
			Title:     "Generate docstring",
			Command:   "docstring",
			Arguments: []interface{}{doc, selection.Start.Line, selection.End.Line},
		},
	}
	if strings.Contains(strings.Join(strings.Split(l.FileMap[doc], "\n")[selection.Start.Line:selection.End.Line+1], "\n"), fmt.Sprintf("%s TODO", cp)) {
		commands = append(commands, lsp.Command{
			Title:     "Implement TODOs",
			Command:   "todos",
			Arguments: []interface{}{doc, selection.Start.Line, selection.End.Line},
		})
	}
	if strings.Contains(strings.Join(strings.Split(l.FileMap[doc], "\n")[selection.Start.Line:selection.End.Line+1], "\n"), fmt.Sprintf("%s ASK", cp)) {
		commands = append(commands, lsp.Command{
			Title:     "Answer question",
			Command:   "answer",
			Arguments: []interface{}{doc, selection.Start.Line, selection.End.Line},
		})
	}
	return commands
}

func (l *SourcegraphLLM) ExecuteCommand(ctx context.Context, cmd lsp.Command, conn *jsonrpc2.Conn) (*json.RawMessage, error) {
	switch cmd.Command {
	case "suggest":
		filename := lsp.DocumentURI(cmd.Arguments[0].(string))
		startLine := cmd.Arguments[1].(float64)
		endLine := cmd.Arguments[2].(float64)
		snippet := getFileSnippet(l.FileMap[filename], int(startLine), int(endLine))
		snippet = numberLines(snippet, int(startLine))
		return nil, l.sendDiagnostics(ctx, conn, string(filename), snippet)

	case "docstring":
		filename := lsp.DocumentURI(cmd.Arguments[0].(string))
		startLine := int(cmd.Arguments[1].(float64))
		endLine := int(cmd.Arguments[2].(float64))
		funcSnippet := getFileSnippet(l.FileMap[filename], int(startLine), int(endLine))
		docstring := l.getDocString(string(filename), funcSnippet)

		edits := []lsp.TextEdit{
			{
				Range: lsp.Range{
					Start: lsp.Position{
						Line:      startLine,
						Character: 0,
					},
					End: lsp.Position{
						Line:      endLine,
						Character: len(strings.Split(l.FileMap[filename], "\n")[endLine]),
					},
				},
				NewText: docstring + "\n" + funcSnippet,
			},
		}

		editParams := types.ApplyWorkspaceEditParams{
			Edit: types.WorkspaceEdit{
				DocumentChanges: []types.TextDocumentEdit{
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

		var res json.RawMessage
		go func() { conn.Call(ctx, "workspace/applyEdit", editParams, &res) }()
		return nil, nil

	case "todos":
		filename := lsp.DocumentURI(cmd.Arguments[0].(string))
		startLine := int(cmd.Arguments[1].(float64))
		endLine := int(cmd.Arguments[2].(float64))
		funcSnippet := getFileSnippet(l.FileMap[filename], int(startLine), int(endLine))
		implemented := l.implementTODOs(string(filename), l.FileMap[filename], funcSnippet)

		edits := []lsp.TextEdit{
			{
				Range: lsp.Range{
					Start: lsp.Position{
						Line:      startLine,
						Character: 0,
					},
					End: lsp.Position{
						Line:      endLine,
						Character: len(strings.Split(l.FileMap[filename], "\n")[endLine]),
					},
				},
				NewText: implemented,
			},
		}

		editParams := types.ApplyWorkspaceEditParams{
			Edit: types.WorkspaceEdit{
				DocumentChanges: []types.TextDocumentEdit{
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

		var res json.RawMessage
		conn.Call(ctx, "workspace/applyEdit", editParams, &res)

	case "cody":
		filename := lsp.DocumentURI(cmd.Arguments[0].(string))
		startLine := int(cmd.Arguments[1].(float64))
		endLine := int(cmd.Arguments[2].(float64))
		instruction := cmd.Arguments[3].(string)
		overwrite := cmd.Arguments[4].(bool)
		codeOnly := cmd.Arguments[5].(bool)

		funcSnippet := getFileSnippet(l.FileMap[filename], int(startLine), int(endLine))
		implemented := l.codyDo(string(filename), l.FileMap[filename], funcSnippet, instruction, codeOnly)

		if !overwrite {
			implemented += funcSnippet
		}

		edits := []lsp.TextEdit{
			{
				Range: lsp.Range{
					Start: lsp.Position{
						Line:      startLine,
						Character: 0,
					},
					End: lsp.Position{
						Line:      endLine,
						Character: len(strings.Split(l.FileMap[filename], "\n")[endLine]),
					},
				},
				NewText: implemented,
			},
		}

		editParams := types.ApplyWorkspaceEditParams{
			Edit: types.WorkspaceEdit{
				DocumentChanges: []types.TextDocumentEdit{
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

		var res json.RawMessage
		conn.Call(ctx, "workspace/applyEdit", editParams, &res)

	case "cody.explain":
		filename := lsp.DocumentURI(cmd.Arguments[0].(string))
		startLine := int(cmd.Arguments[1].(float64))
		endLine := int(cmd.Arguments[2].(float64))
		instruction := cmd.Arguments[3].(string)
		var codeOnly bool
		if len(cmd.Arguments) >= 5 {
			codeOnly = cmd.Arguments[4].(bool)
		}

		funcSnippet := getFileSnippet(l.FileMap[filename], int(startLine), int(endLine))
		implemented := l.codyDo(string(filename), l.FileMap[filename], funcSnippet, instruction, codeOnly)
		if codeOnly {
			implemented = fmt.Sprintf("```%s\n%s\n```", strings.ToLower(determineLanguage(string(filename))), implemented)
		}

		maxWidth := 80
		lines := strings.Split(strings.TrimSpace(implemented), "\n")
		var splitLines []string
		for _, line := range lines {
			line = strings.TrimRight(line, " ")
			words := strings.Split(line, " ")
			var lineWords []string
			lineLen := 0
			for _, word := range words {
				if lineLen+len(word)+1 < maxWidth {
					lineWords = append(lineWords, word)
					lineLen += len(word) + 1
				} else {
					splitLines = append(splitLines, strings.Join(lineWords, " "))
					lineWords = []string{word}
					lineLen = len(word)
				}
			}
			if len(lineWords) > 0 {
				splitLines = append(splitLines, strings.Join(lineWords, " "))
			}
		}

		resp := struct {
			Message []string `json:"message"`
		}{
			Message: splitLines,
		}
		mars, _ := json.Marshal(resp)
		msJson := json.RawMessage(mars)

		return &msJson, nil

	case "cody.explainErrors":
		lspErr := cmd.Arguments[0].(string)
		message := []claude.Message{{
			Speaker: claude.Human,
			Text:    fmt.Sprintf("Explain the following error: %s", lspErr),
		}, {
			Speaker: claude.Assistant,
			Text:    "",
		}}
		params := claude.DefaultCompletionParameters(message)
		completion, err := l.ClaudeClient.GetCompletion(ctx, params, false)
		if err != nil {
			conn.Notify(ctx, "window/logMessage", lsp.LogMessageParams{Type: lsp.MTError, Message: fmt.Sprintf("%v", err)})
			return nil, err
		}

		conn.Notify(ctx, "window/logMessage", lsp.LogMessageParams{Type: lsp.MTError, Message: completion})

		resp := struct {
			Answer string `json:"answer"`
		}{
			Answer: completion,
		}
		ms, err := json.Marshal(resp)
		if err != nil {
			return nil, err
		}
		msJson := json.RawMessage(ms)

		return &msJson, nil

	case "answer":
		filename := lsp.DocumentURI(cmd.Arguments[0].(string))
		startLine := int(cmd.Arguments[1].(float64))
		endLine := int(cmd.Arguments[2].(float64))
		funcSnippet := getFileSnippet(l.FileMap[filename], int(startLine), int(endLine))
		implemented := l.answerQuestions(string(filename), l.FileMap[filename], funcSnippet)

		edits := []lsp.TextEdit{
			{
				Range: lsp.Range{
					Start: lsp.Position{
						Line:      startLine,
						Character: 0,
					},
					End: lsp.Position{
						Line:      endLine,
						Character: len(strings.Split(l.FileMap[filename], "\n")[endLine]),
					},
				},
				NewText: implemented,
			},
		}

		editParams := types.ApplyWorkspaceEditParams{
			Edit: types.WorkspaceEdit{
				DocumentChanges: []types.TextDocumentEdit{
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

		var res json.RawMessage
		conn.Call(ctx, "workspace/applyEdit", editParams, &res)
	}
	return nil, nil
}

func (l *SourcegraphLLM) codyDo(filename, filecontents, function, instruction string, codeOnly bool) string {
	var embeddings *embeddings.EmbeddingsSearchResult
	if l.RepoID != "" {
		embeddings, _ = l.EmbeddingsClient.GetEmbeddings(l.RepoID, instruction, 8, 2)
	}
	params := claude.DefaultCompletionParameters(l.getMessages(filename, embeddings))
	var assistantText string
	if codeOnly {
		assistantText = fmt.Sprintf("```%s\n", strings.ToLower(determineLanguage(filename)))
	}
	params.Messages = append(params.Messages,
		claude.Message{
			Speaker: claude.Human,
			Text: fmt.Sprintf(`Here are the contents of the file you are working in:
%s`, filecontents),
		},
		claude.Message{
			Speaker: claude.Assistant,
			Text:    "Ok.",
		},
		claude.Message{
			Speaker: claude.Human,
			Text:    fmt.Sprintf(`The programming language is %s`, determineLanguage(filename)),
		},
		claude.Message{
			Speaker: claude.Assistant,
			Text:    "Ok.",
		},
		claude.Message{
			Speaker: claude.Human,
			Text: fmt.Sprintf(`%s
%s`, instruction, function),
		},
		claude.Message{
			Speaker: claude.Assistant,
			Text:    assistantText,
		})
	implemented, err := l.ClaudeClient.GetCompletion(context.Background(), params, true)
	if err != nil {
		return ""
	}
	if codeOnly {
		if index := strings.Index(implemented, "\n```"); index != -1 {
			implemented = implemented[:index]
		}
		implemented = strings.TrimPrefix(implemented, fmt.Sprintf("```%s\n", strings.ToLower(determineLanguage(filename))))
	}

	return implemented
}

func (l *SourcegraphLLM) implementTODOs(filename, filecontents, function string) string {
	params := claude.DefaultCompletionParameters(l.getMessages(filename, nil))
	params.Messages = append(params.Messages,
		claude.Message{
			Speaker: claude.Human,
			Text: fmt.Sprintf(`Here are the contents of the file you are working in:
%s`, filecontents),
		},
		claude.Message{
			Speaker: claude.Assistant,
			Text:    "Ok.",
		},
		claude.Message{
			Speaker: claude.Human,
			Text: fmt.Sprintf(`The following %s code contains TODO instructions. Produce code that will implement the TODO. Don't say anything else.
Here is the code snippet:
%s`, determineLanguage(filename), function),
		},
		claude.Message{
			Speaker: claude.Assistant,
			Text:    fmt.Sprintf("```%s", strings.ToLower(determineLanguage(filename))),
		})
	implemented, err := l.ClaudeClient.GetCompletion(context.Background(), params, true)
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(strings.TrimPrefix(implemented, fmt.Sprintf("```%s\n", strings.ToLower(determineLanguage(filename)))), "\n```")
}

func (l *SourcegraphLLM) answerQuestions(filename, filecontents, question string) string {
	cp := commentPrefix(determineLanguage(filename))
	question = strings.TrimPrefix(strings.TrimSpace(question), fmt.Sprintf("%s ASK: ", cp))
	var embeddings *embeddings.EmbeddingsSearchResult = nil
	var err error
	if l.RepoID != "" {
		embeddings, _ = l.EmbeddingsClient.GetEmbeddings(l.RepoID, question, 8, 2)
	}
	params := claude.DefaultCompletionParameters(l.getMessages(filename, embeddings))
	params.Messages = append(params.Messages,
		claude.Message{
			Speaker: claude.Human,
			Text: fmt.Sprintf(`Here are the contents of the file you are working in:
%s`, filecontents),
		},
		claude.Message{
			Speaker: claude.Assistant,
			Text:    "Ok.",
		},
		claude.Message{
			Speaker: claude.Human,
			Text: fmt.Sprintf(`Answer this question. Prepend each line with `+fmt.Sprintf("`%s`", cp)+` since you are in a code editor.

%s`, question),
		},
		claude.Message{
			Speaker: claude.Assistant,
			Text:    cp + " ANSWER: ",
		})
	answer, err := l.ClaudeClient.GetCompletion(context.Background(), params, true)
	if err != nil {
		return ""
	}
	return cp + " ASK: " + question + "\n" + answer
}

// sendDiagnostics sends the provided diagnostics back over the provided connection.
func (l *SourcegraphLLM) sendDiagnostics(ctx context.Context, conn jsonrpc2.JSONRPC2, filename, snippet string) error {
	repoID, err := l.EmbeddingsClient.GetRepoID("github.com/sourcegraph/sourcegraph")
	if err != nil {
		return err
	}
	var embeddingResults *embeddings.EmbeddingsSearchResult = nil
	if l.RepoID != "" {
		embeddingResults, _ = l.EmbeddingsClient.GetEmbeddings(repoID, snippet, 8, 0)
	}

	params := claude.DefaultCompletionParameters(l.getMessages(filename, embeddingResults))
	params.Messages = append(params.Messages, getSuggestionMessages(strings.TrimPrefix(filename, "file://"), snippet)...)

	retChan, err := l.ClaudeClient.StreamCompletion(ctx, params, true)

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

func (l *SourcegraphLLM) getDocString(filename, function string) string {
	cp := commentPrefix(determineLanguage(filename))
	params := claude.DefaultCompletionParameters(l.getMessages(filename, nil))
	params.Messages = append(params.Messages, claude.Message{
		Speaker: claude.Human,
		Text: fmt.Sprintf(`Generate a doc string explaining the use of the following %s function:
%s

Don't include the function in your output.`, determineLanguage(filename), function),
	},
		claude.Message{
			Speaker: claude.Assistant,
			Text:    cp,
		})
	docstring, err := l.ClaudeClient.GetCompletion(context.Background(), params, true)
	if err != nil {
		return ""
	}
	return docstring
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
			Speaker: claude.Human,
			Text: fmt.Sprintf(`Suggest improvements to following lines of code in the file '%s':
%s

Suggest improvements in the format:
Line {number}: {suggestion}`, filename, content),
		}, {
			Speaker: claude.Assistant,
			Text:    "Line",
		},
	}
}

func (l *SourcegraphLLM) getMessages(filename string, embeddingResults *embeddings.EmbeddingsSearchResult) []claude.Message {
	codyMessage := fmt.Sprintf(`I am Cody, an AI-powered coding assistant developed by Sourcegraph. I operate inside a Language Server Protocol implementation. My task is to help programmers with programming tasks in the %s programming language.
I have access to your currently open files in the editor.
I will generate suggestions as concisely and clearly as possible.
I only suggest something if I am certain about my answer.`, determineLanguage(filename))
	if l.RepoName != "" {
		codyMessage += fmt.Sprintf("\nI have knowledge about the %s repository and can answer questions about it.", l.RepoName)
	}
	messages := []claude.Message{{
		Speaker: claude.Assistant,
		Text:    codyMessage,
	}}
	if embeddingResults != nil {
		for _, embedding := range embeddingResults.CodeResults {
			messages = append(messages, claude.Message{
				Speaker: claude.Human,
				Text: fmt.Sprintf(`Here are the contents of the file '%s':
%s`, embedding.FileName, embedding.Content),
			}, claude.Message{Speaker: claude.Assistant, Text: "Ok."})
		}
	}

	return messages
}
