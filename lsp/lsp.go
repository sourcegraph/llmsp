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
	// Mu is a mutex used for locking
	Mu sync.Mutex
	// Router contains the registered server routes
	Router *Router
}

// NewServer creates a new Server instance.
//
// url is the base URL of the API server.
// accessToken is the OAuth access token to use for requests.
func NewServer(url, accessToken string) *Server {
	s := &Server{
		FileMap:     make(types.MemoryFileMap),
		URL:         url,
		AccessToken: accessToken,
	}
	s.Router = NewRouter()
	s.Router.Register("initialize", s.Initialize())
	s.Router.Register("textDocument/didChange", s.TextDocumentDidChange())
	s.Router.Register("textDocument/didOpen", s.TextDocumentDidOpen())
	s.Router.Register("textDocument/codeAction", s.TextDocumentCodeAction())
	s.Router.Register("textDocument/completion", s.TextDocumentCompletion())
	s.Router.Register("workspace/didChangeConfiguration", s.WorkspaceDidChangeConfiguration())
	s.Router.Register("workspace/executeCommand", s.WorkspaceExecuteCommand())

	return s
}

func (s *Server) Initialize() HandlerFunc {
	return jsonrpc2.HandlerWithError(func(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) (any, error) {
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
	}).Handle
}

func (s *Server) TextDocumentDidChange() HandlerFunc {
	return jsonrpc2.HandlerWithError(func(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) (any, error) {
		var params lsp.DidChangeTextDocumentParams
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			return nil, err
		}

		s.Mu.Lock()
		s.FileMap[params.TextDocument.URI] = params.ContentChanges[0].Text
		defer s.Mu.Unlock()

		return nil, nil
	}).Handle
}

func (s *Server) TextDocumentDidOpen() HandlerFunc {
	return jsonrpc2.HandlerWithError(func(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) (any, error) {
		var params lsp.DidOpenTextDocumentParams
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			return nil, err
		}

		s.FileMap[params.TextDocument.URI] = params.TextDocument.Text

		return nil, nil
	}).Handle
}

func (s *Server) TextDocumentCodeAction() HandlerFunc {
	return jsonrpc2.HandlerWithError(func(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) (any, error) {
		if !s.initialized {
			return nil, nil
		}
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
	}).Handle
}

func (s *Server) TextDocumentCompletion() HandlerFunc {
	return jsonrpc2.HandlerWithError(func(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) (any, error) {
		if !s.initialized {
			return nil, nil
		}
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
	}).Handle
}

func (s *Server) WorkspaceDidChangeConfiguration() HandlerFunc {
	return jsonrpc2.HandlerWithError(func(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) (any, error) {
		var params types.DidChangeConfigurationParams
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			return nil, err
		}
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
	}).Handle
}

func (s *Server) WorkspaceExecuteCommand() HandlerFunc {
	return jsonrpc2.HandlerWithError(func(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) (any, error) {
		if !s.initialized {
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
	}).Handle
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
	ExecuteCommand(context.Context, lsp.Command, *jsonrpc2.Conn) (*json.RawMessage, error)
}
