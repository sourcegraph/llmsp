package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/google/uuid"
	"github.com/pjlast/llmsp/providers"
	"github.com/pjlast/llmsp/types"
	"github.com/sourcegraph/go-lsp"
	"github.com/sourcegraph/jsonrpc2"
)

// LSPHandler is a generic type for LSP Handlers that take parameters of type T.
type LSPHandler[T any] func(context.Context, *jsonrpc2.Conn, *jsonrpc2.Request, T) (any, error)

// LSPHandlerFunc takes an LSPHandler, wraps it in an error handler and unmarshals
// the request parameters before calling the provided handler.
func LSPHandlerFunc[T any](fn LSPHandler[T]) HandlerFunc {
	return jsonrpc2.HandlerWithError(
		func(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) (any, error) {
			var params T
			if err := json.Unmarshal(*req.Params, &params); err != nil {
				return nil, err
			}

			return fn(ctx, conn, req, params)
		},
	).Handle
}

type server struct {
	// initialized indicates whether the server has been initialized
	initialized bool
	// Provider is the language provider used by the server
	Provider LLMProvider
	// FileMap is a map of file URIs to file contents
	FileMap types.MemoryFileMap
	// URL is the URL of the Sourcegraph instance
	URL string
	// AccessToken is the access token used to authenticate to Sourcegraph
	AccessToken string
	// AutoComplete enables or disables autocompletion
	AutoComplete string
	// Debug enables debug logging
	Debug bool
	// Trace configures tracing
	Trace struct {
		// Enabled enables tracing
		Enabled bool
		// Verbose enables verbose tracing (all message parameters will be logged)
		Verbose bool
	}
	// mu is a mutex used for locking
	mu sync.Mutex
	// router contains the registered server routes
	router *Router
}

// NewServer creates a new server instance.
//
// url is the base URL of the API server.
// accessToken is the OAuth access token to use for requests.
func NewServer(url, accessToken string) *server {
	s := &server{
		FileMap:     make(types.MemoryFileMap),
		URL:         url,
		AccessToken: accessToken,
	}
	s.router = NewRouter()
	s.router.Register("initialize", LSPHandlerFunc(s.initialize))
	s.router.Register("textDocument/didChange", LSPHandlerFunc(s.textDocumentDidChange))
	s.router.Register("textDocument/didOpen", LSPHandlerFunc(s.textDocumentDidOpen))
	s.router.Register("textDocument/codeAction",
		s.requiresInitialized(LSPHandlerFunc(s.textDocumentCodeAction)))
	s.router.Register("textDocument/completion",
		s.requiresInitialized(LSPHandlerFunc(s.textDocumentCompletion)))
	s.router.Register("workspace/didChangeConfiguration",
		LSPHandlerFunc(s.workspaceDidChangeConfiguration))
	s.router.Register("workspace/executeCommand",
		s.requiresInitialized(LSPHandlerFunc(s.workspaceExecuteCommand)))

	return s
}

func (s *server) Handle(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) {
	s.router.Handle(ctx, conn, req)
}

func (s *server) requiresInitialized(handler jsonrpc2.Handler) jsonrpc2.Handler {
	if !s.initialized {
		return jsonrpc2.HandlerWithError(func(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) (any, error) {
			return nil, errors.New("server has not yet been initialized")
		})
	}

	return handler
}

func (s *server) initialize(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request, params lsp.InitializeParams) (any, error) {
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
		Commands: []string{"todos", "suggest", "answer", "docstring", "cody", "cody.explain", "cody.explainErrors", "cody.remember", "cody.forget", "cody.chat/history", "cody.chat/message"},
	}

	return types.InitializeResult{
		Capabilities: types.ServerCapabilities{
			TextDocumentSync:       &opts,
			CodeActionProvider:     true,
			CompletionProvider:     &completionOptions,
			ExecuteCommandProvider: &ecopts,
		},
	}, nil
}

func (s *server) textDocumentDidChange(_ context.Context, _ *jsonrpc2.Conn, _ *jsonrpc2.Request, params lsp.DidChangeTextDocumentParams) (any, error) {
	s.mu.Lock()
	s.FileMap[params.TextDocument.URI] = params.ContentChanges[0].Text
	s.mu.Unlock()

	return nil, nil
}

func (s *server) textDocumentDidOpen(_ context.Context, _ *jsonrpc2.Conn, _ *jsonrpc2.Request, params lsp.DidOpenTextDocumentParams) (any, error) {
	s.mu.Lock()
	s.FileMap[params.TextDocument.URI] = params.TextDocument.Text
	s.mu.Unlock()

	return nil, nil
}

func (s *server) textDocumentCodeAction(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request, params types.CodeActionParams) (any, error) {
	commands := s.Provider.GetCodeActions(params.TextDocument.URI, params.Range)
	for _, diagnostic := range params.Context.Diagnostics {
		commands = append(commands, lsp.Command{
			Title:     fmt.Sprintf("Explain error: %s", diagnostic.Message),
			Command:   "cody.explainErrors",
			Arguments: []any{diagnostic.Message},
		})
	}
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
}

func (s *server) textDocumentCompletion(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request, params types.CompletionParams) (any, error) {
	if s.AutoComplete == "" || s.AutoComplete == "off" {
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

	completions, err := s.Provider.GetCompletions(ctx, params)
	if err != nil {
		return nil, nil
	}

	return types.CompletionList{
		IsIncomplete: true,
		Items:        completions,
	}, nil
}

func (s *server) workspaceDidChangeConfiguration(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request, params types.DidChangeConfigurationParams) (any, error) {
	if params.Settings.LLMSP.Sourcegraph.AutoComplete != "" {
		s.AutoComplete = params.Settings.LLMSP.Sourcegraph.AutoComplete
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
}

func (s *server) workspaceExecuteCommand(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request, params types.ExecuteCommandParams) (any, error) {
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

	return s.Provider.ExecuteCommand(ctx, params, conn)
}

// LLMProvider is the interface for Language Server Protocol providers.
type LLMProvider interface {
	// Initialize initializes the LLM provider with the given settings.
	Initialize(types.LLMSPSettings) error
	// GetCompletions returns completion items for the given completion parameters.
	GetCompletions(context.Context, types.CompletionParams) ([]types.CompletionItem, error)
	// GetCodeActions returns the code actions for the given document URI and range.
	GetCodeActions(lsp.DocumentURI, lsp.Range) []lsp.Command
	// ExecuteCommand executes the given command and returns the result.
	ExecuteCommand(context.Context, types.ExecuteCommandParams, *jsonrpc2.Conn) (*json.RawMessage, error)
}
