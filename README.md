# LLMSP

POC to explore using LLMs to provide feedback in text editors using the Language Server Protocol.

This repo specifically interacts with Sourcegraph's endpionts to do two things:
1. Fetch related files in the repository using repo embedding search
2. Use these files as context for queries to the LLM

This logic is lifted from the Cody VS Code extension.

## How to use it

You'll need a Cody-enabled Sourceraph server URL as well as a Sourcegraph Access Token.

1. In `lsp/lsp.go`, modify the the `sgAccessToken` and `instanceURL` variables to your specific values.
2. Run `go install`. This will compile the `llmsp` binary and copy it to your `$GOPATH/bin`. This directory needs to be on your `$PATH`. Alternatively, use `go build` and copy the binary yourself.

### Configure the LSP

In your Neovim LSP configuration, add the following lines:

```lua
local lspconfig = require('lspconfig')
local configs = require('lspconfig.configs')
if not configs.llmsp then
  configs.llmsp = {
    default_config = {
      cmd = { 'llmsp' },
      filetypes = { 'go' },
      root_dir = function(fname)
        return lspconfig.util.find_git_ancestor(fname)
      end,
      settings = {},
    },
  }
end

lspconfig.llmsp.setup {}
```

For other editors you are on your own :)

## Play around with it

The prompts are set up to try and get the LLM to output feedback in a specific format. Modify it and the prompts as you wish.

Currently the LSP asks for code improvements whenever you save the file.
