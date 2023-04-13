package main

import (
	"context"
	"flag"
	"os"

	"github.com/pjlast/llmsp/lsp"
	"github.com/pjlast/llmsp/types"
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

func main() {
	url := *flag.String("url", "", "LLM provider URL")
	token := *flag.String("token", "", "LLM provider token")
	debug := *flag.Bool("debug", false, "Debug mode")
	flag.Parse()

	llmsp := &lsp.Server{
		FileMap:     make(types.MemoryFileMap),
		URL:         url,
		AccessToken: token,
		Debug:       debug,
	}
	<-jsonrpc2.NewConn(context.Background(), jsonrpc2.NewBufferedStream(stdrwc{}, jsonrpc2.VSCodeObjectCodec{}), jsonrpc2.AsyncHandler(llmsp.Handle())).DisconnectNotify()
}
