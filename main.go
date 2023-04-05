package main

import (
	"context"
	"encoding/json"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/sourcegraph/go-lsp"
	"github.com/sourcegraph/jsonrpc2"
)

func PathToURI(path string) lsp.DocumentURI {
	path = filepath.ToSlash(path)
	parts := strings.SplitN(path, "/", 2)

	head := parts[0]
	if head != "" {
		head = "/" + head
	}

	rest := ""
	if len(parts) > 1 {
		rest = "/" + parts[1]
	}

	return lsp.DocumentURI("file://" + head + rest)
}

type Application struct{}

type stdrwc struct{}

func (stdrwc) Read(p []byte) (int, error) {
	return os.Stdin.Read(p)
}

func (stdrwc) Write(p []byte) (int, error) {
	return os.Stdout.Write(p)
}

func (stdrwc) Close() error {
	if err := os.Stdin.Close(); err != nil {
		return err
	}
	return os.Stdout.Close()
}

// lspHandler wraps LangHandler to correctly handle requests in the correct
// order.
//
// The LSP spec dictates a strict ordering that requests should only be
// processed serially in the order they are received. However, implementations
// are allowed to do concurrent computation if it doesn't affect the
// result. We actually can return responses out of order, since vscode does
// not seem to have issues with that. We also do everything concurrently,
// except methods which could mutate the state used by our typecheckers (ie
// textDocument/didOpen, etc). Those are done serially since applying them out
// of order could result in a different textDocument.
type lspHandler struct {
	jsonrpc2.Handler
}

// Handle implements jsonrpc2.Handler
func (h lspHandler) Handle(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) {
	go h.Handler.Handle(ctx, conn, req)
}

func constructPrompt() string {
	return ""
}

const serverEndpoint = "https://sourcegraph.sourcegraph.com"

var root lsp.DocumentURI

func newHandler() (jsonrpc2.Handler, io.Closer) {
	return jsonrpc2.HandlerWithError(func(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) (result interface{}, err error) {
		if req.Method == "initialize" {
			var params lsp.InitializeParams
			if err := json.Unmarshal(*req.Params, &params); err != nil {
				return nil, err
			}
			root = params.Root()
			kind := lsp.TDSKIncremental
			return lsp.InitializeResult{
				Capabilities: lsp.ServerCapabilities{
					TextDocumentSync: &lsp.TextDocumentSyncOptionsOrKind{
						Kind: &kind,
					},
					CompletionProvider:           nil,
					DefinitionProvider:           false,
					TypeDefinitionProvider:       false,
					DocumentFormattingProvider:   false,
					DocumentSymbolProvider:       false,
					HoverProvider:                false,
					ReferencesProvider:           false,
					RenameProvider:               false,
					WorkspaceSymbolProvider:      false,
					ImplementationProvider:       false,
					XWorkspaceReferencesProvider: false,
					XDefinitionProvider:          false,
					XWorkspaceSymbolByProperties: false,
					SignatureHelpProvider:        nil,
				},
			}, nil
			// return nil, sendDiagnostics(ctx, conn, "Diagnostics", "From here", []string{"main.go"})
		}
		if req.Method == "textDocument/didSave" {
			return nil, sendDiagnostics(ctx, conn, "Diagnostics", "go", []string{"/home/pjlast/workspace/llmsp/main.go"})
		}
		return nil, nil
	}), ioutil.NopCloser(strings.NewReader(""))
}

func sendDiagnostics(ctx context.Context, conn jsonrpc2.JSONRPC2, diags string, source string, files []string) error {
	params := lsp.PublishDiagnosticsParams{
		URI:         root + "/main.go",
		Diagnostics: make([]lsp.Diagnostic, 1),
	}
	params.Diagnostics[0] = lsp.Diagnostic{
		Range: lsp.Range{
			Start: lsp.Position{
				Line:      0,
				Character: 0,
			},
			End: lsp.Position{
				Line: 20,
			},
		},
		Severity: lsp.Info,
		Message:  "git gud bro",
	}
	if err := conn.Notify(ctx, "textDocument/publishDiagnostics", params); err != nil {
		return err
	}

	return nil
}

func main() {
	handler, closer := newHandler()

	<-jsonrpc2.NewConn(context.Background(), jsonrpc2.NewBufferedStream(stdrwc{}, jsonrpc2.VSCodeObjectCodec{}), handler).DisconnectNotify()
	err := closer.Close()
	if err != nil {
		log.Println(err)
	}
}
