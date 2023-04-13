package main

import (
	"context"
	"fmt"
	"os"
	"time"

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
			Text: `Suggest a code snippet to complete the following code. Continue from where I left off.:
func isPrim`,
		},
		{
			Speaker: "ASSISTANT",
			Text:    "```go\n",
		},
	}

	params := claude.DefaultCompletionParameters(messages)

	start := time.Now()
	retChan, _ := cli.StreamCompletion(context.Background(), params, true)
	for range retChan {
	}
	fmt.Println(time.Now().Sub(start))

	// start := time.Now()
	// _, err := cli.GetCompletion(context.Background(), params, true)
	// if err != nil {
	// 	fmt.Println(err)
	// 	return
	// }
	// fmt.Println(time.Now().Sub(start))
}
