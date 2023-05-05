TAG=$(git describe --tags --always)

env GOOS=darwin GOARCH=arm64 go build -o build/llmsp-arm64-darwin-${TAG} .
env GOOS=darwin GOARCH=amd64 go build -o build/llmsp-amd64-darwin-${TAG} .
env GOOS=linux GOARCH=amd64 go build -o build/llmsp-amd64-linux-${TAG} .
env GOOS=linux GOARCH=arm64 go build -o build/llmsp-arm64-linux-${TAG} .
env GOOS=windows GOARCH=amd64 go build -o build/llmsp-amd64-windows-${TAG}.exe .
