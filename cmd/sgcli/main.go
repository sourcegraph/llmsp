package main

import (
	"fmt"
	"io/ioutil"
	"os"

	"github.com/pjlast/llmsp/sourcegraph/embeddings"
)

func main() {
	srcToken := os.Getenv("SRC_TOKEN")
	srcURL := os.Getenv("SRC_URL")

	buf, err := ioutil.ReadFile("main.go")
	if err != nil {
		fmt.Println(err)
		return
	}

	cli := embeddings.NewClient(srcURL, srcToken, nil)
	embeddingResults, err := cli.GetEmbeddings("UmVwb3NpdG9yeTozOTk=", string(buf), 8, 2)
	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println("RESULTS", embeddingResults)
}
