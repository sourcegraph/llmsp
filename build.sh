TAG=$(git describe --tags --always)

env GOOS=darwin GOARCH=arm64 go build -o build/llmsp-${TAG}-arm64-darwin .
env GOOS=darwin GOARCH=amd64 go build -o build/llmsp-${TAG}-amd64-darwin .
env GOOS=linux GOARCH=amd64 go build -o build/llmsp-${TAG}-amd64-linux .
env GOOS=linux GOARCH=arm64 go build -o build/llmsp-${TAG}-arm64-linux .
env GOOS=windows GOARCH=amd64 go build -o build/llmsp-${TAG}-amd64-windows.exe .
