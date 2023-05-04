package providers

import (
	"encoding/json"
	"io/ioutil"

	"github.com/google/uuid"
	"github.com/pjlast/llmsp/sourcegraph/embeddings"
)

type publicArgument struct {
	ExtensionDetails extensionDetails `json:"extensionDetails"`
	ServerEndpoint   string           `json:"serverEndpoint"`
	Version          string           `json:"version"`
}

type extensionDetails struct {
	IDE              string `json:"ide"`
	IDEExtensionType string `json:"ideExtensionType"`
}

const sourcegraphDotComURL = "https://sourcegraph.com"

type eventLogger struct {
	serverURL      string
	uid            string
	serverClient   *embeddings.Client
	dotcomClient   *embeddings.Client
	argument       string
	publicArgument string
}

func NewEventLogger(serverClient *embeddings.Client, dotcomClient *embeddings.Client, serverURL string, uidFile string) *eventLogger {
	newInstall := false
	uid, err := readUidFromFile(uidFile)
	if err != nil {
		newInstall = true
		uid = uuid.New().String()
		err = ioutil.WriteFile(uidFile, []byte(uid), 0o644)
		if err != nil {
			panic(err)
		}
	}

	publicArgument, _ := json.Marshal(publicArgument{
		ServerEndpoint: serverURL,
		ExtensionDetails: extensionDetails{
			IDE:              "Neovim",
			IDEExtensionType: "Cody",
		},
		Version: "0.1.0",
	})

	eventLogger := &eventLogger{
		uid:            uid,
		serverClient:   serverClient,
		dotcomClient:   dotcomClient,
		argument:       string(publicArgument),
		publicArgument: string(publicArgument),
	}
	if newInstall {
		eventLogger.Log("CodyInstalled")
	}

	return eventLogger
}

func readUidFromFile(path string) (string, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (l *eventLogger) Log(eventName string) {
	// Don't log events if the UID has not yet been generated.
	if l.uid == "" {
		return
	}

	go func() {
		_ = l.serverClient.LogEvent(eventName, l.uid, l.argument, l.publicArgument)
		if l.serverURL != sourcegraphDotComURL {
			_ = l.dotcomClient.LogEvent(eventName, l.uid, l.argument, l.publicArgument)
		}
	}()
}
