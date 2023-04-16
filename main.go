package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/pjlast/llmsp/lsp"
	"github.com/sourcegraph/jsonrpc2"
)

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

const (
	urlFlag  = "url"
	urlUsage = "LLM provider URL"

	tokenFlag  = "token"
	tokenUsage = "LLM provider token"

	debugFlag  = "debug"
	debugUsage = "Debug mode"

	stdioFlag  = "stdio"
	stdioUsage = "Stdio mode"

	autoCompleteFlag  = "auto-complete"
	autoCompleteUsage = "Enable auto-completion (off, init, always)"
)

func main() {
	var (
		url   string
		token string
		// debug        bool
		autoComplete string
	)

	flag.StringVar(&url, urlFlag, "", urlUsage)
	flag.StringVar(&token, tokenFlag, "", tokenUsage)
	// debug = *flag.Bool(debugFlag, false, debugUsage)
	flag.StringVar(&autoComplete, autoCompleteFlag, "", autoCompleteUsage)
	_ = *flag.Bool(stdioFlag, true, stdioUsage) // Some editors pass it so we need to not error on it
	flag.Parse()

	if autoComplete == "" {
		autoComplete = "off"
	}

	switch autoComplete {
	case "off", "init", "always":
		// valid
	default:
		fmt.Println("Invalid autoComplete value. Must be 'off', 'init' or 'always'")
		os.Exit(1)
	}

	server := lsp.NewServer(url, token)
	server.AutoComplete = autoComplete

	<-jsonrpc2.NewConn(context.Background(), jsonrpc2.NewBufferedStream(stdrwc{}, jsonrpc2.VSCodeObjectCodec{}), jsonrpc2.AsyncHandler(server.Router)).DisconnectNotify()
}
