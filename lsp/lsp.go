package lsp

import (
  "fmt"
)

type Header map[string]string

func (h Header) Add(key, value string) {
  h[key] = value
}

func (h Header) Get(key string) string {
  return h[key]
}

func (h Header) Remove(key string) {
  delete(h, key)
}

type LSPMessage struct {
  Header Header
  Content string
}

func (m *LSPMessage) Encode() (encoded string) {
  for key, value := range m.Header {
    encoded += fmt.Sprintf("%s: %s\r\n", key, value)
  }
  encoded += fmt.Sprintf("\r\n%s", m.Content)

  return
}
