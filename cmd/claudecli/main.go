package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"strings"

	"github.com/pjlast/llmsp/claude"
)

func main() {
	srcURL := os.Getenv("SRC_URL")
	srcToken := os.Getenv("SRC_TOKEN")
	cli := claude.NewClient(srcURL, srcToken, nil)

	buf, err := ioutil.ReadFile("main.go")
	if err != nil {
		fmt.Println(err)
		return
	}

	lines := strings.Split(string(buf), "\n")
	numberedLines := make([]string, len(lines))
	for i, line := range lines {
		numberedLines[i] = fmt.Sprintf("%d. %s", i, line)
	}

	numberedFile := strings.Join(numberedLines, "\n")

	messages := []claude.Message{
		{
			Speaker: "assistant",
			Text: `I am Cody, an AI-powered coding assistant developed by Sourcegraph. I operate inside a Language Server Protocol implementation. My task is to help programmers with programming tasks in the Go programming language.
I have access to your currently open files in the editor.
I will generate suggestions as concisely and clearly as possible.
I only suggest something if I am certain about my answer.
I suggest improvements in the following format:
Line number: suggestion`,
		},
		{
			Speaker: "human",
			Text: fmt.Sprintf(`Suggest improvements to this code:

%s`, numberedFile),
		},
		{
			Speaker: "assistant",
			Text:    "Line",
		},
	}

	params := claude.DefaultCompletionParameters(messages)

	completion, err := cli.GetCompletion(params)
	if err != nil {
		fmt.Println(err)
		return
	}

	for val := range completion {
		fmt.Println(val)
	}
}
