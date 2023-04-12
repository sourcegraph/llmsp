package lsp

import (
	"context"
	"encoding/json"

	"github.com/pjlast/llmsp/providers"
	"github.com/pjlast/llmsp/types"
	"github.com/sourcegraph/go-lsp"
	"github.com/sourcegraph/jsonrpc2"
)

type Server struct {
	Provider LLMProvider
	FileMap  types.MemoryFileMap
}

type LLMProvider interface {
	Initialize(types.LLMSPSettings) error
	GetCompletions(context.Context, types.CompletionParams) ([]lsp.CompletionItem, error)
	GetCodeActions(lsp.DocumentURI, lsp.Range) []lsp.Command
	ExecuteCommand(context.Context, lsp.Command, *jsonrpc2.Conn) error
}

func (s *Server) Handle() jsonrpc2.Handler {
	return jsonrpc2.HandlerWithError(func(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) (any, error) {
		switch req.Method {
		case "initialize":
			var params lsp.InitializeParams
			if err := json.Unmarshal(*req.Params, &params); err != nil {
				return nil, err
			}
			provider := &providers.SourcegraphLLM{
				FileMap: s.FileMap,
			}
			s.Provider = provider

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
			var params types.CompletionParams
			if err := json.Unmarshal(*req.Params, &params); err != nil {
				return nil, err
			}

			completions, err := s.Provider.GetCompletions(ctx, params)
			if err != nil {
				return nil, nil
			}

			return types.CompletionList{
				IsIncomplete: false,
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

			return nil, nil

		case "workspace/executeCommand":
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
