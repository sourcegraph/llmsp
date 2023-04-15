package main

import (
	"context"
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
			Speaker: "ASSISTANT",
			Text: `I am Cody, an AI-powered coding assistant developed by Sourcegraph. I operate inside a Language Server Protocol implementation. My task is to help programmers with programming tasks in the Go programming language.
I have access to your currently open files in the editor.
I will generate suggestions as concisely and clearly as possible.
I only suggest something if I am certain about my answer.`,
		},
		{
			Speaker: "HUMAN",
			Text: `Here are the contents of the file I'm working in. My cursor is on line 5.

` + "```go" + `
1. package main
2.
3. func main() {
4.   messages := []string{"First", "Second", "Third"}
5.   i
6. }
` + "```",
		},
		{
			Speaker: "ASSISTANT",
			Text:    "Ok.",
		},
		{
			Speaker: "HUMAN",
			Text:    `Suggest Go code to complete the code. Continue from where I left off. Return only code.`,
		},
		{
			Speaker: "ASSISTANT",
			Text:    "```go\n",
		},
	}

	params := claude.DefaultCompletionParameters(messages)

	completion, err := cli.GetCompletion(context.Background(), params, true)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(completion)
}
