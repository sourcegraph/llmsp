package lsp

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/google/uuid"
	"github.com/pjlast/llmsp/providers"
	"github.com/pjlast/llmsp/types"
	"github.com/sourcegraph/go-lsp"
	"github.com/sourcegraph/jsonrpc2"
)

type Server struct {
	initialized  bool
	Provider     LLMProvider
	FileMap      types.MemoryFileMap
	URL          string
	AccessToken  string
	AutoComplete bool
	Debug        bool
	Trace        struct {
		Enabled bool
		Verbose bool
	}
	Mu sync.Mutex
}

type LLMProvider interface {
	Initialize(types.LLMSPSettings) error
	GetCompletions(context.Context, types.CompletionParams) ([]types.CompletionItem, error)
	GetCodeActions(lsp.DocumentURI, lsp.Range) []lsp.Command
	ExecuteCommand(context.Context, lsp.Command, *jsonrpc2.Conn) (*json.RawMessage, error)
}

func (s *Server) Handle() jsonrpc2.Handler {
	if s.URL != "" && s.AccessToken != "" {
		provider := &providers.SourcegraphLLM{
			FileMap: s.FileMap,
		}
		settings := types.LLMSPSettings{
			Sourcegraph: &types.SourcegraphSettings{
				URL:          s.URL,
				AccessToken:  s.AccessToken,
				AutoComplete: "off",
			},
		}
		provider.Initialize(settings)
		s.Provider = provider
		s.initialized = true
	}
	return jsonrpc2.HandlerWithError(func(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) (any, error) {
		if s.Trace.Enabled {
			trace := types.LogTraceParams{
				Message: req.Method,
			}
			if s.Trace.Verbose {
				trace.Verbose = string(*req.Params)
			}
			go func() { conn.Notify(ctx, "$/logTrace", trace) }()
		}
		if !s.initialized && !(req.Method == "initialize" || req.Method == "workspace/didChangeConfiguration" || req.Method == "textDocument/didChange" || req.Method == "textDocument/didOpen") {
			conn.Notify(ctx, "window/logMessage", lsp.LogMessageParams{Type: lsp.MTWarning, Message: "LLMSP not yet initialized"})
			return nil, nil
		}

		switch req.Method {
		case "initialize":
			var params lsp.InitializeParams
			if err := json.Unmarshal(*req.Params, &params); err != nil {
				return nil, err
			}

			if !s.initialized && s.URL != "" && s.AccessToken != "" {
				provider := &providers.SourcegraphLLM{
					FileMap: s.FileMap,
				}
				provider.URL = s.URL
				provider.AccessToken = s.AccessToken
				s.Provider = provider
				if params.Trace == "messages" {
					s.Trace.Enabled = true
				} else if params.Trace == "verbose" {
					s.Trace.Enabled = true
					s.Trace.Verbose = true
				} else {
					s.Trace.Enabled = false
				}
				s.initialized = true
			}

			opts := lsp.TextDocumentSyncOptionsOrKind{
				Options: &lsp.TextDocumentSyncOptions{
					OpenClose: true,
					WillSave:  true,
					Change:    lsp.TDSKFull,
				},
			}
			completionOptions := types.CompletionOptions{
				WorkDoneProgress: true,
			}
			ecopts := lsp.ExecuteCommandOptions{
				Commands: []string{"todos", "suggest", "answer", "docstring", "cody", "cody.explain"},
			}

			return types.InitializeResult{
				Capabilities: types.ServerCapabilities{
					TextDocumentSync:       &opts,
					CodeActionProvider:     true,
					CompletionProvider:     &completionOptions,
					ExecuteCommandProvider: &ecopts,
				},
			}, nil

		case "initialized":
			return nil, nil

		case "textDocument/didChange":
			var params lsp.DidChangeTextDocumentParams
			if err := json.Unmarshal(*req.Params, &params); err != nil {
				return nil, err
			}

			s.Mu.Lock()
			s.FileMap[params.TextDocument.URI] = params.ContentChanges[0].Text
			defer s.Mu.Unlock()

			return nil, nil

		case "textDocument/didOpen":
			var params lsp.DidOpenTextDocumentParams
			if err := json.Unmarshal(*req.Params, &params); err != nil {
				return nil, err
			}

			s.FileMap[params.TextDocument.URI] = params.TextDocument.Text

			return nil, nil

		case "textDocument/codeAction":
			var params types.CodeActionParams
			if err := json.Unmarshal(*req.Params, &params); err != nil {
				return nil, err
			}

			commands := s.Provider.GetCodeActions(params.TextDocument.URI, params.Range)
			if len(params.Context.Only) > 0 {
				filteredCommands := []lsp.Command{}
				for _, command := range commands {
					for _, filteredCommand := range params.Context.Only {
						if filteredCommand == command.Command {
							filteredCommands = append(filteredCommands, command)
							break
						}
					}
				}

				return filteredCommands, nil
			}
			return commands, nil

		case "textDocument/completion":
			if !s.AutoComplete {
				return nil, nil
			}
			uuid := uuid.New().String()
			var res any
			conn.Call(ctx, "window/workDoneProgress/create", types.WorkDoneProgressCreateParams{
				Token: uuid,
			}, &res)
			conn.Notify(ctx, "$/progress", types.ProgressParams[types.WorkDoneProgressBegin]{
				Token: uuid,
				Value: types.WorkDoneProgressBegin{
					Title:   "Completion",
					Kind:    "begin",
					Message: "Fetching completion...",
				},
			})
			defer conn.Notify(ctx, "$/progress", types.ProgressParams[types.WorkDoneProgressEnd]{
				Token: uuid,
				Value: types.WorkDoneProgressEnd{
					Message: "Completion fetched",
					Kind:    "end",
				},
			})
			var params types.CompletionParams
			if err := json.Unmarshal(*req.Params, &params); err != nil {
				return nil, err
			}

			completions, err := s.Provider.GetCompletions(ctx, params)
			if err != nil {
				return nil, nil
			}

			return types.CompletionList{
				IsIncomplete: true,
				Items:        completions,
			}, nil

		case "workspace/didChangeConfiguration":
			var params types.DidChangeConfigurationParams
			if err := json.Unmarshal(*req.Params, &params); err != nil {
				return nil, err
			}
			if !s.initialized {

				provider := &providers.SourcegraphLLM{
					FileMap: s.FileMap,
				}
				if err := provider.Initialize(params.Settings.LLMSP); err != nil {
					return nil, err
				}
				s.Provider = provider
				s.initialized = true
			}
			conn.Notify(ctx, "window/logMessage", lsp.LogMessageParams{Type: lsp.MTWarning, Message: "LLMSP initialized!"})

			return nil, nil

		case "workspace/executeCommand":
			uuid := uuid.New().String()
			var res any
			conn.Call(ctx, "window/workDoneProgress/create", types.WorkDoneProgressCreateParams{
				Token: uuid,
			}, &res)
			conn.Notify(ctx, "$/progress", types.ProgressParams[types.WorkDoneProgressBegin]{
				Token: uuid,
				Value: types.WorkDoneProgressBegin{
					Title:   "Code actions",
					Kind:    "begin",
					Message: "Computing code actions...",
				},
			})
			defer conn.Notify(ctx, "$/progress", types.ProgressParams[types.WorkDoneProgressEnd]{
				Token: uuid,
				Value: types.WorkDoneProgressEnd{
					Message: "Code actions computed",
					Kind:    "end",
				},
			})
			var command lsp.Command
			if err := json.Unmarshal(*req.Params, &command); err != nil {
				return nil, err
			}

			return s.Provider.ExecuteCommand(ctx, command, conn)

			// return nil, nil
		}
		return nil, nil
	})
}
