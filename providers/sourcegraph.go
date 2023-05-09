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

const (
	charsPerToken        = 4
	maxPromptTokenLength = 7000
	maxCurrentFileTokens = 1000
)

type SourcegraphLLM struct {
	AnonymousUIDPath  string
	FileMap           types.MemoryFileMap
	EventLogger       *eventLogger
	EmbeddingsClient  *embeddings.Client
	ClaudeClient      *claude.Client
	URL               string
	AccessToken       string
	RepoID            string
	RepoName          string
	InteractionMemory []claude.Message
	Mu                sync.Mutex
	Context           *struct {
		context.Context
		CancelFunc context.CancelFunc
	}
}

// truncateTextStarts trims the beginning of the text, leaving only the last `maxTokens`.
func truncateTextStart(text string, maxTokens int) (string, int) {
	maxLength := maxTokens * charsPerToken
	if len(text) > maxLength {
		text = text[len(text)-maxLength:]
	}

	return text, getTokenLength(text)
}

// truncateText trims the end of the text, leaving only the first `maxTokens`.
func truncateText(text string, maxTokens int) (string, int) {
	maxLength := maxTokens * charsPerToken
	if len(text) > maxLength {
		text = text[:maxLength]
	}

	return text, getTokenLength(text)
}

func getTokenLength(text string) int {
	return (len(text) + charsPerToken - 1) / charsPerToken
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
		return strings.TrimPrefix(ext, ".")
	}
}

func (l *SourcegraphLLM) Initialize(settings types.LLMSPSettings) error {
	if settings.Sourcegraph == nil {
		return fmt.Errorf("Sourcegraph settings not present")
	}

	serverClient := embeddings.NewClient(l.URL, l.AccessToken, nil)
	dotcomClient := embeddings.NewClient(sourcegraphDotComURL, "", nil)

	l.URL = settings.Sourcegraph.URL
	l.AccessToken = settings.Sourcegraph.AccessToken
	l.EmbeddingsClient = serverClient
	l.ClaudeClient = claude.NewClient(l.URL, l.AccessToken, nil)
	l.InteractionMemory = make([]claude.Message, 0)
	l.AnonymousUIDPath = settings.Sourcegraph.AnonymousUIDFile
	l.EventLogger = NewEventLogger(serverClient, dotcomClient, l.URL, l.AnonymousUIDPath)

	gitURL := getGitURL()
	if gitURL != "" {
		repoName := getRepoName(gitURL)
		repoID, err := l.EmbeddingsClient.GetRepoID(repoName)
		// If we had no problem fetching the repo ID, we set the Repo ID and Name
		if err == nil {
			l.RepoID = repoID
			l.RepoName = repoName
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
	truncText, _ := truncateText(l.FileMap[params.TextDocument.URI], maxCurrentFileTokens)
	claudeParams.Messages = append(claudeParams.Messages,
		claude.Message{
			Speaker: claude.Human,
			Text: fmt.Sprintf(`Here are the contents of the file you are working in:
%s`, truncText),
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
		{
			Title:     "Cody: Remember this",
			Command:   "cody.remember",
			Arguments: []interface{}{doc, selection.Start.Line, selection.End.Line},
		},
	}
	if len(l.InteractionMemory) > 0 {
		commands = append(commands, lsp.Command{
			Title:   "Cody: Forget",
			Command: "cody.forget",
		})
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

func (l *SourcegraphLLM) ExecuteCommand(ctx context.Context, params types.ExecuteCommandParams, conn *jsonrpc2.Conn) (*json.RawMessage, error) {
	switch params.Command {
	case "suggest":
		filename := lsp.DocumentURI(params.Arguments[0].(string))
		startLine := params.Arguments[1].(float64)
		endLine := params.Arguments[2].(float64)
		snippet := getFileSnippet(l.FileMap[filename], int(startLine), int(endLine))
		snippet = numberLines(snippet, int(startLine))
		return nil, l.sendDiagnostics(ctx, conn, string(filename), snippet)

	case "docstring":
		filename := lsp.DocumentURI(params.Arguments[0].(string))
		startLine := int(params.Arguments[1].(float64))
		endLine := int(params.Arguments[2].(float64))
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
		filename := lsp.DocumentURI(params.Arguments[0].(string))
		startLine := int(params.Arguments[1].(float64))
		endLine := int(params.Arguments[2].(float64))
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
		filename := lsp.DocumentURI(params.Arguments[0].(string))
		startLine := int(params.Arguments[1].(float64))
		endLine := int(params.Arguments[2].(float64))
		instruction := params.Arguments[3].(string)
		overwrite := params.Arguments[4].(bool)
		codeOnly := params.Arguments[5].(bool)

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
		filename := lsp.DocumentURI(params.Arguments[0].(string))
		startLine := int(params.Arguments[1].(float64))
		endLine := int(params.Arguments[2].(float64))
		instruction := params.Arguments[3].(string)
		var codeOnly bool
		if len(params.Arguments) >= 5 {
			codeOnly = params.Arguments[4].(bool)
		}
		if codeOnly {
			l.EventLogger.Log("CodyNeovimExtension:codeAction:cody.explain:executed")
		} else {
			l.EventLogger.Log("CodyNeovimExtension:codeAction:cody.diff:executed")
		}

		funcSnippet := getFileSnippet(l.FileMap[filename], int(startLine), int(endLine))
		humanMessage := fmt.Sprintf(`%s
`+"```%s"+`
%s
`+"```", instruction, strings.ToLower(determineLanguage(string(filename))), funcSnippet)

		var embeddings *embeddings.EmbeddingsSearchResult
		if l.RepoID != "" {
			embeddings, _ = l.EmbeddingsClient.GetEmbeddings(l.RepoID, humanMessage, 8, 2)
		}
		params := claude.DefaultCompletionParameters(l.getMessages("", embeddings))
		var assistantText string
		if codeOnly {
			assistantText = fmt.Sprintf("```%s\n", strings.ToLower(determineLanguage(string(filename))))
		}

		params.Messages = append(params.Messages, codyDoPreamble(string(filename), l.FileMap[filename])...)
		params.Messages = append(params.Messages, l.InteractionMemory...)
		params.Messages = append(params.Messages,
			claude.Message{
				Speaker: claude.Human,
				Text:    humanMessage,
			},
			claude.Message{
				Speaker: claude.Assistant,
				Text:    assistantText,
			})
		retChan, _ := l.ClaudeClient.StreamCompletion(ctx, params, false)
		var finalMessage string
		for resp := range retChan {
			if codeOnly {
				if endCodeIndex := strings.Index(resp, "\n```"); endCodeIndex != -1 {
					resp = resp[:endCodeIndex]
				}
			}
			finalMessage = resp
			lines := strings.Split(strings.TrimSpace(resp), "\n")
			var splitLines []string
			for _, line := range lines {
				line = strings.TrimRight(line, " ")
				splitLines = append(splitLines, strings.TrimRight(line, " "))
			}

			jsonResponse := struct {
				Message []string `json:"message"`
			}{
				Message: splitLines,
			}
			mars, _ := json.Marshal(jsonResponse)
			msJson := json.RawMessage(mars)
			conn.Notify(ctx, "cody/chat", msJson)
			if codeOnly {
				if endCodeIndex := strings.Index(resp, "\n```"); endCodeIndex != -1 {
					break
				}
			}
		}
		if codeOnly {
			finalMessage = fmt.Sprintf("```%s\n%s\n```", strings.ToLower(determineLanguage(string(filename))), finalMessage)
		}
		l.InteractionMemory = append(l.InteractionMemory, claude.Message{Speaker: claude.Human, Text: humanMessage}, claude.Message{
			Speaker: claude.Assistant,
			Text:    finalMessage,
		},
		)
		return nil, nil

	case "cody.remember":
		filename := lsp.DocumentURI(params.Arguments[0].(string))
		startLine := int(params.Arguments[1].(float64))
		endLine := int(params.Arguments[2].(float64))
		l.EventLogger.Log("CodyNeovimExtension:codeAction:cody.remember:executed")

		funcSnippet := getFileSnippet(l.FileMap[filename], int(startLine), int(endLine))

		l.InteractionMemory = append(l.InteractionMemory, claude.Message{
			Speaker: claude.Human,
			Text: fmt.Sprintf(`Here is a snippet from the file "%s":
`+"```%s"+`
%s
`+"```", strings.TrimPrefix(string(filename), "file://"), strings.ToLower(determineLanguage(string(filename))), funcSnippet),
		}, claude.Message{
			Speaker: claude.Assistant,
			Text:    "Ok.",
		})

		return nil, nil

	case "cody.chat/history":
		mars, _ := json.Marshal(l.InteractionMemory)
		msJson := json.RawMessage(mars)

		return &msJson, nil

	case "cody.forget":
		l.EventLogger.Log("CodyNeovimExtension:codeAction:cody.forget:executed")
		l.InteractionMemory = nil

		return nil, nil

	case "cody.chat/message":
		l.EventLogger.Log("CodyNeovimExtension:codeAction:cody.chat:executed")
		filename := lsp.DocumentURI(params.Arguments[0].(string))
		message := params.Arguments[1].(string)

		input := []claude.Message{
			{
				Speaker: claude.Human,
				Text:    message,
			},
			{
				Speaker: claude.Assistant,
				Text:    "",
			},
		}

		params := claude.DefaultCompletionParameters(l.AddContext(input, string(filename), l.FileMap[filename]))
		codyResponse, err := l.ClaudeClient.GetCompletion(ctx, params, false)
		if err != nil {
			panic(err)
		}
		codyResponse = strings.TrimSpace(codyResponse)

		resp := struct {
			Message string `json:"message"`
		}{
			Message: codyResponse,
		}
		mars, _ := json.Marshal(resp)
		msJson := json.RawMessage(mars)
		l.InteractionMemory = append(l.InteractionMemory, claude.Message{
			Speaker: claude.Human,
			Text:    message,
		}, claude.Message{
			Speaker: claude.Assistant,
			Text:    codyResponse,
		})
		l.EventLogger.Log("CodyNeovimExtension:codeAction:cody.chat:executed")
		return &msJson, nil

	case "cody.explainErrors":
		lspErr := params.Arguments[0].(string)
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
		filename := lsp.DocumentURI(params.Arguments[0].(string))
		startLine := int(params.Arguments[1].(float64))
		endLine := int(params.Arguments[2].(float64))
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

	case "testCommand":
		if params.WorkDoneToken != "" {
			for i := 0; i < 5; i++ {
				time.Sleep(500 * time.Millisecond)
				conn.Notify(ctx, "$/progress", types.ProgressParams[claude.Message]{
					Token: params.WorkDoneToken,
					Value: claude.Message{
						Speaker: "Me",
						Text:    fmt.Sprintf("Hello %d", i),
					},
				})
			}
			conn.Notify(ctx, "$/progress", types.ProgressParams[types.WorkDoneProgressEnd]{
				Token: params.WorkDoneToken,
				Value: types.WorkDoneProgressEnd{
					Kind: "end",
				},
			})
		}
	}
	return nil, nil
}

func codyDoPreamble(filename, filecontents string) []claude.Message {
	return []claude.Message{
		{
			Speaker: claude.Human,
			Text: fmt.Sprintf(`Here are the contents of the file you are working in:
%s`, filecontents),
		},
		{
			Speaker: claude.Assistant,
			Text:    "Ok.",
		},
		{
			Speaker: claude.Human,
			Text:    fmt.Sprintf(`The programming language is %s`, determineLanguage(filename)),
		},
		{
			Speaker: claude.Assistant,
			Text:    "Ok.",
		},
	}
}

// reverseSlice reverses a slice in place
func reverseSlice[S ~[]E, E any](s S) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}

// trimMessages makes sure that msgs don't exceed maxTokens. It assumes
// that the most important messages are at the end of the slice.
// It returns the trimmed slice, as well as the number of tokens used.
func trimMessages(msgs []claude.Message, maxTokens int) ([]claude.Message, int) {
	if len(msgs) == 0 {
		return nil, 0
	}
	var trimmedMessages []claude.Message
	tokens := 0
	for i := len(msgs) - 1; i >= 0 && tokens < maxTokens; i-- {
		text, ts := truncateTextStart(msgs[i].Text, maxTokens-tokens)
		tokens += ts
		trimmedMessages = append(trimmedMessages, claude.Message{
			Speaker: msgs[i].Speaker,
			Text:    text,
		})
	}
	reverseSlice(trimmedMessages)
	// The messages _must_ start with a Human speaker
	if trimmedMessages[0].Speaker != claude.Human {
		tokens -= getTokenLength(trimmedMessages[0].Text)
		trimmedMessages = trimmedMessages[1:]
	}
	return trimmedMessages, tokens
}

func (l *SourcegraphLLM) AddContext(input []claude.Message, currentFile string, currentFileContents string) []claude.Message {
	tokens := maxPromptTokenLength
	messages := l.getPreamble()

	// First make sure we have space for the preamble
	for _, message := range messages {
		tokens -= getTokenLength(message.Text)
	}

	truncedContents, _ := truncateText(currentFileContents, maxCurrentFileTokens-10)
	// Also reserve some space for some of the contents of the current open file.
	currentFileMessages := []claude.Message{
		{
			Speaker: claude.Human,
			Text:    fmt.Sprintf("Here are the contents of the file, `%s`, we are in right now:\n%s", currentFile, truncedContents),
		},
		{
			Speaker: claude.Assistant,
			Text:    "Ok.",
		},
	}
	currentFileMessages, tokensUsed := trimMessages(currentFileMessages, maxCurrentFileTokens)
	tokens -= tokensUsed

	// Next make sure we have space for the actual prompt
	for _, message := range input {
		tokens -= getTokenLength(message.Text)
	}

	// Next we need to determine whether or not embeddings search is being used.
	// If it is, we split the remaining tokens between the interaction history
	// and the embedding results.
	maxEmbeddingsTokens := tokens / 2
	embeddingsMessages := []claude.Message{}
	if l.RepoID != "" {
		embs, err := l.EmbeddingsClient.GetEmbeddings(l.RepoID, input[len(input)-1].Text, 12, 3)
		// If embeddings fail for some reason, we don't want to end the interaction
		if err == nil && embs != nil {
			embeddingsResults := append(embs.CodeResults, embs.TextResults...)
			reverseSlice(embeddingsResults) // Reverse results so that they appear in ascending order of importance (least -> most)
			for _, embedding := range embeddingsResults {
				embeddingsMessages = append(embeddingsMessages, claude.Message{
					Speaker: claude.Human,
					Text:    fmt.Sprintf("Use the following text from file `%s`:\n%s", embedding.FileName, embedding.Content),
				}, claude.Message{Speaker: claude.Assistant, Text: "Ok."})
			}
		}
	}
	embeddingsMessages, tokensUsed = trimMessages(embeddingsMessages, maxEmbeddingsTokens)
	tokens -= tokensUsed

	messages = append(messages, embeddingsMessages...)
	messages = append(messages, currentFileMessages...)

	// Finally, the rest of the tokens can be used for interaction
	// history, starting from the last interaction.
	var reversedInteractionHistory []claude.Message
	for i := len(l.InteractionMemory) - 1; i >= 0 && tokens > 0; i-- {
		text, tokensUsed := truncateTextStart(l.InteractionMemory[i].Text, tokens)
		tokens -= tokensUsed
		reversedInteractionHistory = append(reversedInteractionHistory, claude.Message{
			Speaker: l.InteractionMemory[i].Speaker,
			Text:    text,
		})
	}
	// Then interaction history, but append in the correct order
	reverseSlice(reversedInteractionHistory)
	messages = append(messages, reversedInteractionHistory...)

	// And finally the actual input
	messages = append(messages, input...)

	return messages
}

func (l *SourcegraphLLM) codyDo(filename, filecontents, function, instruction string, codeOnly bool) string {
	var assistantText string
	if codeOnly {
		assistantText = fmt.Sprintf("```%s\n", strings.ToLower(determineLanguage(filename)))
	}
	input := []claude.Message{
		{
			Speaker: claude.Human,
			Text: fmt.Sprintf(`%s
%s`, instruction, function),
		},
		{
			Speaker: claude.Assistant,
			Text:    assistantText,
		},
	}
	params := claude.DefaultCompletionParameters(l.AddContext(input, filename, filecontents))
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

	l.InteractionMemory = append(l.InteractionMemory,
		claude.Message{
			Speaker: claude.Human,
			Text: fmt.Sprintf(`%s
%s`, instruction, function),
		},
		claude.Message{
			Speaker: claude.Assistant,
			Text:    implemented,
		})

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

func (l *SourcegraphLLM) getPreamble() []claude.Message {
	codyMessage := fmt.Sprintf(`I am Cody, an AI-powered coding assistant developed by Sourcegraph. I operate inside a Language Server Protocol implementation. My task is to help programmers with programming tasks in all programming languages.
I have access to your currently open files in the editor.
I will generate suggestions as concisely and clearly as possible.
I only suggest something if I am certain about my answer.`)
	if l.RepoName != "" {
		codyMessage += fmt.Sprintf("\nI have knowledge about the %s repository and can answer questions about it.", l.RepoName)
	}
	messages := []claude.Message{{
		Speaker: claude.Assistant,
		Text:    codyMessage,
	}}

	return messages
}

func (l *SourcegraphLLM) getMessages(filename string, embeddingResults *embeddings.EmbeddingsSearchResult) []claude.Message {
	codyMessage := fmt.Sprintf(`I am Cody, an AI-powered coding assistant developed by Sourcegraph. I operate inside a Language Server Protocol implementation. My task is to help programmers with programming tasks in all programming languages.
I have access to your currently open files in the editor.
I will generate suggestions as concisely and clearly as possible.
I only suggest something if I am certain about my answer.`)
	if l.RepoName != "" {
		codyMessage += fmt.Sprintf("\nI have knowledge about the %s repository and can answer questions about it.", l.RepoName)
	}
	messages := []claude.Message{{
		Speaker: claude.Assistant,
		Text:    codyMessage,
	}}
	for k, v := range l.FileMap {
		messages = append(messages, claude.Message{
			Speaker: claude.Human,
			Text: fmt.Sprintf(`Here are the contents of the file '%s':
%s`, k, v),
		},
			claude.Message{
				Speaker: claude.Assistant,
				Text:    "Ok.",
			})
	}
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
