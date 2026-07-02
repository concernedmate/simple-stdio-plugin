package main

import (
	"log"

	simplestdioplugin "github.com/concernedmate/simple-stdio-plugin"
)

var plugin *simplestdioplugin.PluginData

func main() {
	router := map[string]func(data []byte) ([]byte, error){
		"command": func(data []byte) ([]byte, error) {
			return data, nil
		},
		"call": func(data []byte) ([]byte, error) {
			return plugin.Command(simplestdioplugin.MessageInput{
				Function: "reverse",
				Data:     data,
			})
		},
	}

	plugin = simplestdioplugin.NewPluginClient(simplestdioplugin.PluginClientConfig{Router: router})
	if err := simplestdioplugin.PluginServe(plugin); err != nil {
		log.Fatal(err)
	}
}
