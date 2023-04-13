package lsp

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/pjlast/llmsp/providers"
	"github.com/pjlast/llmsp/types"
	"github.com/sourcegraph/go-lsp"
	"github.com/sourcegraph/jsonrpc2"
)

type Server struct {
	initialized bool
	Provider    LLMProvider
	FileMap     types.MemoryFileMap
	URL         string
	AccessToken string
	Debug       bool
}

type LLMProvider interface {
	Initialize(types.LLMSPSettings) error
	GetCompletions(context.Context, types.CompletionParams) ([]lsp.CompletionItem, error)
	GetCodeActions(lsp.DocumentURI, lsp.Range) []lsp.Command
	ExecuteCommand(context.Context, lsp.Command, *jsonrpc2.Conn) error
}

func (s *Server) Handle() jsonrpc2.Handler {
	return jsonrpc2.HandlerWithError(func(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) (any, error) {
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
			provider := &providers.SourcegraphLLM{
				FileMap: s.FileMap,
			}
			if s.URL != "" && s.AccessToken != "" {
				provider.URL = s.URL
				provider.AccessToken = s.AccessToken
			}
			s.Provider = provider
			s.initialized = true

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
				Commands: []string{"todos", "suggest", "answer", "docstring"},
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

			s.FileMap[params.TextDocument.URI] = params.ContentChanges[0].Text

			return nil, nil

		case "textDocument/didOpen":
			var params lsp.DidOpenTextDocumentParams
			if err := json.Unmarshal(*req.Params, &params); err != nil {
				return nil, err
			}

			s.FileMap[params.TextDocument.URI] = params.TextDocument.Text

			return nil, nil

		case "textDocument/codeAction":
			var params lsp.CodeActionParams
			if err := json.Unmarshal(*req.Params, &params); err != nil {
				return nil, err
			}

			return s.Provider.GetCodeActions(params.TextDocument.URI, params.Range), nil

		case "textDocument/completion":
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
			provider := &providers.SourcegraphLLM{
				FileMap: s.FileMap,
			}
			if err := provider.Initialize(params.Settings.LLMSP); err != nil {
				return nil, err
			}
			s.Provider = provider
			s.initialized = true
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

			s.Provider.ExecuteCommand(ctx, command, conn)

			return nil, nil
		}
		return nil, nil
	})
}
