env GOOS=darwin GOARCH=arm64 go build -o build/llmsp-arm64-darwin .
env GOOS=darwin GOARCH=amd64 go build -o build/llmsp-amd64-darwin .
env GOOS=linux GOARCH=amd64 go build -o build/llmsp-amd64-linux .
env GOOS=linux GOARCH=arm64 go build -o build/llmsp-arm64-linux .
env GOOS=windows GOARCH=amd64 go build -o build/llmsp-amd64-windows.exe .
