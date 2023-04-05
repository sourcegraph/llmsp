package lsp

import (
	"context"
	"encoding/json"

	"github.com/sourcegraph/go-lsp"
	"github.com/sourcegraph/jsonrpc2"
)

var root lsp.DocumentURI

// Handle looks at the provided request and calls functions depending on the request method.
// The response, if any, is sent back over the connection.
func Handle() jsonrpc2.Handler {
	return jsonrpc2.HandlerWithError(func(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) (any, error) {
		switch req.Method {
		case "initialize":
			var params lsp.InitializeParams
			if err := json.Unmarshal(*req.Params, &params); err != nil {
				return nil, err
			}

			root = params.Root()

			return lsp.InitializeResult{}, nil

		case "initialized":
			return nil, nil

		case "textDocument/didSave":
			return nil, sendDiagnostics(ctx, conn, "diagnostics", "go", nil)
		}
		return nil, nil
	})
}

// sendDiagnostics sends the provided diagnostics back over the provided connection.
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
