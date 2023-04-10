package types

import "github.com/sourcegraph/go-lsp"

type MemoryFileMap map[lsp.DocumentURI]string

type LLMSPSettings struct {
	Sourcegraph *SourcegraphSettings `json:"sourcegraph"`
}

type SourcegraphSettings struct {
	URL            string   `json:"url"`
	AccessToken    string   `json:"accessToken"`
	RepoEmbeddings []string `json:"repos"`
}

type LLMSPConfig struct {
	Settings SourcegraphSettings `json:"sourcegraph"`
}

type ConfigurationSettings struct {
	LLMSP LLMSPSettings `json:"llmsp"`
}

type TextDocumentEdit struct {
	TextDocument lsp.VersionedTextDocumentIdentifier `json:"textDocument"`
	Edits        []lsp.TextEdit                      `json:"edits"`
}

type WorkspaceEdit struct {
	DocumentChanges []TextDocumentEdit `json:"documentChanges"`
}

type ApplyWorkspaceEditParams struct {
	Edit WorkspaceEdit `json:"edit"`
}

type DidChangeConfigurationParams struct {
	Settings ConfigurationSettings `json:"settings"`
}
