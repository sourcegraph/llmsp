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
	GetCompletions(lsp.CompletionParams) ([]lsp.CompletionItem, error)
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

			return s.Provider.GetCompletions(params)

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
