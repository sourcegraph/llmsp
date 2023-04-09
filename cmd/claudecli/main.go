package main

import (
	"fmt"
	"os"

	"github.com/pjlast/llmsp/claude"
)

func main() {
	srcURL := os.Getenv("SRC_URL")
	srcToken := os.Getenv("SRC_TOKEN")
	cli := claude.NewClient(srcURL, srcToken, nil)

	messages := []claude.Message{
		{
			Speaker: "assistant",
			Text: `I am Cody, an AI-powered coding assistant developed by Sourcegraph. I operate inside a Language Server Protocol implementation. My task is to help programmers with programming tasks in the Go programming language.
I have access to your currently open files in the editor.
I will generate suggestions as concisely and clearly as possible.
I only suggest something if I am certain about my answer.`,
		},
		{
			Speaker: "human",
			Text: `Suggest a code snippet to complete the following code:
func printWords(words []string) {
`,
		},
		{
			Speaker: "assistant",
			Text: "```go" + `
func printWords(words []string) {`,
		},
	}

	params := claude.DefaultCompletionParameters(messages)

	completion, err := cli.GetCompletion(params)
	if err != nil {
		fmt.Println(err)
		return
	}

	complete := ""
	for val := range completion {
		complete = val
	}

	fmt.Println(complete)
}
