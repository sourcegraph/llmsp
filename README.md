# LLMSP (very WIP)

POC to explore using LLMs to provide feedback in text editors using the Language Server Protocol.

This repo specifically interacts with Sourcegraph's endpionts to do two things:
1. Fetch related files in the repository using repo embedding search
2. Use these files as context for queries to the LLM

This logic is lifted from the Cody VS Code extension.

## How to use it

You'll need a Cody-enabled Sourcegraph server URL as well as a Sourcegraph Access Token.

```
Run `go install`. This will compile the `llmsp` binary and copy it to your `$GOPATH/bin`. This directory needs to be on your `$PATH`. Alternatively, use `go build` and copy the binary yourself.
```

### Configure the LSP

Important bits:

Whatever you use to configure your LSP, it needs to provide a configuration in the form of:

```json
{
  "llmsp": {
    "sourcegraph": {
      "url": "SOURCEGRAPH_URL",
      "accessToken": "SOURCEGRAPH_ACCESS_TOKEN"
    }
  }
}
```

See below example configurations for examples.

#### neovim/nvim-lspconfig

In your Neovim LSP config:

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

lspconfig.llmsp.setup({
  settings = {
    llmsp = {
      sourcegraph = {
        url = SOURCEGRAPH_ENDPOINT,
        accessToken = SOURCEGRAPH_ACCESSTOKEN
      }
    }
  }
})
```

#### neoclide/coc.nvim

In your `coc-settings.json` (open it with `:CocConfig`):

```json
{
  "languageserver": {
    "llmsp": {
      "command": "llmsp",
      "rootPatterns": ["go.mod"],
      "trace.server": "verbose",
      "filetypes": ["go"],
      "settings": {
        "llmsp": {
          "sourcegraph": {
            "url": SOURCEGRAPH_ENDPOINT,
            "accessToken": SOURCEGRAPH_ACCESSTOKEN
          }
        }
      }
    }
  }
}
```

For other editors you are on your own :)

It's also recommended to have a way to trigger code actions while in `VISUAL` mode, as the text selection is used for some code actions.

## Neovim tips

I've found the following setup to work rather well:

```lua
-- ...LSP keymaps
-- Trigger code actions in NORMAL and VISUAL mode
vim.keymap.set("n", "<space>ca", vim.lsp.buf.code_action, opts)
vim.keymap.set("v", "<space>ca", vim.lsp.buf.code_action, opts)

-- ... nvim-cmp settings
mapping = cmp.mapping.preset.insert({
-- ...
  ['<C-Space>'] = cmp.mapping.complete(), -- Manual trigger completions
-- ...
}),
```

## Play around with it

Try to add your own code actions. Use the existing ones to see how to send edits back to the editor, play around with the prompts, etc.
